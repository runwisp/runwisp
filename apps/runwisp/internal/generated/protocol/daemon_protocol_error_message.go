// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type DaemonProtocolErrorMessage struct {
	Type        string `json:"type" binding:"required"`
	V           int    `json:"v,omitempty"`
	SentAt      string `json:"sentAt,omitempty"`
	Code        string `json:"code" binding:"required"`
	Message     string `json:"message" binding:"required"`
	RequestID   string `json:"requestId,omitempty"`
	ExecutionID string `json:"executionId,omitempty"`
}
