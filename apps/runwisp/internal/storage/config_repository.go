// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"

	"github.com/runwisp/runwisp/internal/model"
)

const (
	ConfigKeyFingerprint = "fingerprint"
)

// ConfigRepository stores and retrieves named daemon configuration values.
type ConfigRepository interface {
	// GetConfigValue returns the stored value for key, or ("", false, nil) if not set.
	GetConfigValue(ctx context.Context, key string) (string, bool, error)
	// SetConfigValue persists key=value, overwriting any existing entry.
	SetConfigValue(ctx context.Context, key, value string) error
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
	UpsertPendingLogUpload(ctx context.Context, rec model.PendingLogUpload) error
	DeletePendingLogUpload(ctx context.Context, externalExecutionID string) error
	ListPendingLogUploads(ctx context.Context) ([]model.PendingLogUpload, error)
}
