// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/netguard"
)

// blockedMetadataHosts are well-known cloud-metadata endpoints that must never
// be reachable from user-authored tasks. This is a hostname-string guard; the
// IP-literal metadata addresses (169.254.169.254, 100.100.100.200, 192.0.0.192)
// are all inside netguard's blocked prefixes and rejected at IP resolution.
var blockedMetadataHosts = []string{"metadata.google.internal", "169.254.169.254"}

// validateHTTPURL blocks requests to private/internal network addresses (SSRF protection).
func validateHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	for _, blocked := range blockedMetadataHosts {
		if strings.EqualFold(hostname, blocked) {
			return fmt.Errorf("requests to %s are blocked", hostname)
		}
	}

	// Resolve and check for private/loopback IPs
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve host %q: %w", hostname, err)
	}
	for _, ipStr := range ips {
		if err := rejectPrivateIP(hostname, ipStr); err != nil {
			return err
		}
	}

	return nil
}

// rejectPrivateIP returns an error unless ipStr is a publicly routable unicast
// address, delegating the range checks to the shared netguard validator.
func rejectPrivateIP(hostname, ipStr string) error {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return fmt.Errorf("failed to parse IP %q for host %s", ipStr, hostname)
	}
	if err := netguard.RejectNonPublicIP(ip); err != nil {
		return fmt.Errorf("requests to %s (%s) are blocked: %w", hostname, ipStr, err)
	}
	return nil
}

// ssrfSafeDialer returns a DialContext that resolves the hostname at connection
// time and validates all resolved IPs before dialing, preventing DNS rebinding attacks.
// Go's http.Transport still uses the original hostname for TLS SNI and certificate
// verification even though the TCP connection targets the pre-resolved IP.
func ssrfSafeDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	base := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		ips, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve host %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no addresses found for host %q", host)
		}

		for _, ipStr := range ips {
			if err := rejectPrivateIP(host, ipStr); err != nil {
				return nil, err
			}
		}

		// Dial the first resolved IP directly; the Transport manages TLS using
		// the original hostname for SNI and certificate verification.
		return base.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
	}
}

// HTTPBackend executes HTTP requests and streams the response as output.
type HTTPBackend struct {
	Client *http.Client
}

func (b *HTTPBackend) Available(_ context.Context) bool { return true }

// Start ignores task/run: an HTTP execution has no argv or environment to carry
// per-run params into, and this backend isn't built from [tasks.*] TOML (only
// from cloud ad-hoc defs, which declare no params), so there is nothing to wire.
func (b *HTTPBackend) Start(ctx context.Context, _ *model.Task, _ *model.Run, def model.ExecutionDef) (*Process, error) {
	httpDef, ok := def.(*model.HTTPExecution)
	if !ok {
		return nil, fmt.Errorf("HTTPBackend received non-http execution: %s", def.ExecType())
	}

	if httpDef.URL == "" {
		return nil, fmt.Errorf("HTTP URL is required")
	}

	if err := validateHTTPURL(httpDef.URL); err != nil {
		return nil, err
	}

	return b.startProcess(ctx, httpDef), nil
}

// startProcess wires up a pipe, kicks off the execute goroutine, and packages
// the result into a Process. Extracted from Start so unit tests can exercise
// the goroutine/Process bookkeeping without going through validateHTTPURL,
// which rejects loopback addresses used by httptest.
func (b *HTTPBackend) startProcess(ctx context.Context, httpDef *model.HTTPExecution) *Process {
	pr, pw := io.Pipe()
	var exitCode int

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer pw.Close()
		exitCode = b.execute(ctx, httpDef, pw)
	}()

	return &Process{
		Stdout: pr,
		Wait: func() (int, error) {
			<-done
			return exitCode, nil
		},
	}
}

func (b *HTTPBackend) execute(ctx context.Context, def *model.HTTPExecution, out io.Writer) int {
	body, contentType, err := b.buildBody(def.Body)
	if err != nil {
		fmt.Fprintf(out, "[ERROR] Failed to build request body: %v\n", err)
		return 1
	}

	req, err := http.NewRequestWithContext(ctx, def.Method, def.URL, body)
	if err != nil {
		fmt.Fprintf(out, "[ERROR] Failed to create request: %v\n", err)
		return 1
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	for _, h := range def.Headers {
		req.Header.Set(h.Key, h.Value)
	}

	fmt.Fprintf(out, "%s %s\n", def.Method, redactURL(def.URL))
	for _, h := range def.Headers {
		fmt.Fprintf(out, "> %s: %s\n", h.Key, redactHeaderValue(h.Key, h.Value))
	}
	if contentType != "" {
		fmt.Fprintf(out, "> Content-Type: %s\n", contentType)
	}
	fmt.Fprintln(out)

	client := b.httpClient()
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(out, "[ERROR] Request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	fmt.Fprintf(out, "< %s (%s)\n", resp.Status, elapsed.Round(time.Millisecond))
	for key, vals := range resp.Header {
		for _, v := range vals {
			fmt.Fprintf(out, "< %s: %s\n", key, redactHeaderValue(key, v))
		}
	}
	fmt.Fprintln(out)

	const maxResponseBody = 100 * 1024 * 1024 // 100 MB
	if _, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxResponseBody)); copyErr != nil {
		fmt.Fprintf(out, "\n[ERROR] Failed to read response body: %v\n", copyErr)
	}
	fmt.Fprintln(out)

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return 0
	}
	return 1
}

func (b *HTTPBackend) buildBody(body *model.HTTPBody) (io.Reader, string, error) {
	if body == nil || body.Kind == "none" || body.Kind == "" {
		return nil, "", nil
	}

	switch body.Kind {
	case "json":
		return strings.NewReader(body.JSON), "application/json", nil
	case "formData":
		form := url.Values{}
		for _, field := range body.Fields {
			form.Set(field.Key, field.Value)
		}
		return strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", nil
	default:
		return nil, "", fmt.Errorf("unsupported body kind: %q", body.Kind)
	}
}

// redactURL blanks any userinfo password before the request line is written to
// the run log (net/url.Redacted keeps the username but replaces the password
// with "xxxxx"). The URL was already validated by validateHTTPURL, so a parse
// error here is unreachable; returning the raw string on error would leak, so
// on the impossible error we blank the whole URL instead.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[redacted]"
	}
	return u.Redacted()
}

// redactHeaderValue hides the values of headers that commonly carry
// credentials, so request/response header dumps in the run log don't persist
// bearer tokens, cookies, or API keys to disk (viewable over API/SSE).
func redactHeaderValue(key, value string) string {
	if isSensitiveHeader(key) {
		return "[redacted]"
	}
	return value
}

func isSensitiveHeader(key string) bool {
	k := strings.ToLower(key)
	switch k {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	}
	return strings.Contains(k, "token") || strings.Contains(k, "secret") ||
		strings.Contains(k, "api-key") || strings.Contains(k, "apikey")
}

func (b *HTTPBackend) httpClient() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return &http.Client{
		Timeout:   5 * time.Minute,
		Transport: &http.Transport{DialContext: ssrfSafeDialer()},
		// Re-validate each redirect destination to prevent redirect-based SSRF.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return validateHTTPURL(req.URL.String())
		},
	}
}
