// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type SystemStats struct {
	CpuUsage float64 `json:"cpuUsage,omitempty"`
	MemUsage float64 `json:"memUsage,omitempty"`
	MemTotal int     `json:"memTotal,omitempty"`
	MemUsed  int     `json:"memUsed,omitempty"`
	CpuCores int     `json:"cpuCores,omitempty"`
	Uptime   string  `json:"uptime,omitempty"`
	Version  string  `json:"version,omitempty"`
	Host     string  `json:"host,omitempty"`
	Os       string  `json:"os,omitempty"`
	Arch     string  `json:"arch,omitempty"`
}
