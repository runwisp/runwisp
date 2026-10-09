// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package home

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
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
			header, _ := RenderHeader(info, false, 80, tc.cursor, -1, false)
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
	header, _ := RenderHeader(info, false, 80, 1, -1, false)
	assert.Contains(t, header, "press Enter to copy",
		"selected password row should show the copy hint")
}

func TestRenderHomeHeader_OmitsPasswordWhenNotEphemeral(t *testing.T) {
	info := uikit.StartupInfo{
		Port:              9477,
		PasswordEphemeral: false,
		Password:          "should-be-ignored",
	}
	header, _ := RenderHeader(info, false, 80, -1, -1, false)
	assert.NotContains(t, header, "Password",
		"env-var case must not render a Password field at all")
	assert.NotContains(t, header, "should-be-ignored")
}

// TestRenderHomeHeader_AuthDisabled locks the RUNWISP_AUTH=off presentation:
// the Password row reads "disabled" instead of a masked value, the minted
// password never leaks into the render, and the row is not focusable (no
// FieldPassword), so it can't be copied as if it were a credential.
func TestRenderHomeHeader_AuthDisabled(t *testing.T) {
	const minted = "Kj2x9pQ7mN4vL8rT5wYz1c"
	info := uikit.StartupInfo{
		Port:              9477,
		AuthDisabled:      true,
		PasswordEphemeral: true,
		Password:          minted,
	}

	header, _ := RenderHeader(info, false, 80, -1, -1, false)
	assert.Contains(t, header, "disabled (RUNWISP_AUTH=off)")
	assert.NotContains(t, header, minted,
		"the internally minted password must never appear when auth is disabled")
	assert.NotContains(t, header, strings.Repeat("•", PasswordMaskWidth),
		"no masked value — there is no credential to hint at")

	assert.NotContains(t, Fields(info, false), FieldPassword,
		"the disabled row must not be focusable/copyable")
}

// TestNextCronRun_ResultShapes covers every shape NextCronRun can produce:
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
			result := NextCronRun(tt.expr, nil)
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
		result := NextCronRun("* * * * *", nil)
		require.NotEmpty(t, result)
		parts := strings.SplitN(result, " (in ", 2)
		require.Len(t, parts, 2, "result must contain ' (in ' separator: %s", result)
		_, err := time.Parse("15:04:05", parts[0])
		assert.NoError(t, err, "time part %q must be HH:MM:SS", parts[0])
	})

	t.Run("hourly-shows-seconds-or-minutes", func(t *testing.T) {
		result := NextCronRun("0 * * * *", nil)
		require.NotEmpty(t, result)
		if !strings.Contains(result, "s") && !strings.Contains(result, "m") {
			t.Fatalf("expected seconds or minutes in hourly cron result, got %q", result)
		}
	})
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
	task := &model.Task{
		Kind:      model.KindService,
		Instances: 3,
	}
	out, _ := TaskHeader{Name: "my-service", Task: task}.Render(80)
	assert.Contains(t, out, "my-service")
	assert.Contains(t, out, "service x3")
}

func TestRenderTaskHeader_CronTask(t *testing.T) {
	task := &model.Task{
		Kind: model.KindTask,
		Cron: "*/5 * * * *",
	}
	out, _ := TaskHeader{Name: "my-task", Task: task}.Render(80)
	assert.Contains(t, out, "my-task")
	assert.Contains(t, out, "*/5 * * * *")
	assert.Contains(t, out, "Next:")
}

func TestRenderTaskHeader_ManualTask(t *testing.T) {
	task := &model.Task{
		Kind: model.KindTask,
		Cron: "",
	}
	out, _ := TaskHeader{Name: "manual-task", Task: task}.Render(80)
	assert.Contains(t, out, "manual-task")
	assert.Contains(t, out, "manual")
}

func TestRenderTaskHeader_NilTask(t *testing.T) {
	out, _ := TaskHeader{Name: "task-name", Task: nil}.Render(80)
	assert.Contains(t, out, "task-name")
	assert.Contains(t, out, "manual")
}

func TestRenderTaskHeader_RunNowButtonLineY(t *testing.T) {
	task := &model.Task{Kind: model.KindTask, Cron: "*/5 * * * *"}
	_, btnY := TaskHeader{Name: "my-task", Task: task}.Render(80)
	assert.Equal(t, 1, btnY)
}

func TestRenderTaskHeader_HoveredButton(t *testing.T) {
	task := &model.Task{Kind: model.KindTask}
	outHovered, _ := TaskHeader{Name: "t", Task: task, Hovered: TaskButtonRun}.Render(80)
	outNormal, _ := TaskHeader{Name: "t", Task: task}.Render(80)
	assert.NotEmpty(t, outHovered)
	assert.NotEmpty(t, outNormal)
}

