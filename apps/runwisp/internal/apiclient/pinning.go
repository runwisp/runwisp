// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

	"github.com/runwisp/runwisp/internal/tlscert"
)

// CertPinStore persists trust-on-first-use certificate pins keyed by daemon
// base URL. The CLI implements it over a per-user cache file; it is an
// interface so apiclient stays free of any filesystem or cache-dir knowledge.
type CertPinStore interface {
	// Load returns the pinned cert SHA-256 (hex) for key, or ok=false when the
	// daemon has not been seen before.
	Load(key string) (fingerprint string, ok bool)
	// Store records the pin observed on first contact with key.
	Store(key, fingerprint string)
}

// CertPinMismatchError reports that a daemon presented a TLS certificate whose
// fingerprint differs from the one pinned on first use — the known-hosts
// "REMOTE HOST IDENTIFICATION HAS CHANGED" moment. It is intentionally fatal:
// either the cert was legitimately regenerated (operator clears the pin) or the
// connection is being intercepted.
type CertPinMismatchError struct {
	Host   string
	Pinned string
	Got    string
}

func (e *CertPinMismatchError) Error() string {
	return fmt.Sprintf("TLS certificate for %s changed: pinned sha256:%s but server presented sha256:%s", e.Host, e.Pinned, e.Got)
}

// pinningTransport clones the default transport and installs a TOFU-pinning TLS
// config keyed by baseURL. InsecureSkipVerify disables the CA-chain check — a
// self-signed daemon cert has no chain — and VerifyConnection re-establishes
// trust by pinning the leaf fingerprint instead. The clone preserves the stdlib
// transport's proxy/timeout defaults; only TLS verification changes.
func pinningTransport(baseURL string, pins CertPinStore) http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	tr := base.Clone()
	tr.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // not insecure: VerifyConnection pins the leaf cert by SHA-256 (TOFU), which a CA chain can't express for a self-signed daemon cert
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyPin(pins, baseURL, cs)
		},
	}
	return tr
}

// verifyPin implements trust-on-first-use: the first cert seen for a host is
// recorded and trusted; every later connection must present the same
// fingerprint or the handshake fails with a CertPinMismatchError.
func verifyPin(pins CertPinStore, key string, cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("tls: server presented no certificate")
	}
	got := tlscert.FingerprintDER(cs.PeerCertificates[0].Raw)
	if pinned, ok := pins.Load(key); ok {
		if subtle.ConstantTimeCompare([]byte(pinned), []byte(got)) != 1 {
			return &CertPinMismatchError{Host: key, Pinned: pinned, Got: got}
		}
		return nil
	}
	pins.Store(key, got)
	return nil
}
