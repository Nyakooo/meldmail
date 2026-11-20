package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	netmail "net/mail"
	"path"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/smtpclient"
	"github.com/nyakooo/meldmail/internal/storage"
)

var ErrInvalidMessage = errors.New("invalid outgoing message")
var mailHTML = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "div", "span", "strong", "b", "em", "i", "u", "s", "blockquote", "pre", "code", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "table", "thead", "tbody", "tr", "th", "td", "a")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(true)
	p.SkipElementsContent("script", "style", "iframe", "object", "embed", "form", "svg", "math")
	return p
}()

type EmailContent struct {
	storage.Email
	imapclient.Content
}

func (s *Service) Read(ctx context.Context, id int64) (result EmailContent, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	release, err := s.acquireInteractive(ctx, true)
	if err != nil {
		return result, err
	}
	defer release()
	e, _, f, c, err := s.openEmail(ctx, id)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := c.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	content, err := c.Read(ctx, e, f)
	if err != nil {
		return result, err
	}
	var sanitized bytes.Buffer
	if err := mailHTML.SanitizeReaderToWriter(strings.NewReader(content.HTML), &bodyWriter{writer: &sanitized, remaining: imapclient.MaxBodyBytes}); err != nil {
		return result, fmt.Errorf("mail sanitize: %w", err)
	}
	content.HTML = sanitized.String()
	content.Text = strings.ToValidUTF8(content.Text, "�")
	if int64(len(content.Text)) > imapclient.MaxBodyBytes {
		return result, imapclient.ErrBodyTooLarge
	}
	return EmailContent{Email: e, Content: content}, nil
}
func (s *Service) Download(ctx context.Context, id int64, partID string, consume func(imapclient.Part, io.Reader) error) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	release, err := s.acquireInteractive(ctx, false)
	if err != nil {
		return err
	}
	defer release()
	e, _, f, c, err := s.openEmail(ctx, id)
	if err != nil {
		return err
	}
	defer func() {
		if err := c.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	return c.Download(ctx, e, f, partID, consume)
}
func (s *Service) openEmail(ctx context.Context, id int64) (storage.Email, storage.Account, storage.Folder, *imapclient.Client, error) {
	e, err := s.Store.Email(ctx, id)
	if err != nil {
		return e, storage.Account{}, storage.Folder{}, nil, err
	}
	a, err := s.Store.Account(ctx, e.AccountID)
	if err != nil {
		return e, a, storage.Folder{}, nil, err
	}
	f, err := s.Store.Folder(ctx, e.FolderID)
	if err != nil {
		return e, a, f, nil, err
	}
	credentials, err := s.Credentials(a)
	if err != nil {
		return e, a, f, nil, err
	}
	dialer, err := s.Dialer(ctx, a)
	if err != nil {
		return e, a, f, nil, err
	}
	client, err := imapclient.Open(ctx, a, credentials, dialer, s.TLSConfig)
	return e, a, f, client, err
}
func (s *Service) acquireInteractive(ctx context.Context, body bool) (func(), error) {
	if body {
		select {
		case s.bodyMemory <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	select {
	case s.interactive <- struct{}{}:
	case <-ctx.Done():
		if body {
			<-s.bodyMemory
		}
		return nil, ctx.Err()
	}
	return func() {
		<-s.interactive
		if body {
			<-s.bodyMemory
		}
	}, nil
}

type SendInput struct {
	AccountID int64    `json:"account_id"`
	To        []string `json:"to"`
	CC        []string `json:"cc,omitempty"`
	Subject   string   `json:"subject"`
	Text      string   `json:"text"`
	HTML      string   `json:"html,omitempty"`
	Action    string   `json:"action,omitempty"`
	EmailID   int64    `json:"email_id,omitempty"`
}

func NormalizeSend(input SendInput) (SendInput, error) {
	if input.Action == "" {
		input.Action = "send"
	}
	if input.AccountID < 1 || input.Action != "send" && input.Action != "reply" && input.Action != "forward" || input.Action != "send" && input.EmailID < 1 || len(input.To)+len(input.CC) > 100 || len(input.Subject) > 8192 || !smtpclient.ValidateHeader(input.Subject) || int64(len(input.Text)+len(input.HTML)) > imapclient.MaxBodyBytes {
		return input, ErrInvalidMessage
	}
	if input.Action != "reply" && len(input.To) == 0 {
		return input, ErrInvalidMessage
	}
	for _, list := range [][]string{input.To, input.CC} {
		for i, value := range list {
			if !smtpclient.ValidateHeader(value) {
				return input, ErrInvalidMessage
			}
			address, err := netmail.ParseAddress(value)
			if err != nil {
				return input, ErrInvalidMessage
			}
			list[i] = address.Address
		}
	}
	return input, nil
}
func (s *Service) Send(ctx context.Context, input SendInput) (result error) {
	input, err := NormalizeSend(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	account, err := s.Store.Account(ctx, input.AccountID)
	if err != nil {
		return err
	}
	message := smtpclient.Message{From: account.Email, FromName: account.DisplayName, To: input.To, CC: input.CC, Subject: input.Subject, Text: input.Text, HTML: input.HTML}
	if input.Action != "send" {
		original, err := s.Store.Email(ctx, input.EmailID)
		if err != nil {
			return err
		}
		if original.AccountID != account.ID {
			return ErrInvalidMessage
		}
		if input.Action == "reply" {
			if len(message.To) == 0 {
				message.To = []string{original.FromAddr}
			}
			if message.Subject == "" {
				message.Subject = "Re: " + original.Subject
			}
			if original.MessageID != "" {
				id := "<" + strings.Trim(original.MessageID, "<>") + ">"
				if !smtpclient.ValidateHeader(id) {
					return ErrInvalidMessage
				}
				message.InReplyTo = id
				message.References = id
			}
		} else {
			if message.Subject == "" {
				message.Subject = "Fwd: " + original.Subject
			}
			source, err := s.Read(ctx, original.ID)
			if err != nil {
				return err
			}
			text := source.Text
			if text == "" {
				text = html.UnescapeString(bluemonday.StrictPolicy().Sanitize(source.HTML))
			}
			message.HTML = ""
			message.Text = input.Text + "\n\n---------- Forwarded message ----------\nFrom: " + original.FromAddr + "\nSubject: " + original.Subject + "\n\n" + text
			for _, part := range source.Attachments {
				part := part
				filename := path.Base(strings.ReplaceAll(part.Filename, "\\", "/"))
				filename = strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(filename)
				if len(filename) > 255 {
					filename = "attachment-" + part.ID
				}
				message.Attachments = append(message.Attachments, smtpclient.Attachment{Name: filename, ContentType: part.MediaType, Write: func(w io.Writer) (int64, error) { return s.forwardAttachment(ctx, original.ID, part.ID, w) }})
			}
		}
	}
	checked, err := NormalizeSend(SendInput{AccountID: input.AccountID, Action: input.Action, EmailID: input.EmailID, To: message.To, CC: message.CC, Subject: message.Subject, Text: message.Text, HTML: message.HTML})
	if err != nil {
		return err
	}
	message.To = checked.To
	message.CC = checked.CC
	release, err := s.acquireInteractive(ctx, false)
	if err != nil {
		return err
	}
	defer release()
	credentials, err := s.Credentials(account)
	if err != nil {
		return err
	}
	dialer, err := s.Dialer(ctx, account)
	if err != nil {
		return err
	}
	client, err := smtpclient.Open(ctx, account, credentials, dialer, s.TLSConfig)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	if err := client.Send(ctx, message); err != nil {
		return fmt.Errorf("mail send: %w", err)
	}
	return nil
}

func (s *Service) forwardAttachment(ctx context.Context, id int64, partID string, writer io.Writer) (written int64, result error) {
	e, _, f, c, err := s.openEmail(ctx, id)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := c.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	result = c.Download(ctx, e, f, partID, func(part imapclient.Part, reader io.Reader) error {
		var err error
		written, err = io.Copy(writer, reader)
		return err
	})
	return written, result
}

type bodyWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *bodyWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, imapclient.ErrBodyTooLarge
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	return n, err
}
