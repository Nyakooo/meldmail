package storage

import (
	"context"
	"testing"
)

func TestAccountMergeRollsBackOnWriteFailure(t *testing.T) {
	store, original, _, _ := cachedBodyFixture(t)
	ctx := context.Background()
	valid := original
	valid.ID = 0
	valid.Email = "new@example.com"
	invalid := original
	invalid.ID = 0
	invalid.Email = "invalid@example.com"
	invalid.CredentialsEnc = nil
	if _, _, err := store.MergeAccounts(ctx, []Account{valid, invalid}, nil); err == nil {
		t.Fatal("invalid record accepted")
	}
	accounts, err := store.Accounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].ID != original.ID {
		t.Fatal("failed transaction left partial accounts", err)
	}
}
