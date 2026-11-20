package imapclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/net/html/charset"
)

const MaxBodyBytes int64 = 5 * 1024 * 1024

var ErrBodyTooLarge = errors.New("message body exceeds 5MiB")
var ErrMessageGone = errors.New("message no longer exists in this mailbox")

type Part struct {
	ID                string `json:"part_id"`
	Filename          string `json:"filename"`
	MediaType         string `json:"content_type"`
	Size              uint32 `json:"size"`
	Path              []int  `json:"-"`
	Encoding, Charset string `json:"-"`
}
type Content struct {
	HTML        string `json:"html"`
	Text        string `json:"text"`
	Attachments []Part `json:"attachments"`
}

func (c *Client) SelectMessage(ctx context.Context, e storage.Email, f storage.Folder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	selected, err := c.IMAP.Select(f.Name, nil).Wait()
	if err != nil {
		return fmt.Errorf("imap read select: %w", err)
	}
	if selected.UIDValidity != f.UIDValidity {
		return ErrMessageGone
	}
	return nil
}
func (c *Client) Structure(e storage.Email) (imap.BodyStructure, error) {
	cmd := c.IMAP.Fetch(imap.UIDSetNum(imap.UID(e.UID)), &imap.FetchOptions{UID: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true}})
	var structure imap.BodyStructure
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		for item := msg.Next(); item != nil; item = msg.Next() {
			if v, ok := item.(upstream.FetchItemDataBodyStructure); ok {
				structure = v.BodyStructure
			}
		}
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("imap body structure: %w", err)
	}
	if structure == nil {
		return nil, ErrMessageGone
	}
	return structure, nil
}
func Parts(structure imap.BodyStructure) (body *Part, attachments []Part) {
	attachments = make([]Part, 0)
	var plain, html *Part
	structure.Walk(func(path []int, bs imap.BodyStructure) bool {
		single, ok := bs.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		if len(path) == 0 {
			path = []int{1}
		}
		ids := make([]string, len(path))
		for i, n := range path {
			ids[i] = strconv.Itoa(n)
		}
		part := Part{ID: strings.Join(ids, "."), Path: append([]int(nil), path...), Filename: single.Filename(), MediaType: single.MediaType(), Size: single.Size, Encoding: single.Encoding, Charset: single.Params["charset"]}
		if _, _, err := mime.ParseMediaType(part.MediaType); err != nil {
			part.MediaType = "application/octet-stream"
		}
		disposition := single.Disposition()
		if part.Filename != "" || disposition != nil && strings.EqualFold(disposition.Value, "attachment") || !strings.HasPrefix(part.MediaType, "text/") {
			if part.Filename == "" {
				part.Filename = "attachment-" + part.ID
			}
			attachments = append(attachments, part)
			return false
		}
		if part.MediaType == "text/html" && html == nil {
			html = &part
		}
		if part.MediaType == "text/plain" && plain == nil {
			plain = &part
		}
		return true
	})
	if html != nil {
		return html, attachments
	}
	return plain, attachments
}
func decoded(reader io.Reader, part Part, convertCharset bool) (io.Reader, error) {
	switch strings.ToLower(part.Encoding) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	}
	if convertCharset && part.Charset != "" {
		converted, err := charset.NewReaderLabel(part.Charset, reader)
		if err != nil {
			return nil, fmt.Errorf("imap charset: %w", err)
		}
		reader = converted
	}
	return reader, nil
}
func (c *Client) part(e storage.Email, part Part, partial *imap.SectionPartial, consume func(io.Reader) error) error {
	section := &imap.FetchItemBodySection{Part: part.Path, Peek: true, Partial: partial}
	cmd := c.IMAP.Fetch(imap.UIDSetNum(imap.UID(e.UID)), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})
	found := false
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		for item := msg.Next(); item != nil; item = msg.Next() {
			body, ok := item.(upstream.FetchItemDataBodySection)
			if !ok || body.Literal == nil {
				continue
			}
			if !body.MatchCommand(section) {
				if _, err := io.Copy(io.Discard, body.Literal); err != nil {
					return c.abortFetch(cmd, err)
				}
				continue
			}
			if found {
				return c.abortFetch(cmd, fmt.Errorf("imap: duplicate body section"))
			}
			found = true
			if partial != nil && body.Literal.Size() > partial.Size {
				return c.abortFetch(cmd, ErrBodyTooLarge)
			}
			if err := consume(body.Literal); err != nil {
				return c.abortFetch(cmd, err)
			}
		}
	}
	if err := cmd.Close(); err != nil {
		return fmt.Errorf("imap part fetch: %w", err)
	}
	if !found {
		return ErrMessageGone
	}
	return nil
}
func (c *Client) Read(ctx context.Context, e storage.Email, f storage.Folder) (Content, error) {
	result := Content{Attachments: make([]Part, 0)}
	if err := c.SelectMessage(ctx, e, f); err != nil {
		return result, err
	}
	structure, err := c.Structure(e)
	if err != nil {
		return result, err
	}
	body, attachments := Parts(structure)
	result.Attachments = attachments
	if body == nil {
		return result, nil
	}
	// Bound both decoded content and the worst-case transfer encoding expansion.
	wireLimit := MaxBodyBytes*4 + 8192
	if int64(body.Size) > wireLimit {
		return result, ErrBodyTooLarge
	}
	var buffer bytes.Buffer
	err = c.part(e, *body, &imap.SectionPartial{Size: wireLimit + 1}, func(raw io.Reader) error {
		reader, err := decoded(raw, *body, true)
		if err != nil {
			return err
		}
		n, err := io.Copy(&buffer, io.LimitReader(reader, MaxBodyBytes+1))
		if err != nil {
			return fmt.Errorf("imap body decode: %w", err)
		}
		if n > MaxBodyBytes {
			return ErrBodyTooLarge
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if body.MediaType == "text/html" {
		result.HTML = buffer.String()
	} else {
		result.Text = buffer.String()
	}
	return result, nil
}
func (c *Client) Download(ctx context.Context, e storage.Email, f storage.Folder, id string, consume func(Part, io.Reader) error) error {
	if len(id) > 128 {
		return fmt.Errorf("imap: invalid attachment part")
	}
	if err := c.SelectMessage(ctx, e, f); err != nil {
		return err
	}
	structure, err := c.Structure(e)
	if err != nil {
		return err
	}
	_, attachments := Parts(structure)
	for _, part := range attachments {
		if part.ID == id {
			return c.part(e, part, nil, func(raw io.Reader) error {
				reader, err := decoded(raw, part, false)
				if err != nil {
					return err
				}
				return consume(part, reader)
			})
		}
	}
	return ErrMessageGone
}
