package smtpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strings"

	gomail "github.com/wneessen/go-mail"
)

type Attachment struct {
	Name, ContentType string
	Write             func(io.Writer) (int64, error)
}
type Message struct {
	Attachments                                []Attachment
	From, FromName                             string
	To, CC                                     []string
	Subject, Text, HTML, InReplyTo, References string
}

func (c *Client) Send(ctx context.Context, input Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	message := gomail.NewMsg()
	if err := message.FromFormat(input.FromName, input.From); err != nil {
		return fmt.Errorf("smtp sender: %w", err)
	}
	if err := message.To(input.To...); err != nil {
		return fmt.Errorf("smtp recipients: %w", err)
	}
	if len(input.CC) > 0 {
		if err := message.Cc(input.CC...); err != nil {
			return fmt.Errorf("smtp CC: %w", err)
		}
	}
	message.Subject(input.Subject)
	if input.InReplyTo != "" {
		message.SetGenHeader(gomail.Header("In-Reply-To"), input.InReplyTo)
	}
	if input.References != "" {
		message.SetGenHeader(gomail.Header("References"), input.References)
	}
	if input.HTML != "" {
		message.SetBodyString(gomail.TypeTextHTML, input.HTML)
	} else {
		message.SetBodyString(gomail.TypeTextPlain, input.Text)
	}
	files := make([]*gomail.File, 0, len(input.Attachments))
	for _, attachment := range input.Attachments {
		files = append(files, &gomail.File{Name: attachment.Name, ContentType: gomail.ContentType(attachment.ContentType), Enc: gomail.EncodingB64, Header: make(textproto.MIMEHeader), Writer: attachment.Write})
	}
	message.SetAttachments(files)
	if err := c.SMTP.Mail(input.From); err != nil {
		return fmt.Errorf("smtp MAIL: %w", err)
	}
	for _, recipient := range append(append([]string{}, input.To...), input.CC...) {
		if err := c.SMTP.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp RCPT: %w", err)
		}
	}
	writer, err := c.SMTP.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	_, writeErr := message.WriteTo(writer)
	if writeErr != nil {
		// Do not terminate DATA after a partial write: abort the transport instead of
		// accidentally submitting a truncated message to the SMTP server.
		if err := c.conn.Close(); err != nil {
			writeErr = errors.Join(writeErr, err)
		}
		return fmt.Errorf("smtp message write: %w", writeErr)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp delivery acknowledgement: %w", err)
	}
	return nil
}
func ValidateHeader(value string) bool { return !strings.ContainsAny(value, "\r\n\x00") }
