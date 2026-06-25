package secrets

import (
	"bytes"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	plaintext := []byte("spike-admin-token-12345")
	sealed, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	out, err := c.Decrypt(sealed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(out, plaintext) {
		t.Fatalf("round trip = %q, want %q", out, plaintext)
	}
}

func TestCipherNonDeterministic(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewCipher(key)
	a, _ := c.Encrypt([]byte("same"))
	b, _ := c.Encrypt([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("same plaintext must seal to different ciphertexts (random nonce)")
	}
}

func TestCipherRejectsTamperAndBadKey(t *testing.T) {
	key, _ := GenerateKey()
	c, _ := NewCipher(key)
	sealed, _ := c.Encrypt([]byte("secret"))
	sealed[len(sealed)-1] ^= 0xff
	if _, err := c.Decrypt(sealed); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}

	other, _ := GenerateKey()
	c2, _ := NewCipher(other)
	good, _ := c.Encrypt([]byte("secret"))
	if _, err := c2.Decrypt(good); err == nil {
		t.Fatal("decrypt with a different key must fail")
	}
}

func TestNewCipherRejectsBadKeys(t *testing.T) {
	for _, tc := range []string{"", "not-base64!!", "c2hvcnQ="} { // empty, invalid, too short
		if _, err := NewCipher(tc); err == nil {
			t.Fatalf("expected error for key %q", tc)
		}
	}
}
