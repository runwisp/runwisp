// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package info

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/sparkline"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

const (
	sparklineWidth    = 24
	sparklineHeight   = 2
	maxHistorySamples = 30
)

// InfoView displays live system information, metrics, and configuration.
type InfoView struct {
	info   uikit.StartupInfo
	width  int
	height int
	scroll int

	stats      *model.SystemStats
	runSummary *model.RunSummary

	cpuHistory []float64
	memHistory []float64

	cpuSparkline sparkline.Model
	memSparkline sparkline.Model

	contentHeight int
}

func NewInfoView(info uikit.StartupInfo) InfoView {
	cpuStyle := lipgloss.NewStyle().Background(uikit.ColorChartBg).Foreground(uikit.ColorRunning)
	memStyle := lipgloss.NewStyle().Background(uikit.ColorChartBg).Foreground(uikit.ColorSecondary)

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
func (v *InfoView) UpdateRunSummary(summary *model.RunSummary) {
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

	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))
	v.contentHeight = len(lines)

	if v.scroll > 0 && v.scroll < len(lines) {
		lines = lines[v.scroll:]
	}
	if len(lines) > v.height {
		lines = lines[:v.height]
	}
	for len(lines) < v.height {
		lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))
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

func (v *InfoView) renderHealthSection(w int) []string {
	var lines []string

	lines = append(lines, uikit.PadLine("", w, uikit.ColorBgLight))

	title := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextBright).
		Bold(true).
		Render("  System")
	sub := ""
	if v.stats != nil {
		sub = lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorTextMuted).
			Render("  ·  up " + v.stats.Uptime)
	}
	lines = append(lines, uikit.PadLine(title+sub, w, uikit.ColorBgLight))

	hostLine := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Render("  " + runtime.GOOS + "/" + runtime.GOARCH)
	if v.stats != nil && v.stats.Host != "" {
		hostLine += lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorTextMuted).
			Render("  ·  " + v.stats.Host)
	}
	lines = append(lines, uikit.PadLine(hostLine, w, uikit.ColorBgLight))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBgLight))

	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

	if v.stats != nil {
		cpuLines := v.renderSparklineRow(w,
			"CPU", fmt.Sprintf("%.1f%%", v.stats.CPUUsage),
			fmt.Sprintf("%d cores", v.stats.CPUCores),
			v.cpuSparkline, uikit.ColorRunning)
		lines = append(lines, cpuLines...)

		lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

		memLines := v.renderSparklineRow(w,
			"MEM", fmt.Sprintf("%.1f%%", v.stats.MemUsage),
			formatBytes(v.stats.MemUsed)+"/"+formatBytes(v.stats.MemTotal),
			v.memSparkline, uikit.ColorSecondary)
		lines = append(lines, memLines...)
	} else {
		waiting := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorTextMuted).Render("  Loading metrics...")
		lines = append(lines, uikit.PadLine(waiting, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) renderSparklineRow(w int, label, pct, detail string, sl sparkline.Model, color lipgloss.Color) []string {
	var lines []string

	bgStyle := lipgloss.NewStyle().Background(uikit.ColorBg)
	chartBg := lipgloss.NewStyle().Background(uikit.ColorChartBg)

	labelStr := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(color).Bold(true).Render("  " + label + " ")
	pctStr := uikit.InfoStatValueStyle.Render(pct)
	detailStr := uikit.InfoStatLabelStyle.Render("  " + detail)

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
				lines = append(lines, uikit.PadLine(line, w, uikit.ColorBg))
			} else {
				indent := bgStyle.Render(strings.Repeat(" ", headerWidth+gap))
				line := indent + sline
				lines = append(lines, uikit.PadLine(line, w, uikit.ColorBg))
			}
		}
	} else {
		lines = append(lines, uikit.PadLine(headerLeft, w, uikit.ColorBg))
		for _, sline := range sparkLines {
			lines = append(lines, uikit.PadLine(bgStyle.Render("  ")+sline, w, uikit.ColorBg))
		}
	}

	return lines
}

