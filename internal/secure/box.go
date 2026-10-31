// Package secure encrypts small secrets (OAuth tokens) at rest with
// AES-256-GCM, keyed from the APP_SECRET setting.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Box seals and opens values with one key.
type Box struct{ aead cipher.AEAD }

// MinSecretLen is the shortest APP_SECRET accepted.
const MinSecretLen = 32

// NewBox derives a 256-bit key from secret. Use a long random value, e.g.
// `openssl rand -base64 32`.
func NewBox(secret string) (*Box, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("APP_SECRET must be at least %d characters (try: openssl rand -base64 32)", MinSecretLen)
	}
	key := sha256.Sum256([]byte("exec-assistant/v1:" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; the random nonce is prepended to the output.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed value too short")
	}
	out, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, errors.New("cannot decrypt (was APP_SECRET changed?)")
	}
	return out, nil
}
