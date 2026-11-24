package imapclient

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyncProviderIgnoresPartialLength(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "partial.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	account, err := store.SaveAccount(ctx, storage.Account{Email: "partial@example.com", Provider: "qq", AuthType: "password", IMAPHost: "fixture", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "fixture", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("fixture"), ProxyMode: "direct", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer func() {
		if err := serverConn.Close(); err != nil {
			t.Error(err)
		}
	}()
	serverDone := make(chan error, 1)
	go func() {
		if _, err := fmt.Fprint(serverConn, "* PREAUTH [CAPABILITY IMAP4rev1] fixture\r\n"); err != nil {
			serverDone <- err
			return
		}
		reader := bufio.NewReader(serverConn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				serverDone <- err
				return
			}
			tag := strings.Fields(line)[0]
			switch {
			case strings.Contains(line, "SELECT "):
				_, err = fmt.Fprintf(serverConn, "* 2 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n* OK [UIDNEXT 3] next\r\n%s OK selected\r\n", tag)
			case strings.Contains(line, "FETCH "):
				if !strings.Contains(line, "BODY.PEEK[TEXT]<0.4096>") {
					serverDone <- fmt.Errorf("client did not request a 4KiB partial")
					return
				}
				for i, body := range []string{strings.Repeat("a", 8192), "next"} {
					if _, err = fmt.Fprintf(serverConn, "* %d FETCH (UID %d BODYSTRUCTURE (\"TEXT\" \"PLAIN\" (\"CHARSET\" \"UTF-8\") NIL NIL \"7BIT\" %d 1 NIL NIL NIL NIL) BODY[TEXT]<0> {%d}\r\n%s)\r\n", i+1, i+1, len(body), len(body), body); err != nil {
						break
					}
				}
				if err == nil {
					_, err = fmt.Fprintf(serverConn, "%s OK fetched\r\n", tag)
				}
			default:
				err = fmt.Errorf("unexpected fixture command")
			}
			if err != nil {
				serverDone <- err
				return
			}
		}
	}()
	stop := context.AfterFunc(ctx, func() {
		if err := clientConn.Close(); err != nil {
			t.Error(err)
		}
	})
	client := &Client{IMAP: upstream.New(clientConn, nil), conn: clientConn, stop: stop}
	defer func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := client.IMAP.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	count, err := client.Sync(ctx, store, account, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("synced %d messages; remainder disrupted next response", count)
	}
	page, err := store.ListEmails(ctx, storage.EmailQuery{})
	if err != nil {
		t.Fatal(err)
	}
	snippets := map[uint32]string{}
	for _, email := range page.Emails {
		snippets[email.UID] = email.Snippet
	}
	if snippets[1] != strings.Repeat("a", 500) || snippets[2] != "next" {
		t.Fatal("snippet limit or next response decoding failed")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}
