// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- validateHTTPURL ---

func TestValidateHTTPURL_InvalidParse(t *testing.T) {
	// A null byte makes url.Parse return an error.
	err := validateHTTPURL("http://example\x00.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid URL")
}

func TestValidateHTTPURL_UnsupportedScheme(t *testing.T) {
	err := validateHTTPURL("ftp://example.com/file.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported URL scheme")
}

func TestValidateHTTPURL_EmptyHostname(t *testing.T) {
	// "http:///path" has an empty host component.
	err := validateHTTPURL("http:///path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL must have a hostname")
}

func TestValidateHTTPURL_BlockedMetadataHost(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"IPv4 link-local metadata", "http://169.254.169.254/latest/meta-data/"},
		{"GCP metadata", "https://metadata.google.internal/computeMetadata/v1/"},
		{"metadata host case-insensitive", "http://METADATA.GOOGLE.INTERNAL/"},
		// net.LookupHost("127.0.0.1") returns ["127.0.0.1"]; rejectPrivateIP then
		// rejects it — exercises the DNS-resolve code path via a numeric IP.
		{"loopback IP resolves and blocks", "http://127.0.0.1/test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateHTTPURL(tt.url)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "blocked")
		})
	}
}

// --- rejectPrivateIP ---

func TestRejectPrivateIP(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		ip      string
		wantErr string // substring; "" means no error
	}{
		{"invalid IP", "example.com", "not-an-ip", "failed to parse IP"},
		{"loopback IPv4", "localhost", "127.0.0.1", "blocked"},
		{"loopback IPv6", "localhost", "::1", "blocked"},
		{"RFC1918 192.168", "internal", "192.168.1.1", "blocked"},
		{"RFC1918 10.0", "internal", "10.0.0.1", "blocked"},
		{"RFC1918 172.16", "internal", "172.16.0.1", "blocked"},
		{"link-local IPv4", "169.254.1.1", "169.254.1.1", "blocked"},
		{"link-local IPv6", "fe80::1", "fe80::1", "blocked"},
		{"unspecified", "any", "0.0.0.0", "blocked"},
		// ::ffff:127.0.0.1 normalizes to 127.0.0.1 via To4(); must be blocked.
		{"IPv6-mapped loopback", "localhost", "::ffff:127.0.0.1", "blocked"},
		{"public IPv4", "dns.google", "8.8.8.8", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectPrivateIP(tt.host, tt.ip)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// --- buildBody ---

func TestBuildBody_EmptyBodies(t *testing.T) {
	tests := []struct {
		name string
		body *model.HTTPBody
	}{
		{"nil body", nil},
		{"kind none", &model.HTTPBody{Kind: "none"}},
		{"kind empty", &model.HTTPBody{Kind: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &HTTPBackend{}
			r, ct, err := b.buildBody(tt.body)
			require.NoError(t, err)
			assert.Nil(t, r)
			assert.Empty(t, ct)
		})
	}
}

func TestBuildBody_KindJSON(t *testing.T) {
	b := &HTTPBackend{}
	payload := `{"key":"val"}`
	r, ct, err := b.buildBody(&model.HTTPBody{Kind: "json", JSON: payload})
	require.NoError(t, err)
	assert.Equal(t, "application/json", ct)
	require.NotNil(t, r)
	data, readErr := io.ReadAll(r)
	require.NoError(t, readErr)
	assert.Equal(t, payload, string(data))
}

func TestBuildBody_KindFormData(t *testing.T) {
	b := &HTTPBackend{}
	r, ct, err := b.buildBody(&model.HTTPBody{
		Kind: "formData",
		Fields: []model.KeyValue{
			{Key: "a", Value: "1"},
			{Key: "b", Value: "2"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "application/x-www-form-urlencoded", ct)
	require.NotNil(t, r)
	data, readErr := io.ReadAll(r)
	require.NoError(t, readErr)
	body := string(data)
	assert.Contains(t, body, "a=1")
	assert.Contains(t, body, "b=2")
}

func TestBuildBody_KindUnknown(t *testing.T) {
	b := &HTTPBackend{}
	r, ct, err := b.buildBody(&model.HTTPBody{Kind: "xml"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported body kind")
	assert.Nil(t, r)
	assert.Empty(t, ct)
}

// --- httpClient ---

func TestHTTPClient(t *testing.T) {
	t.Run("returns existing client", func(t *testing.T) {
		existing := &http.Client{Timeout: 1}
		got := (&HTTPBackend{Client: existing}).httpClient()
		assert.Same(t, existing, got)
	})
	t.Run("creates new client when nil", func(t *testing.T) {
		got := (&HTTPBackend{}).httpClient()
		require.NotNil(t, got)
	})
}

// --- HTTPBackend.Start (early-return error paths) ---

func TestStart_NonHTTPExecutionDef(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, &model.ShellExecution{Script: "echo hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-http execution")
}

func TestStart_EmptyURL(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, &model.HTTPExecution{Method: "GET", URL: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP URL is required")
}

func TestStart_UnsupportedScheme(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, &model.HTTPExecution{Method: "GET", URL: "ftp://example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported URL scheme")
}

func TestStart_BlockedPrivateURL(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, &model.HTTPExecution{Method: "GET", URL: "http://127.0.0.1/admin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestStart_BlockedMetadataURL(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, &model.HTTPExecution{Method: "GET", URL: "http://169.254.169.254/latest"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

// --- HTTPBackend.execute (internal, via direct call from same package) ---
// These tests cover the actual HTTP request/response path by injecting a test
// HTTP server via the Client field, bypassing validateHTTPURL.

// newLocalTestServer creates an httptest.Server and returns its URL and a
// pre-configured client that bypasses the SSRF dialer.
func newLocalTestServer(t *testing.T, status int, body string) (url string, client *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, srv.Client()
}

func TestExecute_SuccessReturnsZero(t *testing.T) {
	srvURL, srvClient := newLocalTestServer(t, 200, "all good")
	b := &HTTPBackend{Client: srvClient}
	var buf bytes.Buffer
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method: "GET",
		URL:    srvURL,
	}, &buf)
	assert.Equal(t, 0, code)
	assert.Contains(t, buf.String(), "all good")
}

func TestExecute_4xxReturnsOne(t *testing.T) {
	srvURL, srvClient := newLocalTestServer(t, 404, "not found")
	b := &HTTPBackend{Client: srvClient}
	var buf bytes.Buffer
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method: "GET",
		URL:    srvURL,
	}, &buf)
	assert.Equal(t, 1, code)
}

func TestExecute_5xxReturnsOne(t *testing.T) {
	srvURL, srvClient := newLocalTestServer(t, 500, "server error")
	b := &HTTPBackend{Client: srvClient}
	var buf bytes.Buffer
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method: "GET",
		URL:    srvURL,
	}, &buf)
	assert.Equal(t, 1, code)
}

func TestExecute_PostWithJSONBody(t *testing.T) {
	var receivedContentType string
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	b := &HTTPBackend{Client: srv.Client()}
	var buf bytes.Buffer
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method: "POST",
		URL:    srv.URL,
		Body:   &model.HTTPBody{Kind: "json", JSON: `{"x":1}`},
	}, &buf)
	assert.Equal(t, 0, code)
	assert.Equal(t, "application/json", receivedContentType)
	assert.Equal(t, `{"x":1}`, string(receivedBody))
}

func TestExecute_WithHeaders(t *testing.T) {
	var receivedHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Test")
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	b := &HTTPBackend{Client: srv.Client()}
	var buf bytes.Buffer
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method:  "GET",
		URL:     srv.URL,
		Headers: []model.KeyValue{{Key: "X-Test", Value: "hello"}},
	}, &buf)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello", receivedHeader)
	assert.Contains(t, buf.String(), "X-Test: hello")
}

func TestExecute_InvalidBodyKindReturnsOne(t *testing.T) {
	b := &HTTPBackend{}
	var buf bytes.Buffer
	// Body with unsupported kind — buildBody returns error before any HTTP call.
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method: "POST",
		URL:    "http://irrelevant.invalid",
		Body:   &model.HTTPBody{Kind: "xml"},
	}, &buf)
	assert.Equal(t, 1, code)
	assert.Contains(t, buf.String(), "[ERROR]")
}

func TestExecute_RequestFailedReturnsOne(t *testing.T) {
	// Point to a closed server so the request fails at transport level.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srvURL := srv.URL
	cli := srv.Client()
	srv.Close() // close before the request

	b := &HTTPBackend{Client: cli}
	var buf bytes.Buffer
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method: "GET",
		URL:    srvURL,
	}, &buf)
	assert.Equal(t, 1, code)
	assert.Contains(t, buf.String(), "[ERROR]")
}

func TestHTTPBackend_Available(t *testing.T) {
	b := &HTTPBackend{}
	assert.True(t, b.Available(context.Background()))
}
