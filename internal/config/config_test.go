package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("MELDMAIL_MASTER_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("missing key accepted")
	}
	t.Setenv("MELDMAIL_MASTER_KEY", strings.Repeat("ab", 32))
	t.Setenv("MELDMAIL_ADDR", "")
	t.Setenv("MELDMAIL_DB_PATH", "")
	t.Setenv("MELDMAIL_TRUST_PROXY", "true")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || !c.TrustProxy || len(c.MasterKey) != 32 {
		t.Fatal("incorrect config")
	}
	t.Setenv("MELDMAIL_TRUST_PROXY", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}
