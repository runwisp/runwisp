// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

const (
	ConfigKeyJWTSecret    = "jwt_secret"
	ConfigKeyFingerprint  = "fingerprint"
	ConfigKeyPasswordHash = "password_hash"
)

// ConfigRepository stores and retrieves named daemon configuration values.
type ConfigRepository interface {
	// GetConfigValue returns the stored value for key, or ("", false, nil) if not set.
	GetConfigValue(key string) (string, bool, error)
	// SetConfigValue persists key=value, overwriting any existing entry.
	SetConfigValue(key, value string) error
}

// Database is the full persistent store for the daemon: runs + configuration.
type Database interface {
	RunRepository
	ConfigRepository
}
