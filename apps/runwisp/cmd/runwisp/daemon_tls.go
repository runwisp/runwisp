// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/tlscert"
)

// tlsSetup is the resolved transport configuration for the main listener: the
// cert/key paths to hand the server (empty for plain HTTP), the advertised URL
// scheme, and — when serving HTTPS — the cert SHA-256 fingerprint the operator
// verifies out-of-band and the remote CLI pins.
type tlsSetup struct {
	CertPath    string
	KeyPath     string
	Scheme      string // "http" or "https"
	Fingerprint string // cert SHA-256 (hex); empty for plain HTTP
	Generated   bool   // true when auto self-signed, false when operator-provided
}

// resolveTLS decides how the main listener serves, generating a self-signed
// cert when auto-HTTPS engages. The matrix (see config.Daemon docs):
//   - operator-supplied tls_cert/tls_key  → HTTPS with those files (any host)
//   - tls = "off"                          → plain HTTP (operator owns TLS upstream)
//   - tls = "auto" + non-loopback bind     → self-signed HTTPS (the secure default)
//   - tls = "auto" + loopback bind         → plain HTTP (no eavesdrop risk locally)
//
// It is called once at boot, before the server is constructed, so a cert
// generation failure aborts startup loudly instead of silently falling back to
// cleartext on an exposed bind.
func resolveTLS(f Flags, d config.Daemon) (tlsSetup, error) {
	if tlsScheme(d, f.Host) == "http" {
		return tlsSetup{Scheme: "http"}, nil
	}

	if d.TLSCert != "" && d.TLSKey != "" {
		fp, err := tlscert.Fingerprint(d.TLSCert)
		if err != nil {
			return tlsSetup{}, fmt.Errorf("read tls_cert fingerprint: %w", err)
		}
		return tlsSetup{CertPath: d.TLSCert, KeyPath: d.TLSKey, Scheme: "https", Fingerprint: fp}, nil
	}

	certPath, keyPath, err := tlscert.EnsureSelfSigned(f.DataDir, tlscert.DefaultHosts(f.Host))
	if err != nil {
		return tlsSetup{}, fmt.Errorf("generate self-signed certificate: %w", err)
	}
	fp, err := tlscert.Fingerprint(certPath)
	if err != nil {
		return tlsSetup{}, fmt.Errorf("read generated cert fingerprint: %w", err)
	}
	return tlsSetup{CertPath: certPath, KeyPath: keyPath, Scheme: "https", Fingerprint: fp, Generated: true}, nil
}

// tlsScheme reports the URL scheme a given daemon config + bind host resolves
// to, without generating anything. resolveTLS uses it to short-circuit the
// plain-HTTP case; daemonListenURL and the restart summary use it to print the
// right scheme without standing up a server.
func tlsScheme(d config.Daemon, host string) string {
	if d.TLSCert != "" && d.TLSKey != "" {
		return "https"
	}
	if d.TLS == config.TLSModeOff {
		return "http"
	}
	if isNonLoopbackBind(host) {
		return "https"
	}
	return "http"
}

// isNonLoopbackBind reports whether a bind host is reachable beyond localhost.
// Shared by the security-warning banner and TLS resolution so "what counts as
// exposed" is defined in exactly one place.
func isNonLoopbackBind(host string) bool {
	return host != "127.0.0.1" && host != "::1" && host != "localhost"
}
