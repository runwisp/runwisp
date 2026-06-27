// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package tlscert generates and persists the daemon's self-signed TLS
// certificate. It is the offline-complete alternative to ACME: a long-lived
// ECDSA cert with SANs for the bind host and loopback, written to the data dir
// with the same 0600/symlink-guarded primitive as every other daemon secret.
// The cert is identified out-of-band by its SHA-256 fingerprint, which the
// remote CLI pins (TOFU) and the operator verifies from the startup banner.
//
// Everything here is pure given (dataDir, hosts) and a clock — generation,
// SAN classification, and fingerprinting are independently testable. The one
// impure helper, DefaultHosts, lives at the call boundary so the rest stays
// deterministic.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/runwisp/runwisp/internal/datadir"
)

const (
	// certValidity is how long a freshly minted cert stays valid. Long because
	// rotation here means "browser shows a new warning + every pinned client
	// re-pins" — friction we want to incur rarely, not annually. The cert is
	// self-signed and pinned by fingerprint, so a long lifetime carries none of
	// the revocation risk a public CA cert would.
	certValidity = 10 * 365 * 24 * time.Hour

	// renewBefore triggers regeneration once a cert is within this window of
	// expiry, so a daemon that has been running (or sitting stopped) for years
	// rolls over before clients start rejecting an expired cert.
	renewBefore = 30 * 24 * time.Hour

	certFileName = "cert.pem"
	keyFileName  = "key.pem"
	tlsDirName   = "tls"
)

// EnsureSelfSigned returns paths to a usable cert/key pair under dataDir/tls,
// generating them if missing, expired (or near expiry), or if the existing
// cert doesn't cover every host in hosts (e.g. the operator changed --host).
// hosts is the full desired SAN set; callers typically build it with
// DefaultHosts. Files are persisted via datadir.WriteSecretFile (0600,
// symlink-guarded) inside a 0700 directory, so the key never leaks to other
// local users — and a fresh pair is recoverable after any crash.
func EnsureSelfSigned(dataDir string, hosts []string) (certPath, keyPath string, err error) {
	return ensureSelfSigned(dataDir, hosts, time.Now())
}

func ensureSelfSigned(dataDir string, hosts []string, now time.Time) (certPath, keyPath string, err error) {
	dir := filepath.Join(dataDir, tlsDirName)
	certPath = filepath.Join(dir, certFileName)
	keyPath = filepath.Join(dir, keyFileName)

	if usableCert(certPath, keyPath, hosts, now) {
		return certPath, keyPath, nil
	}

	certPEM, keyPEM, err := generate(hosts, now)
	if err != nil {
		return "", "", err
	}
	if err := datadir.EnsureDir(dir); err != nil {
		return "", "", fmt.Errorf("create tls dir: %w", err)
	}
	if err := datadir.WriteSecretFile(certPath, certPEM); err != nil {
		return "", "", fmt.Errorf("write cert: %w", err)
	}
	if err := datadir.WriteSecretFile(keyPath, keyPEM); err != nil {
		return "", "", fmt.Errorf("write key: %w", err)
	}
	return certPath, keyPath, nil
}

// usableCert reports whether the on-disk pair can be reused as-is: both files
// load, the cert is in its validity window (with renewBefore margin), and it
// covers every requested host. Any failure falls through to regeneration —
// the on-disk pair is fully owned by the daemon, so replacing it is safe.
func usableCert(certPath, keyPath string, hosts []string, now time.Time) bool {
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	leaf, err := loadLeaf(certPath)
	if err != nil {
		return false
	}
	if now.Before(leaf.NotBefore) || now.Add(renewBefore).After(leaf.NotAfter) {
		return false
	}
	return coversHosts(leaf, hosts)
}

// coversHosts reports whether leaf's SANs include every host requested. A miss
// (operator added a new --host) forces a regen so the new address is reachable
// over TLS without manual cert surgery.
func coversHosts(leaf *x509.Certificate, hosts []string) bool {
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if err := leaf.VerifyHostname(h); err != nil {
			return false
		}
	}
	return true
}

// generate mints a self-signed ECDSA P-256 cert + key for hosts, returned as
// PEM. IP-shaped hosts land in IPAddresses, the rest in DNSNames; duplicates
// and empties are dropped.
func generate(hosts []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	dnsNames, ipAddrs := classifyHosts(hosts)
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "RunWisp self-signed"},
		NotBefore:             now.Add(-time.Hour), // tolerate minor clock skew on clients
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddrs,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// classifyHosts splits a host list into DNS names and IP addresses, dropping
// empties and duplicates while preserving first-seen order.
func classifyHosts(hosts []string) (dnsNames []string, ipAddrs []net.IP) {
	seen := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		if ip := net.ParseIP(h); ip != nil {
			ipAddrs = append(ipAddrs, ip)
		} else {
			dnsNames = append(dnsNames, h)
		}
	}
	return dnsNames, ipAddrs
}

// DefaultHosts builds the SAN set for a daemon binding to bindHost: the bind
// address itself plus loopback aliases and the machine hostname, so the same
// cert serves loopback probes, the bind address, and a hostname-based URL.
// Impure (reads os.Hostname) by design — kept at the boundary so EnsureSelfSigned
// stays deterministic.
func DefaultHosts(bindHost string) []string {
	hosts := []string{bindHost, "127.0.0.1", "::1", "localhost"}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		hosts = append(hosts, hn)
	}
	return hosts
}

// Fingerprint returns the SHA-256 of the leaf certificate's DER bytes as lower
// case hex. It is the value the remote CLI pins and the operator verifies from
// the startup banner; FingerprintDER computes the same value from a live
// connection's certificate.
func Fingerprint(certPath string) (string, error) {
	leaf, err := loadLeaf(certPath)
	if err != nil {
		return "", err
	}
	return FingerprintDER(leaf.Raw), nil
}

// FingerprintDER returns the lower-case hex SHA-256 of a certificate's raw DER
// bytes (x509.Certificate.Raw), matching what Fingerprint reads off disk.
func FingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// loadLeaf reads and parses the first certificate in a PEM file.
func loadLeaf(certPath string) (*x509.Certificate, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("no certificate found in %s", certPath)
	}
	return x509.ParseCertificate(block.Bytes)
}
