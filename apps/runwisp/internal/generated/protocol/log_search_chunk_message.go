// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type LogSearchChunkMessage struct {
	Type        string     `json:"type" binding:"required"`
	V           int        `json:"v,omitempty"`
	SentAt      string     `json:"sentAt,omitempty"`
	ID          string     `json:"id" binding:"required"`
	ExecutionID string     `json:"executionId" binding:"required"`
	Hits        []HitsItem `json:"hits" binding:"required"`
	NextLine    int        `json:"nextLine,omitempty"`
	Exhausted   bool       `json:"exhausted" binding:"required"`
}
