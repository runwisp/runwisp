// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/model"
)

func (v *ExecView) renderMetaField(label, value string, focus headerFocusItem) string {
	bg := colorBgLight
	focused := v.headerFocus == focus
	hovered := v.hoveredHeader == focus && focus != headerFocusNone
	if focused || hovered {
		bg = colorSidebarActive
	}
	labelStyle := lipgloss.NewStyle().Background(bg).Foreground(colorTextMuted)
	valueStyle := lipgloss.NewStyle().Background(bg).Foreground(colorTextBright)
	if focused {
		valueStyle = valueStyle.Bold(true)
	}
	return labelStyle.Render(label+" ") + valueStyle.Render(value)
}

func (v *ExecView) renderBackButton() string {
	style := btnBackStyle
	if v.hoveredHeader == headerFocusBack || v.headerFocus == headerFocusBack {
		style = btnBackHoverStyle
	}
	return style.Render("← Back")
}

func (v *ExecView) renderActionButtons() string {
	switch v.Action() {
	case execViewActionStop:
		style := btnStopStyle
		if v.hoveredHeader == headerFocusAction || v.headerFocus == headerFocusAction {
			style = btnStopHoverStyle
		}
		return style.Render("■ Stop (s)")
	case execViewActionRetry:
		style := btnRetryStyle
		if v.hoveredHeader == headerFocusAction || v.headerFocus == headerFocusAction {
			style = btnRetryHoverStyle
		}
		return style.Render("↻ Retry (r)")
	default:
		return ""
	}
}

func (v *ExecView) View() string {
	var b strings.Builder

	if v.fullscreen {
		v.headerLayout.reset()
		v.pane.RenderLines(&b, false)
		return b.String()
	}

	w := v.pane.width

	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")

	statusStr := v.run.DisplayStatus()
	statusBadge := statusStyle(statusStr).Render(statusStr)
	bgLight := lipgloss.NewStyle().Background(colorBgLight)
	backBtn := v.renderBackButton()
	v.headerLayout.reset()

	backBtnX0 := sidebarWidth + 2
	v.headerLayout.add(headerFocusBack, backBtnX0, backBtnX0+lipgloss.Width(backBtn), 1)

	idFocused := v.headerFocus == headerFocusID
	idHovered := v.hoveredHeader == headerFocusID
	idBg := colorBgLight
	if idFocused || idHovered {
		idBg = colorSidebarActive
	}
	idStyle := lipgloss.NewStyle().Background(idBg).Foreground(colorTextMuted)
	if idFocused {
		idStyle = idStyle.Bold(true)
	}
	idTag := idStyle.Render("#" + v.run.ID[len(v.run.ID)-8:])

	headerLeft := bgLight.Render("  ") +
		backBtn +
		bgLight.Render("  ") +
		bgLight.Bold(true).Foreground(colorTextBright).Render(v.run.TaskName) +
		bgLight.Render("  ")
	idX0 := sidebarWidth + lipgloss.Width(headerLeft)
	headerLeft = headerLeft + idTag
	idX1 := sidebarWidth + lipgloss.Width(headerLeft)
	v.headerLayout.add(headerFocusID, idX0, idX1, 1)
	headerRight := statusBadge + bgLight.Render("  ")
	headerGap := w - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if headerGap < 1 {
		headerGap = 1
	}
	b.WriteString(padLine(headerLeft+bgLight.Render(strings.Repeat(" ", headerGap))+headerRight, w, colorBgLight))
	b.WriteString("\n")

	dur := formatDuration(*v.run)
	startedAt := v.run.CreatedAt.Local().Format("2006-01-02 15:04:05")
	metaSep := bgLight.Foreground(colorTextMuted).Render("  ·  ")

	metaPrefix := bgLight.Render("  ")
	currentX := sidebarWidth + lipgloss.Width(metaPrefix)

	startedField := v.renderMetaField("Started", startedAt, headerFocusStarted)
	startedX0 := currentX
	startedX1 := startedX0 + lipgloss.Width(startedField)
	v.headerLayout.add(headerFocusStarted, startedX0, startedX1, 2)
	currentX = startedX1 + lipgloss.Width(metaSep)

	durField := v.renderMetaField("Duration", dur, headerFocusDuration)
	durationX0 := currentX
	durationX1 := durationX0 + lipgloss.Width(durField)
	v.headerLayout.add(headerFocusDuration, durationX0, durationX1, 2)

	metaLine := metaPrefix +
		startedField +
		metaSep +
		durField +
		metaSep +
		bgLight.Foreground(colorTextMuted).Render(string(v.run.TriggeredBy))
	followIndicator := ""
	if v.pane.follow && (v.run.Status == model.PhaseRunning || v.run.Status == model.PhasePending) {
		followIndicator = lipgloss.NewStyle().Background(colorBgLight).Foreground(colorSecondary).Bold(true).Render("  ● FOLLOW")
	}

	actionBtns := v.renderActionButtons()
	if actionBtns != "" {
		actionW := lipgloss.Width(actionBtns)
		gap := w - lipgloss.Width(metaLine) - lipgloss.Width(followIndicator) - actionW - 1
		if gap < 2 {
			gap = 2
		}
		actionX1 := sidebarWidth + w
		v.headerLayout.add(headerFocusAction, actionX1-actionW, actionX1, 2)
		metaLine = metaLine + followIndicator +
			bgLight.Render(strings.Repeat(" ", gap)) +
			actionBtns
	} else {
		metaLine = metaLine + followIndicator
	}
	b.WriteString(padLine(metaLine, w, colorBgLight))
	b.WriteString("\n")

	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")

	v.pane.RenderLines(&b, v.headerFocus != headerFocusNone)

	return b.String()
}
