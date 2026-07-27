// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestEndReason_ExitCodeClassification(t *testing.T) {
	tests := []struct {
		name    string
		result  ExecuteResult
		wantEnd model.EndReason
	}{
		{"default zero is success", ExecuteResult{ExitCode: 0}, model.ReasonSuccess},
		{"default non-zero is failed", ExecuteResult{ExitCode: 1}, model.ReasonFailed},
		{"listed code is success", ExecuteResult{ExitCode: 2, SuccessExitCodes: []int{0, 2}}, model.ReasonSuccess},
		{"zero still success in custom set", ExecuteResult{ExitCode: 0, SuccessExitCodes: []int{0, 2}}, model.ReasonSuccess},
		{"unlisted code is failed", ExecuteResult{ExitCode: 1, SuccessExitCodes: []int{0, 2}}, model.ReasonFailed},
		{"timeout overrides exit code", ExecuteResult{ExitCode: 2, TimedOut: true, SuccessExitCodes: []int{0, 2}}, model.ReasonTimeout},
		{"stopped overrides exit code", ExecuteResult{ExitCode: 2, Stopped: true, SuccessExitCodes: []int{0, 2}}, model.ReasonStopped},
		{"log overflow overrides exit code", ExecuteResult{ExitCode: 0, KilledByPolicy: true}, model.ReasonLogOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantEnd, tt.result.EndReason())
		})
	}
}
