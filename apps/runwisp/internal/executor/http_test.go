// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
		// Cloud metadata endpoints — now covered by netguard's CGNAT / IETF
		// special-use prefixes (100.64.0.0/10, 192.0.0.0/24).
		{"Alibaba metadata", "metadata", "100.100.100.200", "blocked"},
		{"Oracle Cloud metadata", "metadata", "192.0.0.192", "blocked"},
		// Additional special-use ranges netguard now rejects.
		{"CGNAT shared", "cgnat", "100.64.0.1", "blocked"},
		{"TEST-NET-1 doc", "doc", "192.0.2.1", "blocked"},
		{"benchmarking", "bench", "198.18.0.1", "blocked"},
		{"multicast", "mcast", "224.0.0.1", "blocked"},
		{"reserved future", "reserved", "240.0.0.1", "blocked"},
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
	_, err := b.Start(context.Background(), nil, nil, &model.ShellExecution{Script: "echo hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-http execution")
}

func TestStart_EmptyURL(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, nil, &model.HTTPExecution{Method: "GET", URL: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP URL is required")
}

func TestStart_UnsupportedScheme(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, nil, &model.HTTPExecution{Method: "GET", URL: "ftp://example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported URL scheme")
}

func TestStart_BlockedPrivateURL(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, nil, &model.HTTPExecution{Method: "GET", URL: "http://127.0.0.1/admin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestStart_BlockedMetadataURL(t *testing.T) {
	b := &HTTPBackend{}
	_, err := b.Start(context.Background(), nil, nil, &model.HTTPExecution{Method: "GET", URL: "http://169.254.169.254/latest"})
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

// --- ssrfSafeDialer ---
//
// The dialer's contract is: split addr, resolve host, reject if any resolved
// IP is private, dial the first allowed IP. We exercise the four observable
// paths: malformed addr, DNS failure, private-IP rejection, and a public IP
// happy path (via 127.0.0.1 reverse — a loopback IP is rejected so the dial
// path is not entered, but the private-IP rejection IS).

func TestSsrfSafeDialer_RejectsMalformedAddr(t *testing.T) {
	d := ssrfSafeDialer()
	_, err := d(context.Background(), "tcp", "no-colon-no-port")
	require.Error(t, err)
}

func TestSsrfSafeDialer_RejectsPrivateIP(t *testing.T) {
	// Localhost resolves to a loopback address; rejectPrivateIP must block it.
	d := ssrfSafeDialer()
	_, err := d(context.Background(), "tcp", "127.0.0.1:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestSsrfSafeDialer_DnsFailureBubblesUp(t *testing.T) {
	d := ssrfSafeDialer()
	// `.invalid` is reserved by RFC 2606 to always fail resolution.
	_, err := d(context.Background(), "tcp", "nonexistent.invalid:80")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve host")
}

func TestHTTPBackend_Available(t *testing.T) {
	b := &HTTPBackend{}
	assert.True(t, b.Available(context.Background()))
}

// TestStartProcess_SuccessRunsExecuteAndReturnsExitCode exercises the goroutine
// + Process bookkeeping path that Start sets up after validateHTTPURL passes.
// validateHTTPURL rejects loopback, so we drive startProcess directly to keep
// httptest's 127.0.0.1 listener usable.
func TestStartProcess_SuccessRunsExecuteAndReturnsExitCode(t *testing.T) {
	srvURL, srvClient := newLocalTestServer(t, 200, "hello")
	b := &HTTPBackend{Client: srvClient}
	proc := b.startProcess(context.Background(), &model.HTTPExecution{
		Method: "GET",
		URL:    srvURL,
	})
	require.NotNil(t, proc)
	require.NotNil(t, proc.Stdout)

	body, err := io.ReadAll(proc.Stdout)
	require.NoError(t, err)
	assert.Contains(t, string(body), "hello")

	code, err := proc.Wait()
	require.NoError(t, err)
	assert.Equal(t, 0, code)
}

// TestStartProcess_PropagatesNonZeroExitFromExecute confirms the Wait callback
// surfaces the exit code execute computed (here, 1 from a 5xx response).
func TestStartProcess_PropagatesNonZeroExitFromExecute(t *testing.T) {
	srvURL, srvClient := newLocalTestServer(t, 500, "boom")
	b := &HTTPBackend{Client: srvClient}
	proc := b.startProcess(context.Background(), &model.HTTPExecution{Method: "GET", URL: srvURL})
	_, _ = io.ReadAll(proc.Stdout)
	code, err := proc.Wait()
	require.NoError(t, err)
	assert.Equal(t, 1, code)
}

// TestExecute_NewRequestErrorReturnsOne triggers the http.NewRequestWithContext
// error path (e.g. an invalid HTTP method).
func TestExecute_NewRequestErrorReturnsOne(t *testing.T) {
	srvURL, srvClient := newLocalTestServer(t, 200, "ok")
	b := &HTTPBackend{Client: srvClient}
	var buf bytes.Buffer
	code := b.execute(context.Background(), &model.HTTPExecution{
		Method: "GET\x00with-bad-byte",
		URL:    srvURL,
	}, &buf)
	assert.Equal(t, 1, code)
	assert.Contains(t, buf.String(), "Failed to create request")
}

// TestHTTPClientCheckRedirect_BlocksLoopback verifies the redirect re-validator
// rejects a redirect destination that resolves to a private/loopback address.
func TestHTTPClientCheckRedirect_BlocksLoopback(t *testing.T) {
	b := &HTTPBackend{}
	client := b.httpClient()
	require.NotNil(t, client.CheckRedirect)
	// Build a minimal Request with a private-IP URL to trigger validateHTTPURL.
	req, err := http.NewRequest("GET", "http://127.0.0.1/", nil)
	require.NoError(t, err)
	err = client.CheckRedirect(req, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

// TestStart_HappyPathReturnsProcess covers Start's terminal `return
// b.startProcess(...), nil` branch — the only happy path through validateHTTPURL.
// Uses an IP literal as the host (net.LookupHost short-circuits without DNS)
// and a stub RoundTripper so the goroutine spawned by startProcess completes
// without touching the network.
func TestStart_HappyPathReturnsProcess(t *testing.T) {
	stub := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString("ok")),
			Header:     make(http.Header),
		}, nil
	})}
	b := &HTTPBackend{Client: stub}
	// 8.8.8.8 is public, so validateHTTPURL's rejectPrivateIP gate passes.
	proc, err := b.Start(context.Background(), nil, nil, &model.HTTPExecution{Method: "GET", URL: "http://8.8.8.8/"})
	require.NoError(t, err)
	require.NotNil(t, proc)
	_, _ = io.ReadAll(proc.Stdout)
	code, waitErr := proc.Wait()
	require.NoError(t, waitErr)
	assert.Equal(t, 0, code)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestHTTPClientCheckRedirect_StopsAfter10 covers the "stopped after 10
// redirects" branch.
func TestHTTPClientCheckRedirect_StopsAfter10(t *testing.T) {
	b := &HTTPBackend{}
	client := b.httpClient()
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = &http.Request{}
	}
	req, err := http.NewRequest("GET", "http://example.com/", nil)
	require.NoError(t, err)
	err = client.CheckRedirect(req, via)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped after 10 redirects")
}
