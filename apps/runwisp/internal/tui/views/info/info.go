// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package info

import (
	"fmt"
	"image/color"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

const (
	sparklineWidth    = 24
	maxHistorySamples = 30
)

// sparklineBlocks are the block-height levels used to render a sparkline from
// a 0-100 value range, lowest to highest.
var sparklineBlocks = []rune("▁▂▃▄▅▆▇█")

// InfoView displays live system information, metrics, and configuration.
type InfoView struct {
	info   uikit.StartupInfo
	loc    *time.Location // the daemon's timezone
	width  int
	height int
	scroll int

	stats      *model.SystemStats
	runSummary *model.RunSummary
	taskUsage  map[string]model.ResourceUsage

	cpuHistory []float64
	memHistory []float64

	contentHeight int
}

func NewInfoView(info uikit.StartupInfo) InfoView {
	return InfoView{info: info, loc: uikit.ResolveLocation(info.Timezone)}
}

// SetSize updates dimensions and resets scroll if needed.
func (v *InfoView) SetSize(w, h int) {
	v.width = w
	v.height = h
	v.scroll = min(v.scroll, v.maxScroll())
}

// UpdateStats pushes new metrics data and updates sparklines.
func (v *InfoView) UpdateStats(stats *model.SystemStats) {
	v.stats = stats
	v.cpuHistory = appendCapped(v.cpuHistory, stats.CPUUsage)
	v.memHistory = appendCapped(v.memHistory, stats.MemUsage)
}

// SetTaskUsage replaces the live per-task CPU and memory shown in the task list.
func (v *InfoView) SetTaskUsage(usage map[string]model.ResourceUsage) {
	v.taskUsage = usage
}

// LoadHistory pre-fills sparklines from historical samples (oldest first).
func (v *InfoView) LoadHistory(samples []model.MetricsSample) {
	v.cpuHistory = v.cpuHistory[:0]
	v.memHistory = v.memHistory[:0]
	for _, s := range samples {
		v.cpuHistory = appendCapped(v.cpuHistory, s.CPUUsage)
		v.memHistory = appendCapped(v.memHistory, s.MemUsage)
	}
}

// UpdateRunSummary stores the latest run summary.
func (v *InfoView) UpdateRunSummary(summary *model.RunSummary) {
	v.runSummary = summary
}

// Update handles keyboard input for scrolling.
func (v *InfoView) Update(msg tea.KeyPressMsg) {
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
		v.scroll = max(v.scroll, 0)
	case "pgdown":
		v.scroll += v.height / 2
		v.scroll = min(v.scroll, v.maxScroll())
	case "home", "g":
		v.scroll = 0
	case "end", "G":
		v.scroll = v.maxScroll()
	}
}

// ScrollUp scrolls up by n lines (for mouse wheel).
func (v *InfoView) ScrollUp(n int) {
	v.scroll -= n
	v.scroll = max(v.scroll, 0)
}

// ScrollDown scrolls down by n lines (for mouse wheel).
func (v *InfoView) ScrollDown(n int) {
	v.scroll += n
	v.scroll = min(v.scroll, v.maxScroll())
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
	return max(v.contentHeight-v.height, 0)
}

