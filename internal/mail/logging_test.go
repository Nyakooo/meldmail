package mail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestMailOperationLogsFailureAfterContextCleanup(t *testing.T) {
	var out bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	defer slog.SetDefault(old)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Sync's deferred cancellation runs before diagnostic logging.
	logMailOperation(ctx, 1, "sync", time.Now(), fmt.Errorf("imap TLS: %w", io.EOF), 0)
	text := out.String()
	for _, want := range []string{`"level":"WARN"`, `"outcome":"failed"`, `"reason":"eof"`, `"account_id":1`} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure misclassified after cleanup: %s", text)
		}
	}
}
