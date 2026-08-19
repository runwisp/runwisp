// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type LogReplayRequestMessage struct {
	Type            string `json:"type" binding:"required"`
	ProtocolVersion int    `json:"protocolVersion,omitempty"`
	SentAt          string `json:"sentAt,omitempty"`
	RequestID       string `json:"requestId" binding:"required"`
	ExecutionID     string `json:"executionId" binding:"required"`
	FromLine        int64  `json:"fromLine" binding:"required"`
	Limit           int64  `json:"limit" binding:"required"`
}
