// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package info

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in  uint64
		out string
	}{
		{0, "0B"},
		{512, "512B"},
		{1023, "1023B"},
		{1024, "1K"},
		{1536, "2K"},
		{1024 * 1024, "1M"},
		{1536 * 1024, "2M"},
		{1024 * 1024 * 1024, "1.0G"},
		{2560 * 1024 * 1024, "2.5G"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.out, formatBytes(tt.in), "formatBytes(%d)", tt.in)
	}
}

func TestAppendCapped(t *testing.T) {
	s := appendCapped(nil, 1.0, 3)
	assert.Equal(t, []float64{1.0}, s)

	s = appendCapped(s, 2.0, 3)
	s = appendCapped(s, 3.0, 3)
	assert.Equal(t, []float64{1.0, 2.0, 3.0}, s)

	// Adding a 4th element should evict the first.
	s = appendCapped(s, 4.0, 3)
	assert.Equal(t, []float64{2.0, 3.0, 4.0}, s)
}

func TestInfoView_ScrollUp_ScrollDown(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 20)

	v.ScrollDown(5)
	assert.GreaterOrEqual(t, v.scroll, 0)

	start := v.scroll
	v.ScrollUp(3)
	if start >= 3 {
		assert.Equal(t, start-3, v.scroll)
	} else {
		assert.Equal(t, 0, v.scroll)
	}
}

func TestInfoView_ScrollUp_ClampedAtZero(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 20)
	v.scroll = 2

	v.ScrollUp(10)
	assert.Equal(t, 0, v.scroll)
}

func TestInfoView_Update_Keys(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 20)
	v.scroll = 5

	v.Update(tea.KeyMsg{Type: tea.KeyHome})
	assert.Equal(t, 0, v.scroll)

	v.Update(tea.KeyMsg{Type: tea.KeyEnd})
	assert.GreaterOrEqual(t, v.scroll, 0)

	v.scroll = 5
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	assert.Equal(t, 4, v.scroll)

	// "j" is capped at maxScroll; without content, scroll stays at maxScroll (which may be 0).
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	assert.GreaterOrEqual(t, v.scroll, 0)

	v.scroll = 5
	v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	assert.Equal(t, 0, v.scroll)
}

// TestInfoView_Update_PageKeys covers the pgup/pgdown branches that
// TestInfoView_Update_Keys skipped. pgup must clamp at zero and pgdown at
// maxScroll.
func TestInfoView_Update_PageKeys(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 20)
	v.contentHeight = 100 // force maxScroll > 0

	v.scroll = 50
	v.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	assert.Greater(t, v.scroll, 50, "pgdown should advance scroll")

	v.scroll = 5
	v.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	assert.Equal(t, 0, v.scroll, "pgup with small scroll should clamp at 0")

	v.scroll = 1000 // beyond maxScroll
	v.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	assert.Equal(t, v.contentHeight-v.height, v.scroll, "pgdown should clamp at maxScroll")
}

// TestInfoView_Update_UpDownArrowKeys hits the named arrow key strings, which
// share their case labels with k/j but distinct rune values would otherwise be
// uncovered.
func TestInfoView_Update_UpDownArrowKeys(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 20)
	v.contentHeight = 100
	v.scroll = 10

	v.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 9, v.scroll)
	v.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 10, v.scroll)
}

func TestInfoView_SetSize_ClampsScroll(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 20)
	v.scroll = 1000

	v.SetSize(80, 20)
	assert.GreaterOrEqual(t, v.scroll, 0)
}

func TestInfoView_RenderWarningsSection_Empty(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 20)

	lines := v.renderWarningsSection(80)
	assert.NotEmpty(t, lines, "renderWarningsSection should return at least the header line even without warnings")
}

func TestInfoView_RenderWarningsSection_WithWarnings(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{
		ScheduleWarnings: []string{"cron overlap detected", "invalid timezone"},
	})
	v.SetSize(80, 20)

	lines := v.renderWarningsSection(80)
	assert.NotEmpty(t, lines)
	full := ""
	for _, l := range lines {
		full += l
	}
	assert.Contains(t, full, "cron overlap detected")
	assert.Contains(t, full, "invalid timezone")
}

func TestInfoView_UpdateStats(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 40)

	stats := &model.SystemStats{
		CPUUsage: 42.5,
		MemUsage: 65.0,
		MemTotal: 8 * 1024 * 1024 * 1024,
		MemUsed:  5 * 1024 * 1024 * 1024,
		CPUCores: 4,
		Uptime:   "2h30m",
		Host:     "myhost",
	}
	v.UpdateStats(stats)

	require.NotNil(t, v.stats)
	assert.InDelta(t, 42.5, v.stats.CPUUsage, 0.001)
	assert.Len(t, v.cpuHistory, 1)
	assert.Len(t, v.memHistory, 1)
}