func (v *InfoView) renderActivitySection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render("Activity"), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

	s := v.runSummary
	parts := []string{
		uikit.InfoStatValueStyle.Render(fmt.Sprintf("%d", s.Total)) + uikit.InfoStatLabelStyle.Render(" runs"),
	}
	if s.Total > 0 {
		successStr := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorSuccess).Bold(true).Render(fmt.Sprintf("%d", s.Success))
		failedStr := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorError).Bold(true).Render(fmt.Sprintf("%d", s.Failed))
		parts = append(parts, successStr+uikit.InfoStatLabelStyle.Render(" success"))
		parts = append(parts, failedStr+uikit.InfoStatLabelStyle.Render(" failed"))

		rate := float64(s.Success) / float64(s.Total) * 100
		rateColor := uikit.ColorSuccess
		if rate < 90 {
			rateColor = uikit.ColorWarning
		}
		if rate < 70 {
			rateColor = uikit.ColorError
		}
		rateStr := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(rateColor).Render(fmt.Sprintf("%.1f%%", rate))
		parts = append(parts, rateStr)
	}
	sep := uikit.InfoStatLabelStyle.Render("  ·  ")
	line := bgSpace(2) + strings.Join(parts, sep)
	lines = append(lines, uikit.PadLine(line, w, uikit.ColorBg))

	if s.LastFailure != nil {
		failLine := bgSpace(2) + uikit.InfoStatLabelStyle.Render("Last failure  ") +
			lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorTextMuted).Render(s.LastFailure.Format(time.RFC3339))
		lines = append(lines, uikit.PadLine(failLine, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) renderQuickInfoSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render("Info"), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

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
		label := uikit.InfoLabelStyle.Render(f.label)
		value := uikit.InfoValueStyle.Render(f.value)
		lines = append(lines, uikit.PadLine(bgSpace(2)+label+value, w, uikit.ColorBg))
	}

	if len(v.info.Capabilities) > 0 {
		lines = append(lines, v.renderCapabilitiesLines(v.info.Capabilities, w)...)
	}

	return lines
}

func (v *InfoView) renderCapabilitiesLines(caps []model.CapInfo, w int) []string {
	var lines []string
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))
	var capStrs []string
	for _, cap := range caps {
		if cap.Available {
			capStrs = append(capStrs, uikit.InfoCapsAvailableStyle.Render("✓ "+cap.Name))
		} else {
			capStrs = append(capStrs, uikit.InfoCapsUnavailableStyle.Render("✗ "+cap.Name))
		}
	}
	capLine := bgSpace(2) + strings.Join(capStrs, bgSpace(2))
	if lipgloss.Width(capLine) > w-2 {
		for _, c := range capStrs {
			lines = append(lines, uikit.PadLine(bgSpace(2)+c, w, uikit.ColorBg))
		}
	} else {
		lines = append(lines, uikit.PadLine(capLine, w, uikit.ColorBg))
	}
	return lines
}

func (v *InfoView) renderConfigSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render("Configuration"), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

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
		label := uikit.InfoLabelStyle.Render(f.label)
		value := uikit.InfoValueStyle.Render(f.value)
		lines = append(lines, uikit.PadLine(bgSpace(2)+label+value, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) renderTasksSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render(fmt.Sprintf("Tasks (%d)", len(v.info.Tasks))), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

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
		name := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorTextBright).Bold(true).Render(task.Name)
		schedStyle := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorTextMuted).Render(sched)
		lines = append(lines, uikit.PadLine(bgSpace(2)+name+bgSpace(2)+schedStyle, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) renderWarningsSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render("Warnings"), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

	for _, warn := range v.info.ScheduleWarnings {
		warnStyle := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorWarning).Render("⚠ " + warn)
		lines = append(lines, uikit.PadLine(bgSpace(2)+warnStyle, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) sectionDivider(w int) []string {
	divider := uikit.InfoDividerStyle.Render("  " + strings.Repeat("─", w-4))
	return []string{
		uikit.PadLine("", w, uikit.ColorBg),
		uikit.PadLine(divider, w, uikit.ColorBg),
		uikit.PadLine("", w, uikit.ColorBg),
	}
}

func bgSpace(n int) string {
	return lipgloss.NewStyle().Background(uikit.ColorBg).Render(strings.Repeat(" ", n))
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
