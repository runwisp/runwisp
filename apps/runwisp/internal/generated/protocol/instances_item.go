// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"time"
)

type InstancesItem struct {
	Index        int                   `json:"index" binding:"required"`
	State        *ServiceInstanceState `json:"state" binding:"required"`
	Pid          int                   `json:"pid,omitempty"`
	StartedAt    *time.Time            `json:"startedAt,omitempty"`
	RestartCount int                   `json:"restartCount" binding:"required"`
	LastExitCode int                   `json:"lastExitCode,omitempty"`
}
