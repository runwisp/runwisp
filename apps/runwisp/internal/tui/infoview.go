// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/NimbleMarkets/ntcharts/sparkline"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
)

const (
	sparklineWidth    = 24
	sparklineHeight   = 2
	maxHistorySamples = 30
	infoFieldWidth    = 14
)

// InfoView displays live system information, metrics, and configuration.
type InfoView struct {
	info   StartupInfo
	width  int
	height int
	scroll int

	stats      *model.SystemStats
	runSummary *apiclient.RunSummary

	cpuHistory []float64
	memHistory []float64

	cpuSparkline sparkline.Model
	memSparkline sparkline.Model

	contentHeight int
}

func NewInfoView(info StartupInfo) InfoView {
	cpuStyle := lipgloss.NewStyle().Background(colorChartBg).Foreground(colorRunning)
	memStyle := lipgloss.NewStyle().Background(colorChartBg).Foreground(colorSecondary)

	cpuSL := sparkline.New(sparklineWidth, sparklineHeight,
		sparkline.WithMaxValue(100),
		sparkline.WithNoAutoMaxValue(),
		sparkline.WithStyle(cpuStyle),
	)
	memSL := sparkline.New(sparklineWidth, sparklineHeight,
		sparkline.WithMaxValue(100),
		sparkline.WithNoAutoMaxValue(),
		sparkline.WithStyle(memStyle),
	)

	return InfoView{
		info:         info,
		cpuSparkline: cpuSL,
		memSparkline: memSL,
	}
}

// SetSize updates dimensions and resets scroll if needed.
func (v *InfoView) SetSize(w, h int) {
	v.width = w
	v.height = h
	if v.scroll > v.maxScroll() {
		v.scroll = v.maxScroll()
	}
}

// UpdateStats pushes new metrics data and updates sparklines.
func (v *InfoView) UpdateStats(stats *model.SystemStats) {
	v.stats = stats
	v.cpuHistory = appendCapped(v.cpuHistory, stats.CPUUsage, maxHistorySamples)
	v.memHistory = appendCapped(v.memHistory, stats.MemUsage, maxHistorySamples)
	v.cpuSparkline.Push(stats.CPUUsage)
	v.cpuSparkline.DrawBraille()
	v.memSparkline.Push(stats.MemUsage)
	v.memSparkline.DrawBraille()
}

// LoadHistory pre-fills sparklines from historical samples (oldest first).
func (v *InfoView) LoadHistory(samples []model.MetricsSample) {
	v.cpuHistory = v.cpuHistory[:0]
	v.memHistory = v.memHistory[:0]
	v.cpuSparkline.Clear()
	v.memSparkline.Clear()
	cpuVals := make([]float64, 0, len(samples))
	memVals := make([]float64, 0, len(samples))
	for _, s := range samples {
		v.cpuHistory = appendCapped(v.cpuHistory, s.CPUUsage, maxHistorySamples)
		v.memHistory = appendCapped(v.memHistory, s.MemUsage, maxHistorySamples)
		cpuVals = append(cpuVals, s.CPUUsage)
		memVals = append(memVals, s.MemUsage)
	}
	v.cpuSparkline.PushAll(cpuVals)
	v.memSparkline.PushAll(memVals)
	v.cpuSparkline.DrawBraille()
	v.memSparkline.DrawBraille()
}

// UpdateRunSummary stores the latest run summary.
func (v *InfoView) UpdateRunSummary(summary *apiclient.RunSummary) {
	v.runSummary = summary
}

// Update handles keyboard input for scrolling.
func (v *InfoView) Update(msg tea.KeyMsg) {
	switch msg.String() {
	case "up", "k":
		if v.scroll > 0 {
			v.scroll--
		}
	case "down", "j":
		if v.scroll < v.maxScroll() {
			v.scroll++
		}
	case "pgup":
		v.scroll -= v.height / 2
		if v.scroll < 0 {
			v.scroll = 0
		}
	case "pgdown":
		v.scroll += v.height / 2
		if v.scroll > v.maxScroll() {
			v.scroll = v.maxScroll()
		}
	case "home", "g":
		v.scroll = 0
	case "end", "G":
		v.scroll = v.maxScroll()
	}
}

