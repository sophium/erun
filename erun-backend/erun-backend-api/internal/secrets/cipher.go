// Package secrets provides authenticated encryption for tenant secrets (k3s admin
// tokens, BYO-cloud credentials) so the database never holds them in plaintext.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// Cipher is AES-256-GCM; its key also seeds DeriveToken's domain-separated HMAC-KDF.
type Cipher struct {
	aead cipher.AEAD
	key  []byte
}

// NewCipher has no plaintext fallback: a missing or invalid key is fatal.
func NewCipher(keyBase64 string) (*Cipher, error) {
	keyBase64 = strings.TrimSpace(keyBase64)
	if keyBase64 == "" {
		return nil, fmt.Errorf("secrets key is required (set ERUN_SECRETS_KEY to a base64-encoded 32-byte key)")
	}
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode secrets key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets key must be 32 bytes (AES-256), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Cipher{aead: aead, key: key}, nil
}

// DeriveToken is deterministic per (key, label), so a resumed provisioning re-derives the same k3s token.
func (c *Cipher) DeriveToken(label string) string {
	extract := hmac.New(sha256.New, c.key)
	extract.Write([]byte("erun-token-derivation-v1"))
	prk := extract.Sum(nil)
	expand := hmac.New(sha256.New, prk)
	expand.Write([]byte(label))
	return base64.RawURLEncoding.EncodeToString(expand.Sum(nil))
}

// Encrypt seals plaintext and returns nonce||ciphertext.
func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt fails on a truncated value or bad authentication tag.
func (c *Cipher) Decrypt(sealed []byte) ([]byte, error) {
	nonceSize := c.aead.NonceSize()
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: %d bytes", len(sealed))
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// GenerateKey mints a base64-encoded 32-byte key for a deployment's ERUN_SECRETS_KEY.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
