// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type ServiceRemoveMessage struct {
	Type   string `json:"type" binding:"required"`
	V      int    `json:"v,omitempty"`
	SentAt string `json:"sentAt,omitempty"`
	TaskID string `json:"taskId" binding:"required"`
}
