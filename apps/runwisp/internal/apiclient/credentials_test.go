// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLocalCredentials_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/local/credentials", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"password":  "Kj2x9pQ7mN4vL8rT5wYz1c",
			"ephemeral": true,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	creds, err := c.GetLocalCredentials()
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "Kj2x9pQ7mN4vL8rT5wYz1c", creds.Password)
	assert.True(t, creds.Ephemeral)
}

func TestGetLocalCredentials_404MapsToUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no shareable password", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	creds, err := c.GetLocalCredentials()
	assert.Nil(t, creds)
	assert.ErrorIs(t, err, ErrLocalCredentialsUnavailable)
}

func TestGetLocalCredentials_403PropagatesAsHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not local", http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	creds, err := c.GetLocalCredentials()
	assert.Nil(t, creds)
	require.Error(t, err)
	assert.True(t, IsHTTPStatus(err, http.StatusForbidden),
		"403 must surface as an HTTPStatusError, not ErrLocalCredentialsUnavailable")
	assert.False(t, errors.Is(err, ErrLocalCredentialsUnavailable))
}

func TestIsHTTPStatus(t *testing.T) {
	wrapped := &HTTPStatusError{StatusCode: http.StatusNotFound, Body: "nope"}
	assert.True(t, IsHTTPStatus(wrapped, http.StatusNotFound))
	assert.False(t, IsHTTPStatus(wrapped, http.StatusForbidden))
	assert.False(t, IsHTTPStatus(errors.New("plain"), http.StatusNotFound))
}
