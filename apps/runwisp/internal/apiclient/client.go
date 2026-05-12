// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/datadir"
	srp "mz.attahri.com/code/srp/v3"
)

// Client communicates with the RunWisp daemon HTTP API.
// It is used by the TUI and CLI commands to interact with a running daemon.
type Client struct {
	baseURL    string
	password   string
	token      string
	httpClient *http.Client
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithBearerToken seeds the client with a pre-issued JWT, skipping the SRP
// handshake. Used by the local-JWT CLI shortcut where the data-dir-readable
// signing secret is enough to mint a session directly.
func WithBearerToken(token string) Option {
	return func(c *Client) { c.token = token }
}

// New constructs a Client bound to baseURL. password is held for SRP
// authentication via AuthenticateSRP; pass "" for callers that authenticate
// via WithBearerToken instead.
func New(baseURL, password string, opts ...Option) *Client {
	c := &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AuthenticateSRP performs the two-step SRP handshake against the daemon and
// stores the resulting JWT. Replaces the old CHAP Authenticate.
func (c *Client) AuthenticateSRP() error {
	var startResp struct {
		SessionID string `json:"sessionID"`
		Salt      string `json:"salt"`
		B         string `json:"B"`
	}
	if err := c.doJSON("POST", "/api/auth/srp/start", struct{}{}, &startResp); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited) {
			return err
		}
		return fmt.Errorf("auth srp start: %w", err)
	}

	salt, err := hex.DecodeString(startResp.Salt)
	if err != nil {
		return fmt.Errorf("decode server salt: %w", err)
	}
	B, err := hex.DecodeString(startResp.B)
	if err != nil {
		return fmt.Errorf("decode server B: %w", err)
	}

	srpClient, err := srp.NewClient(datadir.SRPParams(), datadir.SRPIdentity, c.password, salt)
	if err != nil {
		return fmt.Errorf("init SRP client: %w", err)
	}
	if err := srpClient.SetB(B); err != nil {
		return fmt.Errorf("set B: %w", err)
	}
	M1, err := srpClient.ComputeM1()
	if err != nil {
		return fmt.Errorf("compute M1: %w", err)
	}

	var finishResp struct {
		M2    string `json:"M2"`
		Token string `json:"token"`
	}
	if err := c.doJSON("POST", "/api/auth/srp/finish",
		map[string]string{
			"sessionID": startResp.SessionID,
			"A":         hex.EncodeToString(srpClient.A()),
			"M1":        hex.EncodeToString(M1),
		},
		&finishResp); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited) {
			return err
		}
		return fmt.Errorf("auth srp finish: %w", err)
	}

	M2, err := hex.DecodeString(finishResp.M2)
	if err != nil {
		return fmt.Errorf("decode server M2: %w", err)
	}
	ok, err := srpClient.CheckM2(M2)
	if err != nil || !ok {
		return errors.New("server proof verification failed — daemon impersonation suspected")
	}

	c.token = finishResp.Token
	return nil
}

// Authenticate is an alias for AuthenticateSRP kept while callers transition
// to the local-JWT shortcut path.
func (c *Client) Authenticate() error { return c.AuthenticateSRP() }

func (c *Client) IsAuthenticated() bool {
	return c.token != ""
}

// Token returns the JWT currently held by the client (empty if unauthenticated).
// Used by the auth-token subcommand to print the token for downstream tools.
func (c *Client) Token() string {
	return c.token
}

// BaseURL returns the base URL the client connects to.
func (c *Client) BaseURL() string {
	return c.baseURL
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

	resp, err := c.doRequest(method, path, bodyReader, reqBody != nil)
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

// doRaw performs a request and returns the raw response. Caller must close the body.
func (c *Client) doRaw(method, path string) (*http.Response, error) {
	return c.doRequest(method, path, nil, false)
}

// doRequest is the shared transport helper for all HTTP calls.
// It sets auth headers, executes the request, and checks for errors.
func (c *Client) doRequest(method, path string, body io.Reader, isJSON bool) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if isJSON {
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
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	return resp, nil
}

// CreateLaunchTicket requests a single-use launch ticket from the daemon.
// The ticket can be redeemed via GET /api/auth/launch?ticket=<ticket>.
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
