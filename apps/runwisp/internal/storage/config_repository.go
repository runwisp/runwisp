// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

const (
	ConfigKeyJWTSecret      = "jwt_secret"
	ConfigKeyFingerprint    = "fingerprint"
	ConfigKeyPassword       = "password"
	ConfigKeyEnvPasswordSum = "env_password_hash"
	ConfigKeySchemaVersion  = "schema_version"
	ConfigKeySRPVerifier    = "srp_verifier"
	ConfigKeySRPSalt        = "srp_salt"
)

// CurrentSchemaVersion is the schema_version value written by this binary.
// A schema_version bump triggers JWT-secret rotation on first boot, which
// invalidates any web-UI sessions issued by the previous binary.
const CurrentSchemaVersion = "2"

// SecretKeys is the set of config_entries.key values whose stored value is
// treated as a secret. When a *secretcipher.Cipher is supplied to the
// database constructor, these rows are transparently AES-GCM-encrypted at
// rest. Keep this set authoritative — every secret-bearing key must be
// listed here so the encrypt-in-place migration knows what to rewrite.
var SecretKeys = map[string]struct{}{
	ConfigKeyJWTSecret:      {},
	ConfigKeyPassword:       {},
	ConfigKeyEnvPasswordSum: {},
	ConfigKeySRPVerifier:    {},
	ConfigKeySRPSalt:        {},
}

// IsSecretKey reports whether key is a secret-bearing config row.
func IsSecretKey(key string) bool {
	_, ok := SecretKeys[key]
	return ok
}

// ConfigRepository stores and retrieves named daemon configuration values.
type ConfigRepository interface {
	// GetConfigValue returns the stored value for key, or ("", false, nil) if not set.
	// For secret keys, the returned value is the raw stored representation
	// (encrypted prefix included). Callers wanting transparent decryption
	// should use SecretStore.
	GetConfigValue(key string) (string, bool, error)
	// SetConfigValue persists key=value, overwriting any existing entry.
	// Writes the raw value verbatim; SecretStore is the entry point that
	// applies at-rest encryption for whitelisted secret keys.
	SetConfigValue(key, value string) error
}

// SecretStore is a ConfigRepository wrapper that transparently encrypts and
// decrypts secret-keyed values when a cipher is configured. Non-secret keys
// pass through unchanged so the rest of the daemon doesn't need to care
// whether at-rest encryption is on.
type SecretStore interface {
	ConfigRepository
	// GetSecret returns the (decrypted) value for key, or ("", false, nil) if absent.
	GetSecret(key string) (string, bool, error)
	// SetSecret writes value for key, encrypting it when key is secret and a
	// cipher is configured.
	SetSecret(key, value string) error
	// DeleteConfigValue removes the row for key. Absent rows are no-ops.
	DeleteConfigValue(key string) error
}

// Database is the full persistent store for the daemon: runs + configuration + notifications.
type Database interface {
	RunRepository
	ConfigRepository
	NotificationRepository
	PendingLogUploadRepository
	SecretStore
}

// PendingLogUpload is a record of a dispatch that handed the daemon a signed
// PUT URL for terminal log archival. The row is removed on a successful
// upload; the crash-recovery sweep at startup retries any rows still present.
type PendingLogUpload struct {
	ExternalExecutionID string
	UploadURL           string
	LogPath             string
	InsertedAt          int64
}

// PendingLogUploadRepository persists dispatch metadata so the daemon can
// resume terminal log archival after a crash.
type PendingLogUploadRepository interface {
	UpsertPendingLogUpload(rec PendingLogUpload) error
	DeletePendingLogUpload(externalExecutionID string) error
	ListPendingLogUploads() ([]PendingLogUpload, error)
}
