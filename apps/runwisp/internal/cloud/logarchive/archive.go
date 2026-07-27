// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logarchive uploads a single gzipped log file to a signed PUT URL
// supplied by the control plane in the dispatch payload. It is intentionally
// stateless: callers track which executions still need archiving.
package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// MaxAttempts caps the retries on transient PUT failures.
const MaxAttempts = 3

// blockedMetadataIPs are cloud-metadata service addresses that are not caught
// by the private/link-local checks and must never be reachable from a
// peer-supplied upload URL.
var blockedMetadataIPs = map[string]struct{}{
	"169.254.169.254": {},
	"100.100.100.200": {},
	"192.0.0.192":     {},
}

// Archive gzips logFilePath and PUTs it to uploadURL. Returns the gzipped
// byte count on success. Retries up to MaxAttempts on network errors and
// 5xx responses with exponential backoff.
func Archive(ctx context.Context, client *http.Client, uploadURL, logFilePath string) (int64, error) {
	if client == nil {
		client = SafeClient()
	}
	if uploadURL == "" {
		return 0, errors.New("logarchive: empty upload URL")
	}
	// The URL originates from the (untrusted) control-plane peer. Reject
	// non-https and internal targets before touching the disk so the daemon
	// cannot be used as an SSRF egress toward internal services.
	if err := validateUploadURL(uploadURL); err != nil {
		return 0, &PermanentError{StatusCode: 0, Status: err.Error()}
	}
	return archive(ctx, client, uploadURL, logFilePath)
}

// archive performs the gzip + retrying PUT without URL validation. Split out of
// Archive so transport tests can exercise the mechanics against a loopback test
// server, which validateUploadURL rejects.
func archive(ctx context.Context, client *http.Client, uploadURL, logFilePath string) (int64, error) {
	body, size, err := gzipFile(logFilePath)
	if err != nil {
		return 0, err
	}

	var lastErr error
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		err := putOnce(ctx, client, uploadURL, body)
		if err == nil {
			return size, nil
		}
		lastErr = err
		var perm *PermanentError
		if errors.As(err, &perm) {
			return 0, err
		}
		if attempt < MaxAttempts {
			backoff := time.Duration(1<<attempt) * time.Second
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return 0, fmt.Errorf("logarchive: upload failed after %d attempts: %w", MaxAttempts, lastErr)
}

func gzipFile(path string) ([]byte, int64, error) {
	src, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("logarchive: open log file: %w", err)
	}
	defer src.Close()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := io.Copy(gz, src); err != nil {
		return nil, 0, fmt.Errorf("logarchive: gzip log file: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, 0, fmt.Errorf("logarchive: close gzip writer: %w", err)
	}
	return buf.Bytes(), int64(buf.Len()), nil
}

func putOnce(ctx context.Context, client *http.Client, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build PUT request: %w", err)
	}
	// The body is gzip-compressed, but advertise it as gzip-encoded text rather
	// than an application/gzip blob: S3/MinIO stores and replays these headers,
	// so a browser fetching the signed GET URL transparently decompresses the
	// archive and renders the log as plain text. (The presigned PUT signs only
	// host — Content-Type/Content-Encoding are unsigned, so setting them here
	// does not break the signature.)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	req.ContentLength = int64(len(body))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return &PermanentError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	return fmt.Errorf("upload failed: %s", resp.Status)
}

// PermanentError signals a non-retryable PUT response (4xx).
type PermanentError struct {
	StatusCode int
	Status     string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("logarchive: permanent failure: %s", e.Status)
}

// validateUploadURL rejects any upload target that isn't an https URL with a
// host. Scheme/host are checked offline here; the resolved IPs (including
// IP-literal hosts and DNS-rebind targets) are rejected at connect time by
// SafeClient's dialer, so the daemon never PUTs to an internal address.
func validateUploadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("logarchive: invalid upload URL: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("logarchive: upload URL must use https, got %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return errors.New("logarchive: upload URL has no host")
	}
	return nil
}

// rejectInternalIP fails for loopback, private, link-local, unspecified, and
// known cloud-metadata addresses.
func rejectInternalIP(ip net.IP) error {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("logarchive: upload URL resolves to internal address %s", ip)
	}
	if _, blocked := blockedMetadataIPs[ip.String()]; blocked {
		return fmt.Errorf("logarchive: upload URL resolves to cloud-metadata address %s", ip)
	}
	return nil
}

// SafeClient returns the hardened http.Client used for peer-supplied uploads:
// it refuses to follow redirects (a signed-S3 URL that 302s to an internal
// host is a red flag) and re-validates every resolved IP at connect time to
// defeat DNS rebinding.
func SafeClient() *http.Client {
	base := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupHost(ctx, host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("logarchive: no addresses for host %q", host)
				}
				for _, ipStr := range ips {
					ip := net.ParseIP(ipStr)
					if ip == nil {
						return nil, fmt.Errorf("logarchive: unparseable address %q", ipStr)
					}
					if err := rejectInternalIP(ip); err != nil {
						return nil, err
					}
				}
				return base.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
			},
		},
	}
}