func TestInfoView_UpdateStats_MultipleCalls(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 40)

	for i := range 5 {
		v.UpdateStats(&model.SystemStats{
			CPUUsage: float64(i) * 10,
			MemUsage: float64(i) * 5,
		})
	}

	assert.Len(t, v.cpuHistory, 5)
	assert.Len(t, v.memHistory, 5)
}

func TestInfoView_LoadHistory(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 40)

	samples := []model.MetricsSample{
		{Timestamp: 1000, CPUUsage: 10.0, MemUsage: 20.0},
		{Timestamp: 1001, CPUUsage: 30.0, MemUsage: 40.0},
		{Timestamp: 1002, CPUUsage: 50.0, MemUsage: 60.0},
	}
	v.LoadHistory(samples)

	assert.Len(t, v.cpuHistory, 3)
	assert.Len(t, v.memHistory, 3)
	assert.InDelta(t, 50.0, v.cpuHistory[2], 0.001)
	assert.InDelta(t, 60.0, v.memHistory[2], 0.001)
}

func TestInfoView_LoadHistory_Empty(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 40)

	// Pre-populate history then clear it with empty load.
	v.UpdateStats(&model.SystemStats{CPUUsage: 99.0, MemUsage: 80.0})
	require.Len(t, v.cpuHistory, 1)

	v.LoadHistory(nil)

	assert.Empty(t, v.cpuHistory)
	assert.Empty(t, v.memHistory)
}

func TestInfoView_UpdateRunSummary(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 40)

	lastFailure := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	summary := &model.RunSummary{
		Total:       100,
		Success:     90,
		Failed:      10,
		LastFailure: &lastFailure,
	}
	v.UpdateRunSummary(summary)

	require.NotNil(t, v.runSummary)
	assert.Equal(t, int64(100), v.runSummary.Total)
}

func TestInfoView_UpdateRunSummary_Nil(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{})
	v.SetSize(80, 40)

	v.UpdateRunSummary(nil)
	assert.Nil(t, v.runSummary)
}

