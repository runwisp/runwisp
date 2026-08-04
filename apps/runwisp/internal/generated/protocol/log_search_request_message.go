// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type LogSearchRequestMessage struct {
	Type          string `json:"type" binding:"required"`
	V             int    `json:"v,omitempty"`
	SentAt        string `json:"sentAt,omitempty"`
	RequestID     string `json:"requestId" binding:"required"`
	ExecutionID   string `json:"executionId" binding:"required"`
	Query         string `json:"query" binding:"required"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	Limit         int64  `json:"limit,omitempty"`
	FromLine      int64  `json:"fromLine,omitempty"`
}
