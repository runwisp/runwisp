// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package home

import (
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderHomeHeader_PasswordIsMasked is the regression test for the
// disk-pass refactor's security guarantee: the plaintext ephemeral password
// must never appear in a rendered home header, regardless of selection state.
func TestRenderHomeHeader_PasswordIsMasked(t *testing.T) {
	const secret = "Kj2x9pQ7mN4vL8rT5wYz1c"
	info := uikit.StartupInfo{
		Port:              9477,
		PasswordEphemeral: true,
		Password:          secret,
	}

	for _, tc := range []struct {
		name   string
		cursor int
	}{
		{name: "no-selection", cursor: -1},
		{name: "password-selected", cursor: 1}, // 0 = Web UI, 1 = Password (no launch ticket)
	} {
		t.Run(tc.name, func(t *testing.T) {
			header, _ := RenderHeader(info, false, 80, tc.cursor, -1)
			assert.NotContains(t, header, secret,
				"plaintext password must never appear in the rendered home header")
			assert.Contains(t, header, "Password", "label should still be present")
			// 22 bullets so the mask matches GeneratePassword's output length.
			assert.Contains(t, header, strings.Repeat("•", PasswordMaskWidth),
				"masked bullets should be rendered in place of the value")
		})
	}
}

func TestRenderHomeHeader_HintWhenPasswordSelected(t *testing.T) {
	info := uikit.StartupInfo{
		Port:              9477,
		PasswordEphemeral: true,
		Password:          "anything",
	}
	header, _ := RenderHeader(info, false, 80, 1, -1)
	assert.Contains(t, header, "press Enter to copy",
		"selected password row should show the copy hint")
}

func TestRenderHomeHeader_OmitsPasswordWhenNotEphemeral(t *testing.T) {
	info := uikit.StartupInfo{
		Port:              9477,
		PasswordEphemeral: false,
		Password:          "should-be-ignored",
	}
	header, _ := RenderHeader(info, false, 80, -1, -1)
	assert.NotContains(t, header, "Password",
		"env-var case must not render a Password field at all")
	assert.NotContains(t, header, "should-be-ignored")
}

// TestNextCronRun_ResultShapes covers every shape nextCronRun can produce:
// the seconds/minutes/hours buckets via different schedules, the empty-input
// and invalid-input zero cases, and the documented `HH:MM:SS (in …)` format.
func TestNextCronRun_ResultShapes(t *testing.T) {
	tests := []struct {
		name string
		expr string
		// empty: assert empty result; otherwise assert non-empty + Contains.
		empty    bool
		contains []string
	}{
		{name: "every-minute-shows-seconds", expr: "* * * * *", contains: []string{"s", " (in "}},
		{name: "hourly-zero-minute", expr: "0 * * * *", contains: []string{" (in "}},
		{name: "half-hour-every-2h", expr: "30 */2 * * *", contains: []string{" (in "}},
		{name: "hourly-shortcut", expr: "@hourly", contains: []string{" (in "}},
		{name: "weekly-shortcut", expr: "@weekly", contains: []string{" (in "}},
		{name: "empty-schedule", expr: "", empty: true},
		{name: "invalid-expression", expr: "not-a-cron-expression", empty: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nextCronRun(tt.expr)
			if tt.empty {
				assert.Empty(t, result)
				return
			}
			require.NotEmpty(t, result)
			for _, sub := range tt.contains {
				assert.Contains(t, result, sub, "result %q must contain %q", result, sub)
			}
		})
	}

	t.Run("format-is-HH-MM-SS-followed-by-relative-suffix", func(t *testing.T) {
		result := nextCronRun("* * * * *")
		require.NotEmpty(t, result)
		parts := strings.SplitN(result, " (in ", 2)
		require.Len(t, parts, 2, "result must contain ' (in ' separator: %s", result)
		_, err := time.Parse("15:04:05", parts[0])
		assert.NoError(t, err, "time part %q must be HH:MM:SS", parts[0])
	})

	t.Run("hourly-shows-seconds-or-minutes", func(t *testing.T) {
		result := nextCronRun("0 * * * *")
		require.NotEmpty(t, result)
		if !strings.Contains(result, "s") && !strings.Contains(result, "m") {
			t.Fatalf("expected seconds or minutes in hourly cron result, got %q", result)
		}
	})
}

