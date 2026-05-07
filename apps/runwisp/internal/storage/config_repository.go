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

// Database is the full persistent store for the daemon: runs + configuration + notifications.
type Database interface {
	RunRepository
	ConfigRepository
	NotificationRepository
	PendingLogUploadRepository
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
