package crypto

import (
	"bytes"
	"testing"
)

func TestEncryption(t *testing.T) {
	c, err := New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("secret")
	a, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, plain) {
		t.Fatal("nonrandom or plaintext ciphertext")
	}
	got, err := c.Decrypt(a)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("roundtrip", err)
	}
	a[len(a)-1] ^= 1
	if _, err := c.Decrypt(a); err == nil {
		t.Fatal("accepted tampering")
	}
	if _, err := c.Decrypt(nil); err == nil {
		t.Fatal("accepted truncated input")
	}
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("accepted short key")
	}
}
