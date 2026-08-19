// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type ProtocolErrorMessage struct {
	Type            string `json:"type" binding:"required"`
	ProtocolVersion int    `json:"protocolVersion,omitempty"`
	SentAt          string `json:"sentAt,omitempty"`
	Code            string `json:"code" binding:"required"`
	Message         string `json:"message" binding:"required"`
	RequestID       string `json:"requestId,omitempty"`
	ExecutionID     string `json:"executionId,omitempty"`
}
