// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

// PendingLogUpload records a dispatch that handed the daemon a signed PUT URL
// for terminal log archival. The row is removed on a successful upload; the
// crash-recovery sweep at startup retries any rows still present.
type PendingLogUpload struct {
	ExternalExecutionID string
	UploadURL           string
	LogPath             string
	InsertedAt          int64
}
