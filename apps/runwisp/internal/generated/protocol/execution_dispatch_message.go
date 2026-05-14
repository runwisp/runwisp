// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type ExecutionDispatchMessage struct {
	Type      string     `json:"type" binding:"required"`
	V         int        `json:"v,omitempty"`
	SentAt    string     `json:"sentAt,omitempty"`
	Execution *Execution `json:"execution" binding:"required"`
}