func TestRenderHeader_StationConnected(t *testing.T) {
	info := uikit.StartupInfo{
		Port:           9477,
		StationEnabled: true,
	}
	header, _ := RenderHeader(info, false, 80, -1, -1, false)
	assert.Contains(t, header, "Station connected")
}

func TestRenderHeader_ConfigStale(t *testing.T) {
	info := uikit.StartupInfo{
		Port:        9477,
		ConfigStale: true,
	}
	header, _ := RenderHeader(info, false, 80, -1, -1, false)
	assert.Contains(t, header, "runwisp.toml changed")
	assert.Contains(t, header, "press R to reload")
}

func TestRenderHeader_ConfigFresh_NoNotice(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	header, _ := RenderHeader(info, false, 80, -1, -1, false)
	assert.NotContains(t, header, "runwisp.toml changed")
}

func TestRenderHeader_FieldsStartY(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	_, fieldsStartY := RenderHeader(info, false, 80, -1, -1, false)
	assert.True(t, fieldsStartY >= 4, "fieldsStartY should be >= 4, got %d", fieldsStartY)
}

func TestRenderHeader_Hovered(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	header, _ := RenderHeader(info, false, 80, -1, 0, false)
	assert.NotEmpty(t, header)
}

func TestRenderHeader_LaunchTicketRendersOpenWebUIAction(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	// hasLaunchTicket=true puts FieldOpenWebUI first; cursor 0 = selected,
	// hover 0 = (ignored because selected wins). Exercises renderActionRow's
	// selected branch.
	header, _ := RenderHeader(info, true, 80, 0, -1, false)
	assert.Contains(t, header, "Open Web UI")
}

func TestRenderHeader_LaunchTicketHoveredOpenWebUI(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477}
	// cursor=-1, hover=0 exercises renderActionRow's hovered (not selected) branch.
	header, _ := RenderHeader(info, true, 80, -1, 0, false)
	assert.Contains(t, header, "Open Web UI")
}

func TestHeldTaskCount(t *testing.T) {
	assert.Zero(t, HeldTaskCount(nil))
	assert.Zero(t, HeldTaskCount([]model.Task{{Name: "native"}}))
	assert.Equal(t, 2, HeldTaskCount([]model.Task{
		{Name: "backup", HeldBy: model.HeldByCron},
		{Name: "native"},
		{Name: "vacuum", HeldBy: model.HeldByCron},
	}))
}

// The TUI switches to the alt-screen buffer on start, wiping the boot banner, so
// this chip is the only place a held job is visible from the home view.
func TestRenderHeader_ShowsHeldChip(t *testing.T) {
	info := uikit.StartupInfo{
		Port:  9477,
		Tasks: []model.Task{{Name: "backup", Cron: "0 3 * * *", HeldBy: model.HeldByCron}},
	}
	out, _ := RenderHeader(info, false, 100, -1, -1, false)
	assert.Contains(t, out, "1 held by cron")
	assert.Contains(t, out, "runwisp takeover")
}

func TestRenderHeader_NoHeldChipWhenNothingIsHeld(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477, Tasks: []model.Task{{Name: "backup", Cron: "0 3 * * *"}}}
	out, _ := RenderHeader(info, false, 100, -1, -1, false)
	assert.NotContains(t, out, "held by cron")
}

// A "Next:" time on a held task is a lie: that tick comes and goes without
// RunWisp firing anything, because the scheduler stood down for cron.
func TestRenderTaskHeader_HeldTaskReplacesNextRunWithTheReason(t *testing.T) {
	task := &model.Task{Kind: model.KindTask, Cron: "*/5 * * * *", HeldBy: model.HeldByCron}
	out, _ := TaskHeader{Name: "backup", Task: task}.Render(100)
	assert.Contains(t, out, "*/5 * * * *", "the schedule is still real and still shown")
	assert.Contains(t, out, "held")
	assert.Contains(t, out, "cron still owns this job")
	assert.NotContains(t, out, "Next:")
}

// A paused schedule has no next run; the header says so and names the key.
func TestRenderTaskHeader_PausedTaskReplacesNextRun(t *testing.T) {
	task := &model.Task{Kind: model.KindTask, Cron: "*/5 * * * *", ManualTrigger: true}
	out, _ := TaskHeader{Name: "backup", Task: task, Paused: true}.Render(100)
	assert.Contains(t, out, "*/5 * * * *")
	assert.Contains(t, out, "⏸ paused")
	assert.Contains(t, out, "Resume (p)")
	assert.NotContains(t, out, "Next:")
}

func TestRenderTaskHeader_ShowsLiveUsage(t *testing.T) {
	task := &model.Task{Kind: model.KindService, Instances: 1}
	out, _ := TaskHeader{Name: "web", Task: task, Usage: &model.ResourceUsage{CPUPercent: 150, MemoryBytes: 1 << 30}}.Render(100)
	assert.Contains(t, out, "CPU 150% · 1 GB")

	out, _ = TaskHeader{Name: "web", Task: task}.Render(100)
	assert.NotContains(t, out, "CPU ", "no usage line while nothing runs")
}

