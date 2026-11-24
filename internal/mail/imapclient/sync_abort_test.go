package imapclient

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
)

func TestOversizedBodyAbortDoesNotDeadlock(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() {
		if err := serverConn.Close(); err != nil {
			t.Error(err)
		}
	}()
	defer func() {
		if err := clientConn.Close(); err != nil {
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
				serverDone <- err
				return
			}
			tag := strings.Fields(line)[0]
			switch {
			case strings.Contains(line, "SELECT "):
				_, err = fmt.Fprintf(serverConn, "* 1 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n* OK [UIDNEXT 2] next\r\n%s OK selected\r\n", tag)
			case strings.Contains(line, "FETCH "):
				// A server ignores the requested partial and starts an oversized literal.
				// Abort closes the connection before the advertised bytes are received.
				_, err = fmt.Fprint(serverConn, "* 1 FETCH (UID 1 BODY[1]<0> {4097}\r\n"+strings.Repeat("x", 512))
			default:
				err = fmt.Errorf("unexpected fixture command")
			}
			if err != nil {
				serverDone <- err
				return
			}
		}
	}()
	client := &Client{IMAP: upstream.New(clientConn, nil), conn: clientConn, stop: func() bool { return true }}
	if err := client.IMAP.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- client.part(storage.Email{UID: 1}, Part{Path: []int{1}}, &imap.SectionPartial{Size: 4096}, func(reader io.Reader) error { _, err := io.Copy(io.Discard, reader); return err })
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBodyTooLarge) {
			t.Fatalf("unexpected abort: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FETCH abort deadlocked on an unread literal after closing its connection")
	}
	if err := <-serverDone; err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
