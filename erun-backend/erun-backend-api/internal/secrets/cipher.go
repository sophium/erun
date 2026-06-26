// Package secrets provides authenticated encryption for tenant secrets stored at
// rest in the database (the k3s admin token in context_credentials, the
// BYO-cloud credentials in cloud_provider_aliases — issues #605/#676). The
// ciphertext columns are bytea; repositories encrypt before INSERT and decrypt
// after SELECT so the database never holds a plaintext secret.
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

// Cipher is an AES-256-GCM authenticated cipher keyed by a 32-byte key. The
// 12-byte random nonce is prepended to each ciphertext, so the same plaintext
// encrypts to a different value every time and tampering is detected on decrypt.
// The key is also the source for DeriveToken's HMAC-KDF (domain-separated).
type Cipher struct {
	aead cipher.AEAD
	key  []byte
}

// NewCipher builds a Cipher from a base64-encoded 32-byte (AES-256) key, the
// value the deployment supplies via ERUN_SECRETS_KEY. An absent or wrong-length
// key is a configuration error — there is no plaintext fallback.
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

// DeriveToken returns a deterministic, high-entropy token for a label, via an
// HMAC-SHA256 KDF over the secrets key with domain separation from encryption.
// Same (key, label) → same token, so a provisioning re-run (a durable workflow
// resuming after a crash) re-derives the SAME k3s admin token the instance
// baked, instead of a fresh one — making custody idempotent without storing the
// token in the workflow checkpoint. The token is never persisted in plaintext.
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

// Decrypt opens a nonce||ciphertext value produced by Encrypt. It fails on a
// truncated value or a failed authentication tag (tampering / wrong key).
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

// GenerateKey returns a fresh base64-encoded 32-byte key, for provisioning a
// deployment's ERUN_SECRETS_KEY.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
