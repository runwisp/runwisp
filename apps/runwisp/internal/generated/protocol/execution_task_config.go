// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

type ExecutionTaskConfig struct {
	Env          map[string]string             `json:"env,omitempty"`
	GracefulStop int                           `json:"gracefulStop,omitempty"`
	LogMaxSize   int                           `json:"logMaxSize,omitempty"`
	LogOnFull    *ExecutionTaskConfigLogOnFull `json:"logOnFull,omitempty"`
}
