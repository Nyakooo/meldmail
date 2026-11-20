package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

type Cipher struct{ aead cipher.AEAD }

func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto GCM: %w", err)
	}
	return &Cipher{aead}, nil
}
func (c *Cipher) Encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plain, nil), nil
}
func (c *Cipher) Decrypt(data []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(data) < n {
		return nil, fmt.Errorf("crypto: truncated ciphertext")
	}
	plain, err := c.aead.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("crypto decrypt: %w", err)
	}
	return plain, nil
}
