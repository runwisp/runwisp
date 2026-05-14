// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type LinesItem struct {
	N         int64            `json:"n" binding:"required"`
	Ts        int64            `json:"ts,omitempty"`
	Stream    *LinesItemStream `json:"stream" binding:"required"`
	Text      string           `json:"text" binding:"required"`
	Continued bool             `json:"continued,omitempty"`
}
