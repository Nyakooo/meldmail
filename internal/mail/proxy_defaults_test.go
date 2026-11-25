package mail

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nyakooo/meldmail/internal/storage"
)

func TestMailboxDefaultsDirectAndSharedProxyRequiresOptIn(t *testing.T) {
	service := transferFixture(t, 7)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	// An unreachable shared proxy must not interfere with a normal mailbox.
	if err := service.SetGlobalProxy(ctx, "socks5://127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	account, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "direct@example.com", Provider: "qq"}, Password: "fixture"}, nil)
	if err != nil || account.ProxyMode != "direct" {
		t.Fatal("new mailbox inherited shared proxy", err)
	}
	dialer, err := service.Dialer(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal("shared proxy affected direct mailbox", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	account.ProxyMode = "global"
	shared, err := service.Dialer(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := shared.DialContext(ctx, "tcp", listener.Addr().String()); err == nil {
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("shared proxy selection was ignored")
	}

}

func TestSharedProxyEditsOnlyRestartOptedInWorkers(t *testing.T) {
	service := transferFixture(t, 8)
	ctx := context.Background()
	direct := transferAccount(t, service, "direct@qq.com", "fixture")
	shared := transferAccount(t, service, "shared@qq.com", "fixture")
	shared.ProxyMode = "global"
	if _, err := service.Store.SaveAccount(ctx, shared); err != nil {
		t.Fatal(err)
	}
	before, err := service.workerAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetGlobalProxy(ctx, "socks5://127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	after, err := service.workerAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, account := range before {
		if account.ID == direct.ID && account.Revision != after[i].Revision {
			t.Fatal("shared proxy edit restarts direct mailbox")
		}
		if account.ID == shared.ID && account.Revision == after[i].Revision {
			t.Fatal("shared proxy edit ignored by opted-in mailbox")
		}
	}
	if err := service.Store.PutSetting(ctx, "global_proxy_enc", "invalid ciphertext"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.workerAccounts(ctx); err != nil {
		t.Fatal("bad shared proxy blocks discovery of direct accounts", err)
	}
	if _, err := service.Dialer(ctx, direct); err != nil {
		t.Fatal("bad shared proxy blocks direct account", err)
	}
}
