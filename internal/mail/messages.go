package mail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	netmail "net/mail"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/smtpclient"
	"github.com/nyakooo/meldmail/internal/storage"
)

var ErrInvalidMessage = errors.New("invalid outgoing message")

const bodyCachePolicyVersion = 2

var mailHTML = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "div", "span", "strong", "b", "em", "i", "u", "s", "blockquote", "pre", "code", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "table", "thead", "tbody", "tr", "th", "td", "a")
	p.AllowAttrs("href").OnElements("a")
	// Preserve mail layout using bluemonday's property-specific CSS validators.
	// URLs, external stylesheets, positioning and executable attributes stay blocked.
	p.AllowAttrs("class", "id").Matching(regexp.MustCompile(`^[a-zA-Z0-9_ -]{1,256}$`)).Globally()
	p.AllowAttrs("style").Globally()
	p.AllowStyles("display", "float", "clear", "box-sizing", "width", "max-width", "min-width", "height", "max-height", "margin", "margin-top", "margin-right", "margin-bottom", "margin-left", "padding", "padding-top", "padding-right", "padding-bottom", "padding-left", "color", "background-color", "font-family", "font-size", "font-weight", "font-style", "line-height", "text-align", "vertical-align", "text-decoration", "border", "border-top", "border-right", "border-bottom", "border-left", "border-color", "border-width", "border-style", "border-collapse", "border-spacing", "border-radius", "list-style-type", "list-style-position").Globally()
	p.AllowStyles("list-style").MatchingEnum("none").Globally()
	p.AllowAttrs("colspan", "rowspan").Matching(regexp.MustCompile(`^[1-9][0-9]{0,2}$`)).OnElements("td", "th")
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|center|right|justify)$`)).OnElements("table", "tr", "td", "th", "div", "p")
	p.AllowAttrs("width", "cellpadding", "cellspacing", "border").Matching(regexp.MustCompile(`^[0-9]{1,4}%?$`)).OnElements("table", "td", "th")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(true)
	p.SkipElementsContent("script", "style", "iframe", "object", "embed", "form", "svg", "math")
	return p
}()

type EmailContent struct {
	storage.Email
	imapclient.Content
	Cached       bool   `json:"cached"`
	Local        bool   `json:"local"`
	CacheWarning string `json:"cache_warning,omitempty"`
}

func (s *Service) Read(ctx context.Context, id int64) (result EmailContent, resultErr error) {
	resultErr = s.ReadTo(ctx, id, func(content EmailContent) error { result = content; return nil })
	return result, resultErr
}

// ReadTo keeps the body memory slot until the consumer finishes its response,
// so slow HTTP clients cannot accumulate decoded bodies outside the budget.
func (s *Service) ReadTo(ctx context.Context, id int64, consume func(EmailContent) error) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	s.prefetchMu.Lock()
	s.activeReaders++
	if s.cancelPrefetch != nil {
		s.cancelPrefetch()
	}
	s.prefetchMu.Unlock()
	defer func() {
		s.prefetchMu.Lock()
		s.activeReaders--
		s.prefetchMu.Unlock()
	}()
	select {
	case s.bodyMemory <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.bodyMemory }()
	content, err := s.readBody(ctx, id, false)
	if err != nil {
		return err
	}
	return consume(content)
}

func (s *Service) readBody(ctx context.Context, id int64, prefetch bool) (result EmailContent, resultErr error) {
	e, account, folder, err := s.emailSource(ctx, id)
	if err != nil {
		return result, err
	}
	if prefetch {
		if account.Status != "active" || e.IsRead || folder.Role != "inbox" {
			return result, nil
		}
		release, err := s.Limiter.Acquire(ctx, account.ID)
		if err != nil {
			return result, err
		}
		defer release()
	}
	source, err := bodySource(e, account, folder)
	if err != nil {
		return result, err
	}
	cached, found, err := s.Store.Body(ctx, id, source)
	if err != nil && !found {
		return result, fmt.Errorf("mail read local body: %w", err)
	}
	if found {
		var attachments []imapclient.Part
		if err := json.Unmarshal([]byte(cached.AttachmentsJSON), &attachments); err != nil {
			return result, fmt.Errorf("mail decode cached attachment metadata: %w", err)
		}
		result = EmailContent{Email: e, Content: imapclient.Content{HTML: cached.HTML, Text: cached.Text, Attachments: attachments}, Cached: true, Local: true}
		if err != nil {
			result.CacheWarning = "已读取本地正文，但缓存维护失败；请检查本地存储。"
		}
		return result, nil
	}
	release, err := s.acquireInteractive(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	if prefetch {
		s.updateBackgroundActivity(func(activity *BackgroundActivity) {
			activity.Prefetch = &BodyPrefetch{AccountID: account.ID, Subject: e.Subject}
		})
		defer s.updateBackgroundActivity(func(activity *BackgroundActivity) { activity.Prefetch = nil })
	}
	e, account, f, c, err := s.openEmail(ctx, id)
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
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var sanitized bytes.Buffer
	writer := &bodyWriter{writer: &sanitized, remaining: imapclient.MaxBodyBytes}
	if _, err := io.WriteString(writer, safeMailStyles(content.HTML)); err != nil {
		return result, fmt.Errorf("mail sanitize styles: %w", err)
	}
	if err := mailHTML.SanitizeReaderToWriter(strings.NewReader(content.HTML), writer); err != nil {
		return result, fmt.Errorf("mail sanitize: %w", err)
	}
	content.HTML = sanitized.String()
	content.Text = strings.ToValidUTF8(content.Text, "�")
	if int64(len(content.Text)) > imapclient.MaxBodyBytes {
		return result, imapclient.ErrBodyTooLarge
	}
	result = EmailContent{Email: e, Content: content}
	source, err = bodySource(e, account, f)
	if err != nil {
		return result, err
	}
	attachments, cacheErr := json.Marshal(content.Attachments)
	if cacheErr == nil {
		cacheErr = s.Store.SaveBody(ctx, e, source, storage.CachedBody{HTML: content.HTML, Text: content.Text, AttachmentsJSON: string(attachments)})
	}
	if cacheErr != nil {
		result.CacheWarning = "本地正文保存失败，本次仍可阅读；下次将重新从邮箱拉取。"
	} else {
		result.Cached = true
	}
	return result, nil
}

// Bump the policy version if the HTML sanitization policy changes. Mail source
// changes invalidate a hit even before the background sync notices them.
func bodySource(email storage.Email, account storage.Account, folder storage.Folder) (string, error) {
	source := struct {
		Version                              int
		AccountID, FolderID                  int64
		UID, Validity                        uint32
		Address, Provider, Host, TLS, Folder string
		Port                                 int
	}{bodyCachePolicyVersion, email.AccountID, email.FolderID, email.UID, folder.UIDValidity, account.Email, account.Provider, account.IMAPHost, account.IMAPTLS, folder.Name, account.IMAPPort}
	data, err := json.Marshal(source)
	if err != nil {
		return "", fmt.Errorf("mail encode body source: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
func (s *Service) Download(ctx context.Context, id int64, partID string, consume func(imapclient.Part, io.Reader) error) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	release, err := s.acquireInteractive(ctx)
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
func (s *Service) emailSource(ctx context.Context, id int64) (storage.Email, storage.Account, storage.Folder, error) {
	e, err := s.Store.Email(ctx, id)
	if err != nil {
		return e, storage.Account{}, storage.Folder{}, fmt.Errorf("mail load metadata: %w", err)
	}
	a, err := s.Store.Account(ctx, e.AccountID)
	if err != nil {
		return e, a, storage.Folder{}, fmt.Errorf("mail load account: %w", err)
	}
	f, err := s.Store.Folder(ctx, e.FolderID)
	if err != nil {
		return e, a, f, fmt.Errorf("mail load folder: %w", err)
	}
	return e, a, f, nil
}
func (s *Service) openEmail(ctx context.Context, id int64) (storage.Email, storage.Account, storage.Folder, *imapclient.Client, error) {
	e, a, f, err := s.emailSource(ctx, id)
	if err != nil {
		return e, a, f, nil, err
	}
	credentials, err := s.Credentials(a)
	if err != nil {
		return e, a, f, nil, fmt.Errorf("mail decrypt credentials: %w", err)
	}
	dialer, err := s.Dialer(ctx, a)
	if err != nil {
		return e, a, f, nil, fmt.Errorf("mail resolve proxy: %w", err)
	}
	client, err := imapclient.Open(ctx, a, credentials, dialer, s.TLSConfig)
	if err != nil {
		return e, a, f, nil, fmt.Errorf("mail open IMAP: %w", err)
	}
	return e, a, f, client, nil
}
func (s *Service) acquireInteractive(ctx context.Context) (func(), error) {
	select {
	case s.interactive <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return func() { <-s.interactive }, nil
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
	release, err := s.acquireInteractive(ctx)
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