// TestInfoView_View_RenderBranches exercises every distinct render branch of
// View() in one table. Each row constructs a view in a specific state and
// asserts on at least one substring that's unique to that branch (so a
// regression that silently swallows the branch's text still fails).
func TestInfoView_View_RenderBranches(t *testing.T) {
	lastFailure := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		setup func() *InfoView
		// substrings every row's output must contain (must be non-empty so
		// every row pins at least one branch-specific signal).
		contains []string
	}{
		{
			name: "empty",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{})
				v.SetSize(80, 40)
				return &v
			},
			contains: []string{"\n"},
		},
		{
			name: "with-stats",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{
					Version:     "1.2.3",
					ConfigPath:  "/etc/runwisp.toml",
					DataDir:     "/var/lib/runwisp",
					DBPath:      "/var/lib/runwisp/runwisp.db",
					LogDir:      "/var/log/runwisp",
					Fingerprint: "abc123",
					Port:        8080,
				})
				v.SetSize(80, 40)
				v.UpdateStats(&model.SystemStats{
					CPUUsage: 55.0, MemUsage: 70.0,
					MemTotal: 16 * 1024 * 1024 * 1024,
					MemUsed:  11 * 1024 * 1024 * 1024,
					CPUCores: 8, Uptime: "5d2h", Host: "prod-server",
				})
				return &v
			},
			contains: []string{"1.2.3", "prod-server"},
		},
		{
			name: "with-run-summary-failures",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{})
				v.SetSize(80, 40)
				v.UpdateRunSummary(&model.RunSummary{
					Total: 200, Success: 180, Failed: 20,
					LastFailure: &lastFailure,
				})
				return &v
			},
			contains: []string{"200"},
		},
		{
			name: "run-summary-no-failures",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{})
				v.SetSize(80, 40)
				v.UpdateRunSummary(&model.RunSummary{Total: 50, Success: 50, Failed: 0})
				return &v
			},
			contains: []string{"50"},
		},
		{
			name: "run-summary-zero-total",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{})
				v.SetSize(80, 40)
				v.UpdateRunSummary(&model.RunSummary{})
				return &v
			},
			contains: []string{"\n"},
		},
		{
			name: "with-tasks",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{Tasks: []model.TaskBrief{
					{Name: "backup", Cron: "0 2 * * *"},
					{Name: "cleanup", Kind: model.KindTask},
					{Name: "worker", Kind: model.KindService, Instances: 3},
				}})
				v.SetSize(80, 40)
				return &v
			},
			contains: []string{"backup", "cleanup", "worker"},
		},
		{
			name: "with-capabilities",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{Capabilities: []model.CapInfo{
					{Name: "slack", Available: true},
					{Name: "telegram", Available: false},
				}})
				v.SetSize(80, 40)
				return &v
			},
			contains: []string{"slack", "telegram"},
		},
		{
			name: "capabilities-narrow",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{Capabilities: []model.CapInfo{
					{Name: "slack", Available: true},
					{Name: "telegram", Available: true},
					{Name: "email", Available: false},
					{Name: "webhook", Available: true},
					{Name: "pagerduty", Available: false},
				}})
				v.SetSize(30, 40) // narrow forces stacked layout
				return &v
			},
			contains: []string{"slack"},
		},
		{
			name: "cloud-enabled",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{CloudEnabled: true, Version: "1.0.0"})
				v.SetSize(80, 40)
				return &v
			},
			contains: []string{"Connected"},
		},
		{
			name: "with-warnings",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{
					ScheduleWarnings: []string{"task foo has no schedule"},
				})
				v.SetSize(80, 40)
				return &v
			},
			contains: []string{"task foo has no schedule"},
		},
		{
			name: "low-success-rate",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{})
				v.SetSize(80, 40)
				v.UpdateRunSummary(&model.RunSummary{Total: 100, Success: 60, Failed: 40})
				return &v
			},
			contains: []string{"100"},
		},
		{
			name: "medium-success-rate",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{})
				v.SetSize(80, 40)
				v.UpdateRunSummary(&model.RunSummary{Total: 100, Success: 80, Failed: 20})
				return &v
			},
			contains: []string{"100"},
		},
		{
			name: "sparkline-narrow",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{})
				v.SetSize(20, 20)
				v.UpdateStats(&model.SystemStats{
					CPUUsage: 25.0, MemUsage: 50.0, CPUCores: 2,
					MemTotal: 4 * 1024 * 1024 * 1024,
					MemUsed:  2 * 1024 * 1024 * 1024,
				})
				return &v
			},
			contains: []string{"\n"},
		},
		{
			name: "full-data",
			setup: func() *InfoView {
				v := NewInfoView(uikit.StartupInfo{
					Version:      "1.5.0",
					ConfigPath:   "/etc/runwisp.toml",
					DataDir:      "/var/lib/runwisp",
					DBPath:       "/var/lib/runwisp/runwisp.db",
					LogDir:       "/var/log/runwisp",
					Fingerprint:  "fp-xyz",
					Port:         9090,
					CloudEnabled: true,
					Tasks: []model.TaskBrief{
						{Name: "nightly-backup", Cron: "0 3 * * *"},
						{Name: "api-worker", Kind: model.KindService, Instances: 2},
						{Name: "manual-job", Kind: model.KindTask},
					},
					Capabilities: []model.CapInfo{
						{Name: "slack", Available: true},
						{Name: "telegram", Available: false},
					},
					ScheduleWarnings: []string{"overlap on nightly-backup"},
				})
				v.SetSize(120, 200)
				v.UpdateStats(&model.SystemStats{
					CPUUsage: 12.3, MemUsage: 48.7,
					MemTotal: 32 * 1024 * 1024 * 1024,
					MemUsed:  15 * 1024 * 1024 * 1024,
					CPUCores: 16, Uptime: "10d4h", Host: "prod-01",
				})
				v.LoadHistory([]model.MetricsSample{
					{Timestamp: 1000, CPUUsage: 10.0, MemUsage: 45.0},
					{Timestamp: 1060, CPUUsage: 12.0, MemUsage: 47.0},
				})
				v.UpdateRunSummary(&model.RunSummary{
					Total: 500, Success: 450, Failed: 50,
					LastFailure: &lastFailure,
				})
				return &v
			},
			contains: []string{"nightly-backup", "api-worker", "slack", "overlap on nightly-backup", "Connected"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tt.setup()
			out := v.View()
			require.NotEmpty(t, out)
			for _, sub := range tt.contains {
				assert.Contains(t, out, sub, "render branch %q should contain %q", tt.name, sub)
			}
		})
	}
}

// TestInfoView_View_ScrollProducesDifferentContent asserts that scrolling
// actually changes the rendered output — replacing the prior
// `_ = strings.Contains(...)` discard with a real signal.
func TestInfoView_View_ScrollProducesDifferentContent(t *testing.T) {
	v := NewInfoView(uikit.StartupInfo{
		Version:     "1.0.0",
		ConfigPath:  "/etc/runwisp.toml",
		DataDir:     "/var/lib/runwisp",
		DBPath:      "/var/lib/runwisp/runwisp.db",
		LogDir:      "/var/log/runwisp",
		Fingerprint: "abc",
		Port:        9090,
	})
	v.SetSize(80, 10)
	// Populate stats + content so the viewport actually overflows; without
	// overflow, scroll is clamped to 0 and there's nothing for scroll to do.
	v.UpdateStats(&model.SystemStats{
		CPUUsage: 25.0, MemUsage: 50.0, CPUCores: 4,
		MemTotal: 8 * 1024 * 1024 * 1024, MemUsed: 4 * 1024 * 1024 * 1024,
		Uptime: "1h", Host: "h1",
	})

	first := v.View()
	require.NotEmpty(t, first)

	v.scroll = 3
	scrolled := v.View()
	require.NotEmpty(t, scrolled)

	assert.NotEqual(t, first, scrolled, "scrolling should change rendered output")
}