func TestRenderHeader_ShowsPausedChip(t *testing.T) {
	info := uikit.StartupInfo{Port: 9477, PausedTasks: map[string]time.Time{"backup": time.Now()}}
	out, _ := RenderHeader(info, false, 100, -1, -1, false)
	assert.Contains(t, out, "1 schedule paused")
}

// Task-level actions live on the header, so a stop or pause is findable
// without opening a run.
func TestTaskHeader_ButtonsFollowTaskState(t *testing.T) {
	svc := &model.Task{Kind: model.KindService, ManualTrigger: true}
	locked := &model.Task{Kind: model.KindService}
	cron := &model.Task{Kind: model.KindTask, Cron: "0 3 * * *", ManualTrigger: true}
	held := &model.Task{Kind: model.KindTask, Cron: "0 3 * * *", ManualTrigger: true, HeldBy: model.HeldByCron}
	tests := []struct {
		name   string
		h      TaskHeader
		labels []string
		hints  string
	}{
		{"running service", TaskHeader{Task: svc}, []string{"↻ Restart (r)", "■ Stop (s)"}, "r restart  s stop"},
		{"stopped service", TaskHeader{Task: svc, Stopped: true}, []string{"▶ Start (r)"}, "r start"},
		{"locked service", TaskHeader{Task: locked}, []string{"↻ Restart (r)"}, "r restart"},
		{"cron task", TaskHeader{Task: cron}, []string{"▶ Run Now (r)", "⏸ Pause (p)"}, "r run now  p pause"},
		{"paused task", TaskHeader{Task: cron, Paused: true}, []string{"▶ Run Now (r)", "▶ Resume (p)"}, "r run now  p resume"},
		{"held task", TaskHeader{Task: held}, []string{"▶ Run Now (r)"}, "r run now"},
		{"held and paused", TaskHeader{Task: held, Paused: true}, []string{"▶ Run Now (r)", "▶ Resume (p)"}, "r run now  p resume"},
		{"manual task", TaskHeader{Task: &model.Task{ManualTrigger: true}}, []string{"▶ Run Now (r)"}, "r run now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var labels []string
			for _, b := range tt.h.buttons() {
				labels = append(labels, b.label)
			}
			assert.Equal(t, tt.labels, labels)
			assert.Equal(t, tt.hints, strings.Join(tt.h.Hints(), "  "))
			out, _ := tt.h.Render(80)
			for _, l := range tt.labels {
				assert.Contains(t, out, l)
			}
		})
	}
}

func TestTaskHeader_ButtonAtMatchesRenderedColumns(t *testing.T) {
	h := TaskHeader{Name: "web", Task: &model.Task{Kind: model.KindService, ManualTrigger: true}}
	const w = 52 // an 80-column terminal minus the sidebar
	out, btnY := h.Render(w)
	row := ansi.Strip(strings.Split(out, "\n")[btnY])
	assert.Equal(t, w, ansi.StringWidth(row), "the buttons fit an 80-column terminal")
	for label, want := range map[string]TaskButton{"Restart": TaskButtonRun, "Stop": TaskButtonStop} {
		x := ansi.StringWidth(row[:strings.Index(row, label)])
		assert.Equal(t, want, h.ButtonAt(x, w), label)
	}
	assert.Equal(t, TaskButtonNone, h.ButtonAt(3, w), "the task name is not a button")
	assert.Equal(t, TaskButtonNone, h.ButtonAt(w-1, w), "the right margin is not a button")
}

func TestNextCronRun_ShownInGivenZone(t *testing.T) {
	loc := time.FixedZone("CEST", 2*3600)
	result := NextCronRun("15 3 * * *", loc)
	if !strings.HasPrefix(result, "03:15:00 (in ") {
		t.Fatalf("next run should read 03:15:00 in the task's zone, got %q", result)
	}
}

func TestRenderHeader_StarButton(t *testing.T) {
	for _, w := range []int{80, 17, 16} {
		header, _ := RenderHeader(uikit.StartupInfo{Port: 9477}, false, w, -1, -1, false)
		lines := strings.Split(header, "\n")
		assert.Equal(t, w, lipgloss.Width(lines[StarButtonY]), "width %d", w)
		from, to, ok := starButtonSpan(w)
		assert.Equal(t, w >= 17, ok, "width %d", w)
		assert.Equal(t, ok, strings.Contains(lines[StarButtonY], starButtonLabel), "width %d", w)
		assert.Equal(t, ok, StarButtonAt(from, w), "width %d", w)
		assert.False(t, StarButtonAt(to, w), "width %d", w)
	}
}
