package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestFailureClassifiesWithoutLeakingRawResponses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		err           error
		stage, reason string
	}{
		{"TLS EOF", fmt.Errorf("sync connect: imap TLS: %w", io.EOF), "imap tls", "eof"},
		{"deadline", fmt.Errorf("imap metadata fetch: %w", context.DeadlineExceeded), "imap metadata fetch", "timeout"},
		{"server rejection", fmt.Errorf("imap IDLE: %w", &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCode("UNAVAILABLE"), Text: "password=secret proxy://user:secret@host private body"}), "imap idle", "operation_failed"},
		{"untrusted status", &imap.Error{Type: imap.StatusResponseType("secret"), Code: imap.ResponseCode("secret"), Text: "secret"}, "operation", "operation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&out, nil))
			logger.LogAttrs(context.Background(), slog.LevelWarn, "failure", Failure(tc.err)...)
			text := out.String()
			if !strings.Contains(text, `"stage":"`+tc.stage+`"`) || !strings.Contains(text, `"reason":"`+tc.reason+`"`) {
				t.Fatalf("missing classification: %s", text)
			}
			for _, secret := range []string{"secret", "proxy://", "private body", "password="} {
				if strings.Contains(text, secret) {
					t.Fatalf("raw data leaked: %s", text)
				}
			}
			if tc.name == "server rejection" && !strings.Contains(text, `"imap_code":"UNAVAILABLE"`) {
				t.Fatal("safe IMAP code missing")
			}
		})
	}
}
