// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package secretcipher provides AES-256-GCM encryption of small secret values
// stored in the daemon's SQLite key/value config table. Format is
// "enc:v1:<base64(nonce ‖ ciphertext ‖ tag)>" so encrypted rows are visually
// distinguishable from plaintext ones at a glance.
package secretcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Prefix marks a value as encrypted with this scheme.
const Prefix = "enc:v1:"

// KeySize is the required raw key length: AES-256 expects 32 bytes.
const KeySize = 32

// DataKeyEnv is the environment variable from which the operator supplies the
// at-rest key. Value is base64 (std encoding) of 32 random bytes.
const DataKeyEnv = "RUNWISP_DATA_KEY"

// Cipher encrypts and decrypts small secret values with AES-256-GCM.
// The zero value is unusable; construct one with New.
type Cipher struct {
	aead cipher.AEAD
}

// New constructs a Cipher from a raw 32-byte key.
func New(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretcipher: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretcipher: new aes block: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretcipher: new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// FromEnv constructs a Cipher from the RUNWISP_DATA_KEY env var. Returns
// (nil, nil) when the variable is unset — the daemon then runs in
// plaintext-default mode.
func FromEnv() (*Cipher, error) {
	v := strings.TrimSpace(os.Getenv(DataKeyEnv))
	if v == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid base64: %w", DataKeyEnv, err)
	}
	c, err := New(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", DataKeyEnv, err)
	}
	return c, nil
}

// IsEncrypted reports whether s carries the encryption prefix. This is a
// purely lexical check; the value may still fail to decrypt.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, Prefix)
}

// Encrypt produces the wire format "enc:v1:<base64(nonce ‖ ciphertext ‖ tag)>".
// A fresh 12-byte nonce is generated for every call.
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	if c == nil {
		return "", errors.New("secretcipher: nil cipher")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretcipher: read nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, plaintext, nil)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return Prefix + base64.StdEncoding.EncodeToString(payload), nil
}

// Decrypt reverses Encrypt. Returns an error if the value lacks the prefix,
// is malformed, or fails AEAD authentication.
func (c *Cipher) Decrypt(s string) ([]byte, error) {
	if c == nil {
		return nil, errors.New("secretcipher: nil cipher")
	}
	if !IsEncrypted(s) {
		return nil, errors.New("secretcipher: value is not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, Prefix))
	if err != nil {
		return nil, fmt.Errorf("secretcipher: invalid base64: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize+c.aead.Overhead() {
		return nil, errors.New("secretcipher: payload too short")
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("secretcipher: decryption failed (wrong %s?): %w", DataKeyEnv, err)
	}
	return plaintext, nil
}