func TestFields_NoWebUI(t *testing.T) {
	info := uikit.StartupInfo{WebUIDisabled: true, Port: 9477}
	fields := Fields(info, false)
	assert.Empty(t, fields)
}

func TestFields_NoPort(t *testing.T) {
	info := uikit.StartupInfo{Port: 0}
	fields := Fields(info, false)
	assert.Empty(t, fields)
}

func TestFields_WithWebUINoTicket(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	fields := Fields(info, false)
	assert.Len(t, fields, 1)
	assert.Equal(t, FieldWebUI, fields[0])
}

func TestFields_WithWebUIAndTicket(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	fields := Fields(info, true)
	assert.Len(t, fields, 2)
	assert.Equal(t, FieldOpenWebUI, fields[0])
	assert.Equal(t, FieldWebUI, fields[1])
}

func TestFields_WithPasswordAndWebUI(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477, PasswordEphemeral: true, Password: "secret"}
	fields := Fields(info, false)
	assert.Len(t, fields, 2)
	assert.Equal(t, FieldWebUI, fields[0])
	assert.Equal(t, FieldPassword, fields[1])
}

func TestRenderTaskHeader_ServiceTask(t *testing.T) {
	task := &model.TaskBrief{
		Kind:      model.KindService,
		Instances: 3,
	}
	out, _ := RenderTaskHeader("my-service", task, 80, false)
	assert.Contains(t, out, "my-service")
	assert.Contains(t, out, "service x3")
}

func TestRenderTaskHeader_CronTask(t *testing.T) {
	task := &model.TaskBrief{
		Kind: model.KindTask,
		Cron: "*/5 * * * *",
	}
	out, _ := RenderTaskHeader("my-task", task, 80, false)
	assert.Contains(t, out, "my-task")
	assert.Contains(t, out, "*/5 * * * *")
	assert.Contains(t, out, "Next:")
}

func TestRenderTaskHeader_ManualTask(t *testing.T) {
	task := &model.TaskBrief{
		Kind: model.KindTask,
		Cron: "",
	}
	out, _ := RenderTaskHeader("manual-task", task, 80, false)
	assert.Contains(t, out, "manual-task")
	assert.Contains(t, out, "manual")
}

func TestRenderTaskHeader_NilTask(t *testing.T) {
	out, _ := RenderTaskHeader("task-name", nil, 80, false)
	assert.Contains(t, out, "task-name")
	assert.Contains(t, out, "manual")
}

func TestRenderTaskHeader_RunNowButtonLineY(t *testing.T) {
	task := &model.TaskBrief{Kind: model.KindTask, Cron: "*/5 * * * *"}
	_, btnY := RenderTaskHeader("my-task", task, 80, false)
	assert.Equal(t, 2, btnY)
}

func TestRenderTaskHeader_HoveredButton(t *testing.T) {
	task := &model.TaskBrief{Kind: model.KindTask}
	outHovered, _ := RenderTaskHeader("t", task, 80, true)
	outNormal, _ := RenderTaskHeader("t", task, 80, false)
	assert.NotEmpty(t, outHovered)
	assert.NotEmpty(t, outNormal)
}

func TestRenderHeader_CloudConnected(t *testing.T) {
	info := uikit.StartupInfo{
		Port:         9477,
		CloudEnabled: true,
	}
	header, _ := RenderHeader(info, false, 80, -1, -1)
	assert.Contains(t, header, "Cloud connected")
}

func TestRenderHeader_WebUIDisabled(t *testing.T) {
	info := uikit.StartupInfo{
		WebUIDisabled: true,
	}
	header, _ := RenderHeader(info, false, 80, -1, -1)
	assert.Contains(t, header, "Web UI disabled")
}

func TestRenderHeader_FieldsStartY(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	_, fieldsStartY := RenderHeader(info, false, 80, -1, -1)
	assert.True(t, fieldsStartY >= 4, "fieldsStartY should be >= 4, got %d", fieldsStartY)
}

func TestRenderHeader_Hovered(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	header, _ := RenderHeader(info, false, 80, -1, 0)
	assert.NotEmpty(t, header)
}