// ScrollUp scrolls up by n lines (for mouse wheel).
func (v *InfoView) ScrollUp(n int) {
	v.scroll -= n
	if v.scroll < 0 {
		v.scroll = 0
	}
}

// ScrollDown scrolls down by n lines (for mouse wheel).
func (v *InfoView) ScrollDown(n int) {
	v.scroll += n
	if v.scroll > v.maxScroll() {
		v.scroll = v.maxScroll()
	}
}

func (v *InfoView) View() string {
	w := v.width

	var lines []string

	lines = append(lines, v.renderHealthSection(w)...)

	if v.runSummary != nil {
		lines = append(lines, v.renderActivitySection(w)...)
	}

	lines = append(lines, v.renderQuickInfoSection(w)...)
	lines = append(lines, v.renderConfigSection(w)...)
	lines = append(lines, v.renderTasksSection(w)...)

	if len(v.info.ScheduleWarnings) > 0 {
		lines = append(lines, v.renderWarningsSection(w)...)
	}

	lines = append(lines, padLine("", w, colorBg))
	v.contentHeight = len(lines)

	// Apply scrolling.
	if v.scroll > 0 && v.scroll < len(lines) {
		lines = lines[v.scroll:]
	}

	// Truncate to viewport.
	if len(lines) > v.height {
		lines = lines[:v.height]
	}

	// Pad to fill remaining height.
	for len(lines) < v.height {
		lines = append(lines, padLine("", w, colorBg))
	}

	return strings.Join(lines, "\n")
}

func (v *InfoView) maxScroll() int {
	max := v.contentHeight - v.height
	if max < 0 {
		return 0
	}
	return max
}

// ── Render Sections ──

func (v *InfoView) renderHealthSection(w int) []string {
	var lines []string

	lines = append(lines, padLine("", w, colorBgLight))

	title := lipgloss.NewStyle().
		Background(colorBgLight).
		Foreground(colorTextBright).
		Bold(true).
		Render("  System")
	sub := ""
	if v.stats != nil {
		sub = lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorTextMuted).
			Render("  ·  up " + v.stats.Uptime)
	}
	lines = append(lines, padLine(title+sub, w, colorBgLight))

	hostLine := lipgloss.NewStyle().
		Background(colorBgLight).
		Foreground(colorTextMuted).
		Render("  " + runtime.GOOS + "/" + runtime.GOARCH)
	if v.stats != nil && v.stats.Host != "" {
		hostLine += lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorTextMuted).
			Render("  ·  " + v.stats.Host)
	}
	lines = append(lines, padLine(hostLine, w, colorBgLight))
	lines = append(lines, padLine("", w, colorBgLight))

	lines = append(lines, padLine("", w, colorBg))

	if v.stats != nil {
		cpuLines := v.renderSparklineRow(w,
			"CPU", fmt.Sprintf("%.1f%%", v.stats.CPUUsage),
			fmt.Sprintf("%d cores", v.stats.CPUCores),
			v.cpuSparkline, colorRunning)
		lines = append(lines, cpuLines...)

		lines = append(lines, padLine("", w, colorBg))

		memLines := v.renderSparklineRow(w,
			"MEM", fmt.Sprintf("%.1f%%", v.stats.MemUsage),
			formatBytes(v.stats.MemUsed)+"/"+formatBytes(v.stats.MemTotal),
			v.memSparkline, colorSecondary)
		lines = append(lines, memLines...)
	} else {
		waiting := lipgloss.NewStyle().Background(colorBg).Foreground(colorTextMuted).Render("  Loading metrics...")
		lines = append(lines, padLine(waiting, w, colorBg))
	}

	return lines
}

