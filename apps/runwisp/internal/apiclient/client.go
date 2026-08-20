// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/chap"
)

// Client communicates with the RunWisp daemon HTTP API.
// It is used by the TUI and CLI commands to interact with a running daemon.
//
// Construct it with New for remote (TCP) daemons, or NewUnix to talk to a
// local daemon over its Unix socket. Unix-socket clients skip CHAP/JWT
// entirely: the daemon trusts the connection based on filesystem
// permissions plus a PEERCRED check at accept time.
type Client struct {
	baseURL      string
	password     string
	token        string
	httpClient   *http.Client
	streamClient *http.Client
	local        bool
}

// NormalizeBaseURL trims a trailing slash so a base URL maps to a single
// canonical form. New applies it to every Client; callers that key state by
// daemon URL (e.g. the CLI's session-token cache) use it to line their key up
// with the URL the client actually talks to.
func NormalizeBaseURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/")
}

// New constructs a Client for a remote daemon. baseURL must be an http(s)
// URL; the client will run CHAP via Authenticate to obtain a JWT. It performs
// no certificate pinning — over HTTPS it relies on the system trust store, so
// it suits tests (plain-HTTP httptest servers) and callers behind a CA-signed
// reverse proxy. The user-facing `run --url` path uses NewPinned instead.
func New(baseURL, password string) *Client {
	return &Client{
		baseURL:  NormalizeBaseURL(baseURL),
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		streamClient: &http.Client{},
	}
}

// NewPinned is New plus trust-on-first-use certificate pinning: the daemon's
// self-signed cert is recorded in pins on first connect and any later
// fingerprint change fails the handshake (see pinningTransport). pins must be
// non-nil; for plain HTTP the pinning transport is inert (no TLS handshake to
// verify), so it's safe to use unconditionally for remote URLs.
func NewPinned(baseURL, password string, pins CertPinStore) *Client {
	c := New(baseURL, password)
	tr := pinningTransport(c.baseURL, pins)
	c.httpClient.Transport = tr
	c.streamClient.Transport = tr
	return c
}

// NewProbe constructs a short-timeout, password-less Client for one-shot local
// identity probes (GET /api/daemon/identity). The brief timeout keeps a launcher that
// hit a port conflict from stalling when the port-holder is slow or not even an
// HTTP server. It carries no password — /api/daemon/identity is public but local-gated.
func NewProbe(baseURL string) *Client {
	return &Client{
		baseURL: NormalizeBaseURL(baseURL),
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
		streamClient: &http.Client{},
	}
}

// NewUnix constructs a Client that talks to the daemon over a Unix socket.
// The local CLI/TUI use this path: the socket already gates access via 0700
// datadir + 0600 socket-file perms + PEERCRED, so no password is required
// and Authenticate is a no-op (IsAuthenticated returns true immediately).
func NewUnix(socketPath string) *Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	// "http://runwisp" is a placeholder host; the dialer ignores the address
	// and connects to socketPath. Using a literal hostname keeps the URL
	// well-formed for the standard library's URL parser.
	return &Client{
		baseURL: "http://runwisp",
		local:   true,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
		streamClient: &http.Client{Transport: transport},
	}
}

// Authenticate performs CHAP authentication and stores the JWT token.
// On Unix-socket clients this is a no-op — the daemon does not require a
// JWT for socket-delivered requests.
func (c *Client) Authenticate() error {
	if c.local {
		return nil
	}

	var challenge struct {
		Nonce string `json:"nonce"`
	}
	if err := c.doJSON("GET", "/api/auth/challenge", nil, &challenge); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited) {
			return err
		}
		return fmt.Errorf("auth challenge: %w", err)
	}

	body := map[string]string{
		"nonce":    challenge.Nonce,
		"response": chap.Response(c.password, challenge.Nonce),
	}
	var authResult struct {
		Token string `json:"token"`
	}
	if err := c.doJSON("POST", "/api/auth/login", body, &authResult); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited) {
			return err
		}
		return fmt.Errorf("auth failed: %w", err)
	}

	c.token = authResult.Token
	return nil
}

// IsAuthenticated reports whether the client is ready to make protected
// API calls. Unix-socket clients are always authenticated.
func (c *Client) IsAuthenticated() bool {
	return c.local || c.token != ""
}

// SetToken seeds the client with a previously obtained JWT, letting callers
// reuse a cached session instead of re-running CHAP on every invocation.
func (c *Client) SetToken(token string) {
	c.token = token
}

// Token returns the JWT the client is currently using (empty on Unix-socket
// clients, which never hold one). Callers persist it to reuse the session.
func (c *Client) Token() string {
	return c.token
}

// doJSON performs a JSON request. reqBody is marshalled if non-nil.
// respBody is unmarshalled from the response if non-nil.
func (c *Client) doJSON(method, path string, reqBody, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	resp, err := c.doRequest(method, path, bodyReader)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if respBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// doRaw performs a GET request and returns the raw response. Caller must close the body.
func (c *Client) doRaw(path string) (*http.Response, error) {
	return c.doRequest(http.MethodGet, path, nil)
}

// doRequest is the shared transport helper for all HTTP calls.
func (c *Client) doRequest(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrUnauthorized
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		return nil, ErrRateLimited
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return nil, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(errBody)),
		}
	}

	return resp, nil
}

// HTTPStatusError carries the daemon's response status and body for non-2xx
// responses. Callers can recover the code via errors.As to make decisions
// based on the daemon's intent (e.g. 404 → "ephemeral password unavailable").
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// IsHTTPStatus reports whether err is (or wraps) an HTTPStatusError with the
// given code. It is a convenience for status-code dispatch in callers that
// otherwise would have to call errors.As at every site.
func IsHTTPStatus(err error, code int) bool {
	var se *HTTPStatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.StatusCode == code
}

// CreateLaunchTicket requests a single-use launch ticket from the daemon.
// The ticket can be redeemed via GET /api/auth/launch-ticket?ticket=<ticket>.
func (c *Client) CreateLaunchTicket() (string, error) {
	var resp struct {
		Ticket string `json:"ticket"`
	}
	if err := c.doJSON("POST", "/api/auth/launch-ticket", nil, &resp); err != nil {
		return "", err
	}
	return resp.Ticket, nil
}

// ErrUnauthorized is returned when the API returns 401.
var ErrUnauthorized = errors.New("unauthorized")

// ErrRateLimited is returned when the API returns 429. Callers should surface
// a retry-after hint to the user; the daemon's auth endpoints share a single
// per-IP bucket, so hitting this usually means too many failed login attempts.
var ErrRateLimited = errors.New("rate limited")
