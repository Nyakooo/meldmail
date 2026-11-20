//go:build !race

package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
)

type zeroStream struct{}

func (zeroStream) Read(buffer []byte) (int, error) { clear(buffer); return len(buffer), nil }
func TestStreaming20MiBAttachment(t *testing.T) {
	const size = int64(20 * 1024 * 1024)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "stream.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	service, err := New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := cert.TLS.Clone()
	pool := x509.NewCertPool()
	pool.AddCert(cert.Certificate())
	cert.Close()
	service.TLSConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	serverDone := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := fmt.Fprint(c, "* OK [CAPABILITY IMAP4rev1] fixture\r\n"); err != nil {
			serverDone <- err
			return
		}
		reader := bufio.NewReader(c)
		encoded := base64.StdEncoding.EncodedLen(int(size))
		structure := fmt.Sprintf(`(("TEXT" "PLAIN" ("CHARSET" "UTF-8") NIL NIL "7BIT" 5 1 NIL NIL NIL NIL)("APPLICATION" "OCTET-STREAM" NIL NIL NIL "BASE64" %d NIL ("ATTACHMENT" ("FILENAME" "large.bin")) NIL NIL) "MIXED" ("BOUNDARY" "fixture") NIL NIL NIL)`, encoded)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				serverDone <- err
				return
			}
			fields := strings.Fields(line)
			tag := fields[0]
			response := tag + " OK done\r\n"
			switch {
			case strings.Contains(line, " LOGIN "):
				response = tag + " OK [CAPABILITY IMAP4rev1] authenticated\r\n"
			case strings.Contains(line, " CAPABILITY"):
				response = "* CAPABILITY IMAP4rev1\r\n" + response
			case strings.Contains(line, " SELECT "):
				response = "* FLAGS (\\Seen)\r\n* 1 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n* OK [UIDNEXT 2] next\r\n" + response
			case strings.Contains(line, "BODYSTRUCTURE"):
				response = "* 1 FETCH (UID 1 BODYSTRUCTURE " + structure + ")\r\n" + response
			case strings.Contains(line, "BODY.PEEK[2]"):
				if _, err := fmt.Fprintf(c, "* 1 FETCH (UID 1 BODY[2] {%d}\r\n", encoded); err != nil {
					serverDone <- err
					return
				}
				encoder := base64.NewEncoder(base64.StdEncoding, c)
				if _, err := io.CopyN(encoder, zeroStream{}, size); err != nil {
					serverDone <- err
					return
				}
				if err := encoder.Close(); err != nil {
					serverDone <- err
					return
				}
				response = ")\r\n" + response
			default:
				serverDone <- fmt.Errorf("unexpected streaming fixture command")
				return
			}
			if _, err := fmt.Fprint(c, response); err != nil {
				serverDone <- err
				return
			}
		}
	}()
	a, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "stream@example.com", Provider: "custom", IMAPHost: "127.0.0.1", IMAPPort: listener.Addr().(*net.TCPAddr).Port, IMAPTLS: "implicit", SMTPHost: "127.0.0.1", SMTPPort: 465, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.SaveMetadata(ctx, folder, storage.Email{UID: 1, Subject: "stream fixture", ToAddrs: "[]", CCAddrs: "[]", Date: 1})
	if err != nil {
		t.Fatal(err)
	}
	var peak atomic.Int64
	sampleDone := make(chan struct{})
	stopSamples := make(chan struct{})
	go func() {
		defer close(sampleDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSamples:
				return
			case <-ticker.C:
				data, err := os.ReadFile("/proc/self/status")
				if err != nil {
					t.Error(err)
					return
				}
				for _, line := range strings.Split(string(data), "\n") {
					if strings.HasPrefix(line, "VmRSS:") {
						fields := strings.Fields(line)
						rss, err := strconv.ParseInt(fields[1], 10, 64)
						if err != nil {
							t.Error(err)
							return
						}
						for old := peak.Load(); rss > old && !peak.CompareAndSwap(old, rss); old = peak.Load() {
						}
					}
				}
			}
		}
	}()
	var count int64
	downloadErr := service.Download(ctx, e.ID, "2", func(part imapclient.Part, reader io.Reader) error {
		var err error
		count, err = io.Copy(io.Discard, reader)
		return err
	})
	close(stopSamples)
	<-sampleDone
	if downloadErr != nil {
		t.Fatal(downloadErr)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if count != size {
		t.Fatalf("downloaded %d bytes", count)
	}
	if peak.Load() == 0 || peak.Load() >= 180*1024 {
		t.Fatalf("RSS peak %d KiB exceeds 180MiB", peak.Load())
	}
	t.Logf("20MiB streamed without buffering; RSS peak %.2f MiB", float64(peak.Load())/1024)
}
