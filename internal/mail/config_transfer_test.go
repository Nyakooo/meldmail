package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyakooo/meldmail/internal/storage"
)

func transferFixture(t *testing.T, key byte) *Service {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	})
	service, err := New(store, bytes.Repeat([]byte{key}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func transferAccount(t *testing.T, s *Service, email, password string) storage.Account {
	t.Helper()
	ctx := context.Background()
	a, err := s.Prepare(ctx, AccountInput{Account: storage.Account{Email: email, Provider: "gmail", Status: "paused"}, Password: password}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.Store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func transferPayload(t *testing.T, bundle ConfigBundle, password string, change func(*configPayload)) ConfigBundle {
	t.Helper()
	cipher, err := configCipher(password, bundle.Salt)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(bundle.Data)
	if err != nil {
		t.Fatal(err)
	}
	var payload configPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		t.Fatal(err)
	}
	change(&payload)
	plain, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Data, err = cipher.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}
func TestConfigTransferReencryptsAndMergesWithoutLosingMail(t *testing.T) {
	ctx := context.Background()
	source := transferFixture(t, 1)
	target := transferFixture(t, 2)
	original := transferAccount(t, source, "shared@gmail.com", "source-authorization-code")
	if err := source.SetGlobalProxy(ctx, "socks5://fixture:proxy-secret@127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	existing := transferAccount(t, target, "SHARED@gmail.com", "old-authorization-code")
	f, err := target.Store.PrepareFolder(ctx, existing.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := target.Store.SaveMetadata(ctx, f, storage.Email{UID: 1, ToAddrs: "[]", CCAddrs: "[]", Subject: "keep existing mail", Date: 1})
	if err != nil {
		t.Fatal(err)
	}
	const password = "configuration passphrase"
	bundle, err := source.ExportConfig(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{original.Email, "source-authorization-code", "proxy-secret"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("plaintext configuration leaked")
		}
	}
	bundle = transferPayload(t, bundle, password, func(payload *configPayload) {
		duplicate := payload.Accounts[0]
		duplicate.Email = " SHARED@gmail.com "
		duplicate.DisplayName = "Merged mailbox"
		duplicate.Password = ""
		payload.Accounts = append(payload.Accounts, duplicate)
	})
	result, err := target.ImportConfig(ctx, password, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 0 || result.Updated != 1 || result.Duplicates != 1 {
		t.Fatal("incorrect merge result", result)
	}
	accounts, err := target.Store.Accounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].ID != existing.ID || accounts[0].DisplayName != "Merged mailbox" {
		t.Fatal("existing account not merged", err)
	}
	creds, err := target.Credentials(accounts[0])
	if err != nil || creds.Password != "source-authorization-code" {
		t.Fatal("imported authentication unusable", err)
	}
	if bytes.Equal(accounts[0].CredentialsEnc, original.CredentialsEnc) {
		t.Fatal("source master-key ciphertext reused")
	}
	endpoint, err := target.GlobalProxy(ctx)
	if err != nil || !strings.Contains(endpoint, "proxy-secret") {
		t.Fatal("global proxy not transferred", err)
	}
	if _, err := target.Store.Email(ctx, saved.ID); err != nil {
		t.Fatal("existing mail deleted", err)
	}
	again, err := target.ImportConfig(ctx, password, bundle)
	if err != nil || again.Added != 0 || again.Updated != 1 {
		t.Fatal("reimport not idempotent", err)
	}
}
func TestConfigTransferRejectsWrongPasswordTamperingAndPartialImports(t *testing.T) {
	ctx := context.Background()
	source := transferFixture(t, 3)
	target := transferFixture(t, 4)
	transferAccount(t, source, "one@gmail.com", "fixture-authorization")
	const password = "configuration passphrase"
	bundle, err := source.ExportConfig(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportConfig(ctx, "incorrect passphrase", bundle); err == nil {
		t.Fatal("wrong password accepted")
	}
	tampered := bundle
	tampered.Data = append([]byte{}, bundle.Data...)
	tampered.Data[len(tampered.Data)-1] ^= 1
	if _, err := target.ImportConfig(ctx, password, tampered); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	invalid := transferPayload(t, bundle, password, func(payload *configPayload) {
		bad := payload.Accounts[0]
		bad.Email = "invalid-address"
		payload.Accounts = append(payload.Accounts, bad)
	})
	if _, err := target.ImportConfig(ctx, password, invalid); err == nil {
		t.Fatal("invalid account accepted")
	}
	accounts, err := target.Store.Accounts(ctx)
	if err != nil || len(accounts) != 0 {
		t.Fatal("failed import wrote partial configuration", err)
	}
	if _, err := source.ExportConfig(ctx, "short"); err == nil {
		t.Fatal("short export password accepted")
	}
}

func TestConfigTransferRetainsOAuthTokens(t *testing.T) {
	ctx := context.Background()
	source := transferFixture(t, 5)
	target := transferFixture(t, 6)
	account, err := source.Prepare(ctx, AccountInput{Account: storage.Account{Email: "oauth@example.com", Provider: "outlook", AuthType: "oauth2", Status: "paused"}, AccessToken: "fixture-access-token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := source.Credentials(account)
	if err != nil {
		t.Fatal(err)
	}
	creds.RefreshToken = "fixture-refresh-token"
	plain, err := json.Marshal(creds)
	if err != nil {
		t.Fatal(err)
	}
	account.CredentialsEnc, err = source.Cipher.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Store.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportConfig(ctx, "configuration passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportConfig(ctx, "configuration passphrase", bundle); err != nil {
		t.Fatal(err)
	}
	accounts, err := target.Store.Accounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatal("OAuth account missing", err)
	}
	imported, err := target.Credentials(accounts[0])
	if err != nil || imported.AccessToken != creds.AccessToken || imported.RefreshToken != creds.RefreshToken {
		t.Fatal("OAuth tokens changed during transfer", err)
	}
}