func (v *InfoView) renderSparklineRow(w int, label, pct, detail string, sl sparkline.Model, color lipgloss.Color) []string {
	var lines []string

	bgStyle := lipgloss.NewStyle().Background(colorBg)
	chartBg := lipgloss.NewStyle().Background(colorChartBg)

	labelStr := lipgloss.NewStyle().Background(colorBg).Foreground(color).Bold(true).Render("  " + label + " ")
	pctStr := infoStatValueStyle.Render(pct)
	detailStr := infoStatLabelStyle.Render("  " + detail)

	sparkView := sl.View()
	// Re-style each sparkline cell row to force our chart background on empty cells.
	sparkLines := strings.Split(sparkView, "\n")
	for i, sline := range sparkLines {
		sparkLines[i] = chartBg.Render(sline)
	}

	headerLeft := labelStr + pctStr + detailStr
	headerWidth := lipgloss.Width(headerLeft)

	if headerWidth+sparklineWidth+4 <= w {
		gap := w - headerWidth - sparklineWidth - 2
		if gap < 2 {
			gap = 2
		}
		gapStr := bgStyle.Render(strings.Repeat(" ", gap))

		for i, sline := range sparkLines {
			if i == 0 {
				line := headerLeft + gapStr + sline
				lines = append(lines, padLine(line, w, colorBg))
			} else {
				indent := bgStyle.Render(strings.Repeat(" ", headerWidth+gap))
				line := indent + sline
				lines = append(lines, padLine(line, w, colorBg))
			}
		}
	} else {
		lines = append(lines, padLine(headerLeft, w, colorBg))
		for _, sline := range sparkLines {
			lines = append(lines, padLine(bgStyle.Render("  ")+sline, w, colorBg))
		}
	}

	return lines
}

func (v *InfoView) renderActivitySection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, padLine(infoSectionStyle.Render("Activity"), w, colorBg))
	lines = append(lines, padLine("", w, colorBg))

	s := v.runSummary
	parts := []string{
		infoStatValueStyle.Render(fmt.Sprintf("%d", s.Total)) + infoStatLabelStyle.Render(" runs"),
	}
	if s.Total > 0 {
		successStr := lipgloss.NewStyle().Background(colorBg).Foreground(colorSuccess).Bold(true).Render(fmt.Sprintf("%d", s.Success))
		failedStr := lipgloss.NewStyle().Background(colorBg).Foreground(colorError).Bold(true).Render(fmt.Sprintf("%d", s.Failed))
		parts = append(parts, successStr+infoStatLabelStyle.Render(" success"))
		parts = append(parts, failedStr+infoStatLabelStyle.Render(" failed"))

		rate := float64(s.Success) / float64(s.Total) * 100
		rateColor := colorSuccess
		if rate < 90 {
			rateColor = colorWarning
		}
		if rate < 70 {
			rateColor = colorError
		}
		rateStr := lipgloss.NewStyle().Background(colorBg).Foreground(rateColor).Render(fmt.Sprintf("%.1f%%", rate))
		parts = append(parts, rateStr)
	}
	sep := infoStatLabelStyle.Render("  ·  ")
	line := bgSpace(2) + strings.Join(parts, sep)
	lines = append(lines, padLine(line, w, colorBg))

	if s.LastFailure != nil {
		failLine := bgSpace(2) + infoStatLabelStyle.Render("Last failure  ") +
			lipgloss.NewStyle().Background(colorBg).Foreground(colorTextMuted).Render(*s.LastFailure)
		lines = append(lines, padLine(failLine, w, colorBg))
	}

	return lines
}

