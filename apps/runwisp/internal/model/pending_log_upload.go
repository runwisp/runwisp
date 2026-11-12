// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

// PendingLogUpload records a station execution whose log still has to be
// archived. The row is removed once the archive is uploaded (or the station
// declines it); the daemon retries the rows still present after every
// reconnect, so a crash or an unreachable station never loses the log.
type PendingLogUpload struct {
	ExecutionID string
	InsertedAt  int64
}
