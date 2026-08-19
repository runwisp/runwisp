// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type ServiceStatusMessage struct {
	Type             string          `json:"type" binding:"required"`
	ProtocolVersion  int             `json:"protocolVersion,omitempty"`
	SentAt           string          `json:"sentAt,omitempty"`
	TaskID           string          `json:"taskId" binding:"required"`
	State            *ServiceState   `json:"state" binding:"required"`
	DesiredInstances int             `json:"desiredInstances" binding:"required"`
	RunningInstances int             `json:"runningInstances" binding:"required"`
	Instances        []InstancesItem `json:"instances" binding:"required"`
}
