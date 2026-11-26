package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// Failure deliberately excludes err.Error(): upstream responses can contain
// credentials, proxy URLs or message content. Only fixed diagnostic values leave here.
func Failure(err error) []slog.Attr {
	if err == nil {
		return nil
	}
	stage := "operation"
	text := strings.ToLower(err.Error())
	for _, candidate := range []string{"imap recover destination", "imap recover uid", "imap metadata fetch", "imap reconcile select", "imap idle select", "imap idle completion", "imap stop idle", "imap password authentication", "imap provider id", "imap starttls", "imap greeting", "imap tls", "imap dial", "imap select", "imap idle", "imap move", "imap store flags", "smtp authentication", "smtp starttls", "smtp greeting", "smtp ehlo", "smtp tls", "smtp dial", "logging", "config", "proxy connect response", "proxy connect", "proxy socks5", "storage"} {
		if strings.Contains(text, candidate) {
			stage = candidate
			break
		}
	}
	reason := "operation_failed"
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "timeout"
	case errors.As(err, &network) && network.Timeout():
		reason = "timeout"
	case errors.Is(err, io.EOF):
		reason = "eof"
	case errors.Is(err, io.ErrUnexpectedEOF):
		reason = "unexpected_eof"
	case errors.Is(err, net.ErrClosed):
		reason = "connection_closed"
	case strings.Contains(text, "idle connection lost"):
		reason = "idle_connection_lost"
	case strings.Contains(text, "idle unavailable"):
		reason = "idle_unavailable"
	case strings.Contains(text, "connection reset by peer"):
		reason = "connection_reset"
	case strings.Contains(text, "connection refused"):
		reason = "connection_refused"
	case strings.Contains(text, "destination cannot yet be resolved"):
		reason = "move_destination_unresolved"
	case strings.Contains(text, "destination uid is ambiguous"):
		reason = "move_destination_ambiguous"
	}
	attrs := []slog.Attr{slog.String("stage", stage), slog.String("reason", reason)}
	var response *imap.Error
	if errors.As(err, &response) {
		switch response.Type {
		case imap.StatusResponseTypeNo, imap.StatusResponseTypeBad, imap.StatusResponseTypeBye:
			attrs = append(attrs, slog.String("imap_status", string(response.Type)))
		}
		switch string(response.Code) {
		case "AUTHENTICATIONFAILED", "AUTHORIZATIONFAILED", "UNAVAILABLE", "LIMIT", "SERVERBUG", "CLIENTBUG", "CONTACTADMIN", "PRIVACYREQUIRED", "EXPIRED", "INUSE", "NONEXISTENT", "NOPERM", "CANNOT":
			attrs = append(attrs, slog.String("imap_code", string(response.Code)))
		}
	}
	return attrs
}
