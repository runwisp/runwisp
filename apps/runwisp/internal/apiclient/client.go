// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client communicates with the RunWisp daemon HTTP API.
// It is used by the TUI and CLI commands to interact with a running daemon.
type Client struct {
	baseURL    string
	password   string
	token      string
	httpClient *http.Client
}

func New(baseURL, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Authenticate performs CHAP authentication and stores the JWT token.
func (c *Client) Authenticate() error {
	// Step 1: Get challenge nonce.
	var challenge struct {
		Nonce string `json:"nonce"`
	}
	if err := c.doJSON("GET", "/api/auth/challenge", nil, &challenge); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited) {
			return err
		}
		return fmt.Errorf("auth challenge: %w", err)
	}

	// Step 2: Compute HMAC response.
	hash := sha256.Sum256([]byte(c.password + ":" + challenge.Nonce))
	response := hex.EncodeToString(hash[:])

	// Step 3: Send response.
	body := map[string]string{
		"nonce":    challenge.Nonce,
		"response": response,
	}
	var authResult struct {
		Token string `json:"token"`
	}
	if err := c.doJSON("POST", "/api/auth", body, &authResult); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited) {
			return err
		}
		return fmt.Errorf("auth failed: %w", err)
	}

	c.token = authResult.Token
	return nil
}

func (c *Client) IsAuthenticated() bool {
	return c.token != ""
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
