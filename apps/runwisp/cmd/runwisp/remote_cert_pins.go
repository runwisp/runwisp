// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
)

// A remote daemon serving auto-HTTPS presents a self-signed certificate with no
// CA chain to validate, so the CLI trusts it the way ssh trusts host keys:
// pin the cert SHA-256 on first connect and refuse to talk to that URL again if
// the fingerprint ever changes. The pins live in a per-user cache file keyed by
// daemon URL — a remote client has no --data dir of its own, and one CLI may
// hold pins for several daemons. The operator verifies the first-seen
// fingerprint against the daemon's startup banner out-of-band.

// certPinStore persists TOFU pins to ~/.cache/runwisp/pinned_certs.json. It
// satisfies apiclient.CertPinStore; the apiclient calls Load/Store from inside
// the TLS handshake, so both must tolerate a missing or corrupt file.
type certPinStore struct{}

// pinStorePath returns the per-user pin file, parallel to the token cache.
func pinStorePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "runwisp", "pinned_certs.json"), nil
}

// Load returns the pinned fingerprint for key, or ok=false when the daemon has
// not been seen (or the file is unreadable — a missing pin is treated as
// first-contact, letting Store record it).
func (certPinStore) Load(key string) (string, bool) {
	path, err := pinStorePath()
	if err != nil {
		return "", false
	}
	fp, ok := loadJSONCacheMap[string](path)[key]
	return fp, ok && fp != ""
}

// Store records the fingerprint observed on first contact, read-modify-writing
// the pin map. Failures are logged at debug and swallowed: a write that doesn't
// land just means the next connection re-pins (still safe — a mismatch only
// arises against a pin that was actually stored).
func (certPinStore) Store(key, fingerprint string) {
	path, err := pinStorePath()
	if err != nil {
		return
	}
	storeJSONCacheEntry(path, "cert pin", key, fingerprint)
}
