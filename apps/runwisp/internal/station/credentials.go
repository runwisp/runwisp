// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// credentialsKey is the config entry holding the token the station rotated in.
const credentialsKey = "station_credentials"

// CredentialStore persists small daemon settings (storage.SQLiteDatabase).
type CredentialStore interface {
	GetConfigValue(ctx context.Context, key string) (string, bool, error)
	SetConfigValue(ctx context.Context, key, value string) error
}

// storedCredentials is a rotated token and a fingerprint of the token the
// operator had configured when it was rotated in. The rotated token applies
// only while the operator still configures that same token, so configuring a
// new one (a fresh registration) always wins over a stale rotation.
type storedCredentials struct {
	Token            string `json:"token"`
	ConfiguredSHA256 string `json:"configuredSha256"`
}

func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func saveRotatedToken(ctx context.Context, store CredentialStore, configured, token string) error {
	raw, err := json.Marshal(storedCredentials{Token: token, ConfiguredSHA256: tokenFingerprint(configured)})
	if err != nil {
		return err
	}
	return store.SetConfigValue(ctx, credentialsKey, string(raw))
}

// ApplyRotatedToken switches cfg to the token the station last rotated in,
// if the operator still configures the token it replaced. Call it at boot,
// before NewClient.
func ApplyRotatedToken(ctx context.Context, store CredentialStore, cfg *Config) error {
	if !cfg.Enabled || store == nil {
		return nil
	}
	raw, ok, err := store.GetConfigValue(ctx, credentialsKey)
	if err != nil || !ok {
		return err
	}
	var stored storedCredentials
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return err
	}
	if stored.Token != "" && stored.ConfiguredSHA256 == tokenFingerprint(cfg.ConfiguredToken) {
		cfg.StationToken = stored.Token
	}
	return nil
}
