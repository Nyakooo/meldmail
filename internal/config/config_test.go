package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("MELDMAIL_HOST_PROXY_PORT", "")
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

func TestHostProxyPort(t *testing.T) {
	t.Setenv("MELDMAIL_MASTER_KEY", strings.Repeat("ab", 32))
	t.Setenv("MELDMAIL_TRUST_PROXY", "")
	for _, value := range []string{"", "7890", "1", "65535", "0", "65536", "http://localhost:7890", "-1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MELDMAIL_HOST_PROXY_PORT", value)
			c, err := Load()
			valid := value == "" || value == "7890" || value == "1" || value == "65535"
			if (err == nil) != valid {
				t.Fatalf("validation: %v", err)
			}
			if valid && value != "" && c.HostProxyURL != "http://127.0.0.1:"+value {
				t.Fatal("incorrect host URL")
			}
			if value == "" && c.HostProxyURL != "" {
				t.Fatal("empty config enabled proxy")
			}
		})
	}
}