func (v *InfoView) renderHealthSection(w int) []string {
	var lines []string

	lines = append(lines, uikit.PadLine("", w, uikit.ColorBgLight))

	title := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextBright).
		Bold(true).
		Render("  System")
	sub := ""
	if v.stats != nil {
		sub = uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted).
			Render("  ·  up " + v.stats.Uptime)
	}
	lines = append(lines, uikit.PadLine(title+sub, w, uikit.ColorBgLight))

	hostLine := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted).
		Render("  " + runtime.GOOS + "/" + runtime.GOARCH)
	if v.stats != nil && v.stats.Host != "" {
		hostLine += uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted).
			Render("  ·  " + v.stats.Host)
	}
	lines = append(lines, uikit.PadLine(hostLine, w, uikit.ColorBgLight))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBgLight))

	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

	if v.stats != nil {
		cpuLines := v.renderSparklineRow(w,
			"CPU", fmt.Sprintf("%.1f%%", v.stats.CPUUsage),
			fmt.Sprintf("%d cores", v.stats.CPUCores),
			v.cpuHistory, uikit.ColorRunning)
		lines = append(lines, cpuLines...)

		lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

		memLines := v.renderSparklineRow(w,
			"MEM", fmt.Sprintf("%.1f%%", v.stats.MemUsage),
			config.FormatByteSize(int64(v.stats.MemUsed))+"/"+config.FormatByteSize(int64(v.stats.MemTotal)),
			v.memHistory, uikit.ColorSecondary)
		lines = append(lines, memLines...)
	} else {
		waiting := uikit.OnBg(uikit.ColorBg, uikit.ColorTextMuted).Render("  Loading metrics...")
		lines = append(lines, uikit.PadLine(waiting, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) renderSparklineRow(w int, label, pct, detail string, history []float64, color color.Color) []string {
	var lines []string

	bgStyle := lipgloss.NewStyle().Background(uikit.ColorBg)
	chartStyle := uikit.OnBg(uikit.ColorChartBg, color)

	labelStr := uikit.OnBg(uikit.ColorBg, color).Bold(true).Render("  " + label + " ")
	pctStr := uikit.InfoStatValueStyle.Render(pct)
	detailStr := uikit.InfoStatLabelStyle.Render("  " + detail)

	spark := chartStyle.Render(renderSparkline(history, sparklineWidth))

	headerLeft := labelStr + pctStr + detailStr
	headerWidth := lipgloss.Width(headerLeft)

	if headerWidth+sparklineWidth+4 <= w {
		gap := max(w-headerWidth-sparklineWidth-2, 2)
		gapStr := bgStyle.Render(strings.Repeat(" ", gap))
		lines = append(lines, uikit.PadLine(headerLeft+gapStr+spark, w, uikit.ColorBg))
	} else {
		lines = append(lines, uikit.PadLine(headerLeft, w, uikit.ColorBg))
		lines = append(lines, uikit.PadLine(bgStyle.Render("  ")+spark, w, uikit.ColorBg))
	}

	return lines
}

// renderSparkline renders the last width samples of history as a single-line
// block-character sparkline (▁▂▃▄▅▆▇█), each value clamped to 0-100 and
// mapped onto the block levels. Missing leading samples render as spaces.
func renderSparkline(history []float64, width int) string {
	start := 0
	if n := len(history); n > width {
		start = n - width
	}
	var b strings.Builder
	for i := 0; i < width-(len(history)-start); i++ {
		b.WriteByte(' ')
	}
	for _, val := range history[start:] {
		switch {
		case val < 0:
			val = 0
		case val > 100:
			val = 100
		}
		idx := int(val / 100 * float64(len(sparklineBlocks)-1))
		b.WriteRune(sparklineBlocks[idx])
	}
	return b.String()
}

func (v *InfoView) renderActivitySection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render("Activity"), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

	s := v.runSummary
	parts := []string{
		uikit.InfoStatValueStyle.Render(strconv.FormatInt(s.Total, 10)) + uikit.InfoStatLabelStyle.Render(" runs"),
	}
	if s.Total > 0 {
		successStr := uikit.OnBg(uikit.ColorBg, uikit.ColorSuccess).Bold(true).Render(strconv.FormatInt(s.Success, 10))
		failedStr := uikit.OnBg(uikit.ColorBg, uikit.ColorError).Bold(true).Render(strconv.FormatInt(s.Failed, 10))
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
		rateStr := uikit.OnBg(uikit.ColorBg, rateColor).Render(fmt.Sprintf("%.1f%%", rate))
		parts = append(parts, rateStr)
	}
	sep := uikit.InfoStatLabelStyle.Render("  ·  ")
	line := bgIndent() + strings.Join(parts, sep)
	lines = append(lines, uikit.PadLine(line, w, uikit.ColorBg))

	if s.LastFailure != nil {
		failLine := bgIndent() + uikit.InfoStatLabelStyle.Render("Last failure  ") +
			uikit.OnBg(uikit.ColorBg, uikit.ColorTextMuted).Render(s.LastFailure.In(v.loc).Format(time.RFC3339))
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
	if v.info.StationEnabled {
		fields = append(fields, kv{"Station", "Connected"})
	}

	for _, f := range fields {
		if f.value == "" {
			continue
		}
		label := uikit.InfoLabelStyle.Render(f.label)
		value := uikit.InfoValueStyle.Render(f.value)
		lines = append(lines, uikit.PadLine(bgIndent()+label+value, w, uikit.ColorBg))
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
	for _, capInfo := range caps {
		if capInfo.Available {
			capStrs = append(capStrs, uikit.InfoCapsAvailableStyle.Render("✓ "+capInfo.Name))
		} else {
			capStrs = append(capStrs, uikit.InfoCapsUnavailableStyle.Render("✗ "+capInfo.Name))
		}
	}
	capLine := bgIndent() + strings.Join(capStrs, bgIndent())
	if lipgloss.Width(capLine) > w-2 {
		for _, c := range capStrs {
			lines = append(lines, uikit.PadLine(bgIndent()+c, w, uikit.ColorBg))
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
		lines = append(lines, uikit.PadLine(bgIndent()+label+value, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) renderTasksSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render(fmt.Sprintf("Tasks (%d)", len(v.info.Tasks))), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

	for _, task := range v.info.Tasks {
		sched := uikit.ScheduleLabel(&task)
		name := uikit.OnBg(uikit.ColorBg, uikit.ColorTextBright).Bold(true).Render(task.Name)
		schedStyle := uikit.OnBg(uikit.ColorBg, uikit.ColorTextMuted).Render(sched)
		line := bgIndent() + name + bgIndent() + schedStyle
		if u, ok := v.taskUsage[task.Name]; ok {
			line += bgIndent() + uikit.OnBg(uikit.ColorBg, uikit.ColorRunning).Render(uikit.FormatUsage(u))
		}
		lines = append(lines, uikit.PadLine(line, w, uikit.ColorBg))
	}

	return lines
}

func (v *InfoView) renderWarningsSection(w int) []string {
	var lines []string
	lines = append(lines, v.sectionDivider(w)...)
	lines = append(lines, uikit.PadLine(uikit.InfoSectionStyle.Render("Warnings"), w, uikit.ColorBg))
	lines = append(lines, uikit.PadLine("", w, uikit.ColorBg))

	for _, warn := range v.info.ScheduleWarnings {
		warnStyle := uikit.OnBg(uikit.ColorBg, uikit.ColorWarning).Render("⚠ " + warn)
		lines = append(lines, uikit.PadLine(bgIndent()+warnStyle, w, uikit.ColorBg))
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

// bgIndent is the two-column left indent on the page background.
func bgIndent() string {
	return lipgloss.NewStyle().Background(uikit.ColorBg).Render("  ")
}

// appendCapped appends val, keeping only the newest maxHistorySamples values.
func appendCapped(s []float64, val float64) []float64 {
	s = append(s, val)
	if len(s) > maxHistorySamples {
		s = s[len(s)-maxHistorySamples:]
	}
	return s
}
