package imapclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/adapter"
)

func TestReIdleAndCancellation(t *testing.T) {
	if adapter.IdleInterval("qq") != 12*time.Minute {
		t.Fatal("QQ must re-IDLE every 12 minutes")
	}
	local, remote := net.Pipe()
	serverDone := make(chan error, 1)
	cycles := make(chan struct{}, 8)
	go func() {
		defer func() {
			if err := remote.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := fmt.Fprint(remote, "* OK [CAPABILITY IMAP4rev1 IDLE] ready\r\n"); err != nil {
			serverDone <- err
			return
		}
		reader := bufio.NewReader(remote)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverDone <- nil
				return
			}
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[1] != "IDLE" {
				serverDone <- fmt.Errorf("unexpected command")
				return
			}
			if _, err := fmt.Fprint(remote, "+ idling\r\n"); err != nil {
				serverDone <- nil
				return
			}
			cycles <- struct{}{}
			done, err := reader.ReadString('\n')
			if err != nil {
				serverDone <- nil
				return
			}
			if strings.TrimSpace(done) != "DONE" {
				serverDone <- fmt.Errorf("expected DONE")
				return
			}
			if _, err := fmt.Fprintf(remote, "%s OK IDLE completed\r\n", fields[0]); err != nil {
				serverDone <- nil
				return
			}
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		if err := local.Close(); err != nil {
			return
		}
	})
	client := &Client{IMAP: upstream.New(local, nil), conn: local, stop: stop}
	if err := client.IMAP.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.watch(ctx, 10*time.Millisecond, func() {}) }()
	for i := 0; i < 3; i++ {
		select {
		case <-cycles:
		case <-time.After(time.Second):
			t.Fatal("IDLE was not renewed")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("IDLE did not exit on cancellation")
	}
	if err := client.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}