func (v *InfoView) renderQuickInfoSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, padLine(infoSectionStyle.Render("Info"), w, colorBg))
	lines = append(lines, padLine("", w, colorBg))

	type kv struct{ label, value string }
	fields := []kv{
		{"Version", v.info.Version},
		{"Platform", runtime.GOOS + "/" + runtime.GOARCH},
	}
	if v.info.Port > 0 {
		fields = append(fields, kv{"Web UI", fmt.Sprintf("http://localhost:%d", v.info.Port)})
	}
	if v.info.CloudEnabled {
		fields = append(fields, kv{"Cloud", "Connected"})
	}

	for _, f := range fields {
		if f.value == "" {
			continue
		}
		label := infoLabelStyle.Render(f.label)
		value := infoValueStyle.Render(f.value)
		lines = append(lines, padLine(bgSpace(2)+label+value, w, colorBg))
	}

	if len(v.info.Capabilities) > 0 {
		lines = append(lines, padLine("", w, colorBg))
		var caps []string
		for _, cap := range v.info.Capabilities {
			if cap.Available {
				caps = append(caps, infoCapsAvailableStyle.Render("✓ "+cap.Name))
			} else {
				caps = append(caps, infoCapsUnavailableStyle.Render("✗ "+cap.Name))
			}
		}
		capLine := bgSpace(2) + strings.Join(caps, bgSpace(2))
		if lipgloss.Width(capLine) > w-2 {
			for _, c := range caps {
				lines = append(lines, padLine(bgSpace(2)+c, w, colorBg))
			}
		} else {
			lines = append(lines, padLine(capLine, w, colorBg))
		}
	}

	return lines
}

func (v *InfoView) renderConfigSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, padLine(infoSectionStyle.Render("Configuration"), w, colorBg))
	lines = append(lines, padLine("", w, colorBg))

	type kv struct{ label, value string }
	fields := []kv{
		{"Config", v.info.ConfigPath},
		{"Data Dir", v.info.DataDir},
		{"Database", v.info.DBPath},
		{"Log Dir", v.info.LogDir},
		{"Fingerprint", v.info.Fingerprint},
	}

	for _, f := range fields {
		if f.value == "" {
			continue
		}
		label := infoLabelStyle.Render(f.label)
		value := infoValueStyle.Render(f.value)
		lines = append(lines, padLine(bgSpace(2)+label+value, w, colorBg))
	}

	return lines
}

func (v *InfoView) renderTasksSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, padLine(infoSectionStyle.Render(fmt.Sprintf("Tasks (%d)", len(v.info.Tasks))), w, colorBg))
	lines = append(lines, padLine("", w, colorBg))

	for _, task := range v.info.Tasks {
		var sched string
		switch {
		case task.Kind.IsService():
			sched = fmt.Sprintf("service x%d", task.Instances)
		case task.Cron != "":
			sched = task.Cron
		default:
			sched = "manual"
		}
		name := lipgloss.NewStyle().Background(colorBg).Foreground(colorTextBright).Bold(true).Render(task.Name)
		schedStyle := lipgloss.NewStyle().Background(colorBg).Foreground(colorTextMuted).Render(sched)
		lines = append(lines, padLine(bgSpace(2)+name+bgSpace(2)+schedStyle, w, colorBg))
	}

	return lines
}

func (v *InfoView) renderWarningsSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, padLine(infoSectionStyle.Render("Warnings"), w, colorBg))
	lines = append(lines, padLine("", w, colorBg))

	for _, warn := range v.info.ScheduleWarnings {
		warnStyle := lipgloss.NewStyle().Background(colorBg).Foreground(colorWarning).Render("⚠ " + warn)
		lines = append(lines, padLine(bgSpace(2)+warnStyle, w, colorBg))
	}

	return lines
}

// ── Helpers ──

func (v *InfoView) sectionDivider(w int) []string {
	divider := infoDividerStyle.Render("  " + strings.Repeat("─", w-4))
	return []string{
		padLine("", w, colorBg),
		padLine(divider, w, colorBg),
		padLine("", w, colorBg),
	}
}

func bgSpace(n int) string {
	return lipgloss.NewStyle().Background(colorBg).Render(strings.Repeat(" ", n))
}

func appendCapped(s []float64, val float64, max int) []float64 {
	s = append(s, val)
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}

func formatBytes(b uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fG", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.0fM", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.0fK", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%dB", b)
	}
}
