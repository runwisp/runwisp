// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"time"
)

type ExecutionUpdateMessage struct {
	Type            string           `json:"type" binding:"required"`
	ProtocolVersion int              `json:"protocolVersion,omitempty"`
	SentAt          string           `json:"sentAt,omitempty"`
	ExecutionID     string           `json:"executionId" binding:"required"`
	Status          *ExecutionStatus `json:"status" binding:"required"`
	ExitCode        *int             `json:"exitCode,omitempty"`
	StartedAt       *time.Time       `json:"startedAt,omitempty"`
	FinishedAt      *time.Time       `json:"finishedAt,omitempty"`
	LogPath         string           `json:"logPath,omitempty"`
	LogSize         int64            `json:"logSize,omitempty"`
}
