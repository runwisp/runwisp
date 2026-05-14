// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import "errors"

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("record not found")

// PendingLogUpload records a dispatch that handed the daemon a signed PUT URL
// for terminal log archival. The row is removed on a successful upload; the
// crash-recovery sweep at startup retries any rows still present.
type PendingLogUpload struct {
	ExternalExecutionID string
	UploadURL           string
	LogPath             string
	InsertedAt          int64
}
