// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type HitsItem struct {
	N         int64           `json:"n" binding:"required"`
	Ts        int64           `json:"ts,omitempty"`
	Stream    *HitsItemStream `json:"stream" binding:"required"`
	Text      string          `json:"text" binding:"required"`
	Continued bool            `json:"continued,omitempty"`
}
