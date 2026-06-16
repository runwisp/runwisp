// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type ServiceControlMessage struct {
	Type   string  `json:"type" binding:"required"`
	V      int     `json:"v,omitempty"`
	SentAt string  `json:"sentAt,omitempty"`
	TaskID string  `json:"taskId" binding:"required"`
	Action *Action `json:"action" binding:"required"`
}
