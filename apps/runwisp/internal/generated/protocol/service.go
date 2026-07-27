// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type Service struct {
	TaskID            string                 `json:"taskId" binding:"required"`
	TaskName          string                 `json:"taskName" binding:"required"`
	Script            json.RawMessage        `json:"script" binding:"required"`
	Instances         int                    `json:"instances" binding:"required"`
	Autostart         bool                   `json:"autostart,omitempty"`
	RestartDelay      int                    `json:"restartDelay,omitempty"`
	RestartBackoff    *ServiceRestartBackoff `json:"restartBackoff,omitempty"`
	BackoffResetAfter int                    `json:"backoffResetAfter,omitempty"`
	TaskConfig        *ServiceTaskConfig     `json:"taskConfig,omitempty"`
}
