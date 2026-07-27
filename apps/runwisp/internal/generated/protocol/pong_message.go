// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type PongMessage struct {
	Type   string `json:"type" binding:"required"`
	V      int    `json:"v,omitempty"`
	SentAt string `json:"sentAt,omitempty"`
}
