// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type Execution struct {
	ID           string               `json:"id" binding:"required"`
	TaskID       string               `json:"taskId" binding:"required"`
	TaskName     string               `json:"taskName" binding:"required"`
	ExecutionID  string               `json:"executionId" binding:"required"`
	Priority     int                  `json:"priority" binding:"required"`
	TriggeredBy  *TriggerType         `json:"triggeredBy" binding:"required"`
	InputValues  map[string]string    `json:"inputValues" binding:"required"`
	Script       json.RawMessage      `json:"script" binding:"required"`
	Timeout      int                  `json:"timeout" binding:"required"`
	TaskConfig   *ExecutionTaskConfig `json:"taskConfig,omitempty"`
	LogUploadURL string               `json:"logUploadUrl" binding:"required"`
	LogPath      string               `json:"logPath" binding:"required"`
}
