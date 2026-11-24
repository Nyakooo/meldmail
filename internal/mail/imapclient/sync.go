package imapclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/textproto"
	"strings"
	"unicode"

	"github.com/emersion/go-imap/v2"
	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

func (c *Client) Sync(ctx context.Context, s *storage.Store, a storage.Account, onEmail func(storage.Email) error) (int, error) {
	selected, err := c.IMAP.Select("INBOX", nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("imap select: %w", err)
	}
	f, err := s.PrepareFolder(ctx, a.ID, "INBOX", "inbox", selected.UIDValidity)
	if err != nil {
		return 0, err
	}
	if selected.NumMessages == 0 {
		return 0, nil
	}
	var set imap.NumSet
	if f.LastUID == 0 {
		start := uint32(1)
		if selected.NumMessages > 100 {
			start = selected.NumMessages - 99
		}
		set = imap.SeqSet{{Start: start, Stop: selected.NumMessages}}
	} else {
		if selected.UIDNext != 0 && uint32(selected.UIDNext) <= f.LastUID+1 {
			return 0, nil
		}
		set = imap.UIDSet{{Start: imap.UID(f.LastUID) + 1, Stop: 0}}
	}
	section := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierText, Partial: &imap.SectionPartial{Size: 4096}, Peek: true}
	command := c.IMAP.Fetch(set, &imap.FetchOptions{UID: true, Flags: true, InternalDate: true, Envelope: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true}, BodySection: []*imap.FetchItemBodySection{section}})
	count := 0
	highestUID := f.LastUID
	for msg := command.Next(); msg != nil; msg = command.Next() {
		e, err := metadata(msg)
		if err != nil {
			return count, c.abortFetch(command, err)
		}
		if e.UID <= f.LastUID {
			continue
		}
		saved, err := s.SaveMetadata(ctx, f, e)
		if err != nil {
			return count, c.abortFetch(command, err)
		}
		if onEmail != nil {
			if err := onEmail(saved); err != nil {
				return count, c.abortFetch(command, err)
			}
		}
		if e.UID > highestUID {
			highestUID = e.UID
		}
		count++
	}
	if err := command.Close(); err != nil {
		return count, fmt.Errorf("imap metadata fetch: %w", err)
	}
	if err := s.FinishSync(ctx, f, highestUID); err != nil {
		return count, err
	}
	return count, nil
}
func metadata(message *upstream.FetchMessageData) (storage.Email, error) {
	e := storage.Email{ToAddrs: "[]", CCAddrs: "[]"}
	var raw bytes.Buffer
	var structure imap.BodyStructure
	for item := message.Next(); item != nil; item = message.Next() {
		switch v := item.(type) {
		case upstream.FetchItemDataUID:
			e.UID = uint32(v.UID)
		case upstream.FetchItemDataFlags:
			for _, flag := range v.Flags {
				if flag == imap.FlagSeen {
					e.IsRead = true
				}
				if flag == imap.FlagFlagged {
					e.IsStarred = true
				}
			}
		case upstream.FetchItemDataInternalDate:
			e.Date = v.Time.Unix()
		case upstream.FetchItemDataEnvelope:
			if v.Envelope == nil {
				continue
			}
			env := v.Envelope
			e.Subject = env.Subject
			e.MessageID = env.MessageID
			e.InReplyTo = strings.Join(env.InReplyTo, " ")
			if len(env.From) > 0 {
				e.FromName = env.From[0].Name
				e.FromAddr = env.From[0].Addr()
			}
			to, err := addresses(env.To)
			if err != nil {
				return e, err
			}
			cc, err := addresses(env.Cc)
			if err != nil {
				return e, err
			}
			e.ToAddrs = to
			e.CCAddrs = cc
		case upstream.FetchItemDataBodyStructure:
			structure = v.BodyStructure
		case upstream.FetchItemDataBodySection:
			if v.Literal != nil {
				if _, err := io.Copy(&raw, io.LimitReader(v.Literal, 4096)); err != nil {
					return e, fmt.Errorf("imap snippet read: %w", err)
				}
				// Some providers ignore the requested partial length. Retain only
				// 4KiB, then discard the remainder as a stream before decoding the
				// next response. The connection context bounds a stalled transfer.
				if _, err := io.Copy(io.Discard, v.Literal); err != nil {
					return e, fmt.Errorf("imap snippet drain: %w", err)
				}
			}
		}
	}
	if structure != nil {
		structure.Walk(func(path []int, body imap.BodyStructure) bool {
			if part, ok := body.(*imap.BodyStructureSinglePart); ok {
				if part.Filename() != "" || part.Disposition() != nil && part.Disposition().Value == "attachment" {
					e.HasAttachments = true
				}
			}
			return true
		})
	}
	e.Snippet = snippet(raw.Bytes(), structure)
	return e, nil
}
func addresses(values []imap.Address) (string, error) {
	addrs := make([]string, 0, len(values))
	for _, a := range values {
		if a.Addr() != "" {
			addrs = append(addrs, a.Addr())
		}
	}
	data, err := json.Marshal(addrs)
	if err != nil {
		return "", fmt.Errorf("imap addresses: %w", err)
	}
	return string(data), nil
}
func snippet(data []byte, structure imap.BodyStructure) string {
	if structure == nil {
		return ""
	}
	var header textproto.MIMEHeader = make(textproto.MIMEHeader)
	media := structure.MediaType()
	switch body := structure.(type) {
	case *imap.BodyStructureSinglePart:
		header.Set("Content-Type", mime.FormatMediaType(media, body.Params))
		header.Set("Content-Transfer-Encoding", body.Encoding)
	case *imap.BodyStructureMultiPart:
		// The server partial is multipart TEXT; recover only a bounded first text part.
		boundary := ""
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "--") {
				boundary = strings.TrimPrefix(line, "--")
				break
			}
		}
		if boundary == "" {
			return ""
		}
		return multipartSnippet(multipart.NewReader(bytes.NewReader(data), boundary), 0)
	}
	if !strings.HasPrefix(media, "text/") {
		return ""
	}
	return cleanSnippet(bytes.NewReader(data), header, media)
}
func cleanSnippet(reader io.Reader, header textproto.MIMEHeader, media string) string {
	switch strings.ToLower(header.Get("Content-Transfer-Encoding")) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	}
	_, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err == nil && params["charset"] != "" {
		decoded, e := charset.NewReaderLabel(params["charset"], reader)
		if e != nil {
			return ""
		}
		reader = decoded
	}
	var b bytes.Buffer
	if _, err := io.Copy(&b, io.LimitReader(reader, 16384)); err != nil && b.Len() == 0 {
		return ""
	}
	text := b.String()
	if media == "text/html" {
		var out strings.Builder
		t := html.NewTokenizer(strings.NewReader(text))
		skip := false
		for {
			kind := t.Next()
			if kind == html.ErrorToken {
				break
			}
			switch kind {
			case html.StartTagToken:
				name, _ := t.TagName()
				if string(name) == "script" || string(name) == "style" {
					skip = true
				}
				out.WriteByte(' ')
			case html.EndTagToken:
				name, _ := t.TagName()
				if string(name) == "script" || string(name) == "style" {
					skip = false
				}
				out.WriteByte(' ')
			case html.TextToken:
				if !skip {
					out.Write(t.Text())
				}
			}
		}
		text = out.String()
	}
	text = strings.ToValidUTF8(text, "")
	text = strings.Join(strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	runes := []rune(text)
	if len(runes) > 500 {
		runes = runes[:500]
	}
	return string(runes)
}

func multipartSnippet(reader *multipart.Reader, depth int) string {
	if depth >= 8 {
		return ""
	}
	for i := 0; i < 32; i++ {
		part, err := reader.NextPart()
		if err != nil {
			return ""
		}
		media, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			media = "text/plain"
		}
		if strings.HasPrefix(media, "multipart/") && params["boundary"] != "" {
			if text := multipartSnippet(multipart.NewReader(part, params["boundary"]), depth+1); text != "" {
				return text
			}
		}
		if strings.HasPrefix(media, "text/") {
			return cleanSnippet(part, part.Header, media)
		}
	}
	return ""
}

func (c *Client) abortFetch(command *upstream.FetchCommand, cause error) error {
	if err := c.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		cause = errors.Join(cause, fmt.Errorf("imap abort connection: %w", err))
	}
	// Drain after closing the socket: this releases blocked literal/message channels
	// without consuming an unbounded server payload or waiting on a live connection.
	if err := command.Close(); err != nil {
		cause = errors.Join(cause, fmt.Errorf("imap abort fetch: %w", err))
	}
	return cause
}
