// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import "github.com/runwisp/runwisp/internal/storage/sqlcdb"

const (
	ConfigKeyFingerprint = "fingerprint"
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

// PendingLogUploadRepository persists dispatch metadata so the daemon can
// resume terminal log archival after a crash.
type PendingLogUploadRepository interface {
	UpsertPendingLogUpload(rec sqlcdb.PendingLogUpload) error
	DeletePendingLogUpload(externalExecutionID string) error
	ListPendingLogUploads() ([]sqlcdb.PendingLogUpload, error)
}
