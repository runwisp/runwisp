// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package secretcipher

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustNew(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	c, err := New(key)
	require.NoError(t, err)
	return c
}

func TestNew_RejectsBadKeySize(t *testing.T) {
	_, err := New(make([]byte, 16))
	assert.Error(t, err)
	_, err = New(make([]byte, 31))
	assert.Error(t, err)
	_, err = New(make([]byte, 33))
	assert.Error(t, err)
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	c := mustNew(t)

	cases := [][]byte{
		[]byte("hello"),
		[]byte(""),
		[]byte(strings.Repeat("a", 1024)),
		{0x00, 0x01, 0x02, 0xFF},
	}
	for _, pt := range cases {
		enc, err := c.Encrypt(pt)
		require.NoError(t, err)
		assert.True(t, IsEncrypted(enc), "expected prefix: %q", enc)

		got, err := c.Decrypt(enc)
		require.NoError(t, err)
		// Empty plaintext round-trips as a zero-length slice — accept either
		// representation (nil/empty) since []byte("") and []byte(nil) compare
		// unequal but are semantically the same payload.
		if len(pt) == 0 {
			assert.Empty(t, got)
		} else {
			assert.Equal(t, pt, got)
		}
	}
}

func TestDecrypt_WrongKey_AuthFails(t *testing.T) {
	a := mustNew(t)
	b := mustNew(t)

	enc, err := a.Encrypt([]byte("secret"))
	require.NoError(t, err)

	_, err = b.Decrypt(enc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decryption failed")
}

func TestIsEncrypted(t *testing.T) {
	assert.True(t, IsEncrypted("enc:v1:abc"))
	assert.False(t, IsEncrypted(""))
	assert.False(t, IsEncrypted("plaintext"))
	assert.False(t, IsEncrypted("enc:v0:abc"))
}

func TestDecrypt_RejectsNonEncryptedValue(t *testing.T) {
	c := mustNew(t)
	_, err := c.Decrypt("plaintext-no-prefix")
	assert.Error(t, err)
}

func TestDecrypt_RejectsMalformedBase64(t *testing.T) {
	c := mustNew(t)
	_, err := c.Decrypt(Prefix + "!!!not-base64!!!")
	assert.Error(t, err)
}

func TestDecrypt_RejectsTruncatedPayload(t *testing.T) {
	c := mustNew(t)
	// 4 bytes of base64 = 3 bytes payload — shorter than nonce + tag.
	short := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	_, err := c.Decrypt(Prefix + short)
	assert.Error(t, err)
}

func TestEncrypt_NonceUnique(t *testing.T) {
	c := mustNew(t)
	a, err := c.Encrypt([]byte("same-plaintext"))
	require.NoError(t, err)
	b, err := c.Encrypt([]byte("same-plaintext"))
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "two encryptions of the same plaintext must differ")
}

func TestFromEnv_UnsetReturnsNil(t *testing.T) {
	t.Setenv(DataKeyEnv, "")
	c, err := FromEnv()
	require.NoError(t, err)
	assert.Nil(t, c)
}

func TestFromEnv_ValidKey(t *testing.T) {
	key := make([]byte, KeySize)
	_, err := rand.Read(key)
	require.NoError(t, err)
	t.Setenv(DataKeyEnv, base64.StdEncoding.EncodeToString(key))

	c, err := FromEnv()
	require.NoError(t, err)
	require.NotNil(t, c)

	enc, err := c.Encrypt([]byte("hi"))
	require.NoError(t, err)
	got, err := c.Decrypt(enc)
	require.NoError(t, err)
	assert.Equal(t, []byte("hi"), got)
}

func TestFromEnv_InvalidBase64(t *testing.T) {
	t.Setenv(DataKeyEnv, "!!!not-base64!!!")
	_, err := FromEnv()
	assert.Error(t, err)
}

func TestFromEnv_WrongKeyLength(t *testing.T) {
	t.Setenv(DataKeyEnv, base64.StdEncoding.EncodeToString([]byte("too-short")))
	_, err := FromEnv()
	assert.Error(t, err)
}
