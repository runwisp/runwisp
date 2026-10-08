// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestEndReason_ExitCodeClassification(t *testing.T) {
	tests := []struct {
		name    string
		result  ExecuteResult
		wantEnd model.EndReason
	}{
		{"zero is success", ExecuteResult{ExitCode: 0}, model.ReasonSuccess},
		{"non-zero is failed", ExecuteResult{ExitCode: 1}, model.ReasonFailed},
		{"other non-zero is failed", ExecuteResult{ExitCode: 2}, model.ReasonFailed},
		{"timeout overrides exit code", ExecuteResult{ExitCode: 2, TimedOut: true}, model.ReasonTimeout},
		{"stopped overrides exit code", ExecuteResult{ExitCode: 2, Stopped: true}, model.ReasonStopped},
		{"policy kill overrides exit code", ExecuteResult{ExitCode: 0, KillReason: model.ReasonLogOverflow}, model.ReasonLogOverflow},
		{"health kill records unhealthy", ExecuteResult{ExitCode: -1, KillReason: model.ReasonUnhealthy}, model.ReasonUnhealthy},
		{"timeout wins over a policy kill", ExecuteResult{ExitCode: -1, TimedOut: true, KillReason: model.ReasonLogOverflow}, model.ReasonTimeout},
		{"output match fails exit zero", ExecuteResult{ExitCode: 0, OutputMatched: true}, model.ReasonFailed},
		{"timeout overrides output match", ExecuteResult{ExitCode: 0, OutputMatched: true, TimedOut: true}, model.ReasonTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantEnd, tt.result.EndReason())
		})
	}
}
