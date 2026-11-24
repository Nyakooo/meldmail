//go:build linux

package observe

import "testing"

func TestCPUQuota(t *testing.T) {
	cores, err := parseQuota("50000 100000\n")
	if err != nil || cores == nil || *cores != 0.5 {
		t.Fatal("0.5 core quota not recognized", err)
	}
	for _, data := range []string{"max 100000", "-1 100000", "50000 0"} {
		cores, err := parseQuota(data)
		if err != nil || cores != nil {
			t.Fatal("unlimited/invalid quota treated as capacity", err)
		}
	}
	for _, data := range []string{"", "bad 100000", "NaN 100000"} {
		if _, err := parseQuota(data); err == nil {
			t.Fatal("malformed quota accepted")
		}
	}
}
