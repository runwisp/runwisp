// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// focusBg returns the header background for a focused or hovered field:
// highlighted when either is true, the default header background otherwise.
func focusBg(focused, hovered bool) color.Color {
	if focused || hovered {
		return uikit.ColorSidebarActive
	}
	return uikit.ColorBgLight
}

func (v *ExecView) renderMetaField(label, value string, focus HeaderFocusItem) string {
	focused := v.HeaderFocus == focus
	hovered := v.HoveredHeader == focus && focus != HeaderFocusNone
	bg := focusBg(focused, hovered)
	labelStyle := lipgloss.NewStyle().Background(bg).Foreground(uikit.ColorTextMuted)
	valueStyle := lipgloss.NewStyle().Background(bg).Foreground(uikit.ColorTextBright)
	if focused {
		valueStyle = valueStyle.Bold(true)
	}
	return labelStyle.Render(label+" ") + valueStyle.Render(value)
}

func (v *ExecView) renderBackButton() string {
	style := uikit.BtnBackStyle
	if v.HoveredHeader == HeaderFocusBack || v.HeaderFocus == HeaderFocusBack {
		style = uikit.BtnBackHoverStyle
	}
	return style.Render("← Back")
}

func (v *ExecView) renderActionButtons() string {
	actionHovered := v.HoveredHeader == HeaderFocusAction || v.HeaderFocus == HeaderFocusAction
	btn := func(base, hover lipgloss.Style, label string) string {
		if actionHovered {
			return hover.Render(label)
		}
		return base.Render(label)
	}

	switch v.Action() {
	case ActionStop, ActionStopService:
		return btn(uikit.BtnStopStyle, uikit.BtnStopHoverStyle, "■ Stop (s)")
	case ActionRetry:
		return btn(uikit.BtnRetryStyle, uikit.BtnRetryHoverStyle, "↻ Retry (r)")
	case ActionRestartService:
		return btn(uikit.BtnRetryStyle, uikit.BtnRetryHoverStyle, "↻ Restart (r)")
	case ActionDelete:
		return btn(uikit.BtnStopStyle, uikit.BtnStopHoverStyle, "Delete (D)")
	default:
		return ""
	}
}

func (v *ExecView) View() string {
	var b strings.Builder

	if v.fullscreen {
		v.headerLayout.reset()
		v.Pane.RenderLines(&b, false, v.LoadingOlder)
		return b.String()
	}

	w := v.Pane.Width

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	statusStr := v.Run.DisplayStatus()
	statusBadge := uikit.StatusStyle(statusStr).Render(statusStr)
	bgLight := lipgloss.NewStyle().Background(uikit.ColorBgLight)
	backBtn := v.renderBackButton()
	v.headerLayout.reset()

	backBtnX0 := uikit.SidebarWidth + 2
	v.headerLayout.add(HeaderFocusBack, backBtnX0, backBtnX0+lipgloss.Width(backBtn), 1)

	idFocused := v.HeaderFocus == HeaderFocusID
	idHovered := v.HoveredHeader == HeaderFocusID
	idBg := focusBg(idFocused, idHovered)
	idStyle := lipgloss.NewStyle().Background(idBg).Foreground(uikit.ColorTextMuted)
	if idFocused {
		idStyle = idStyle.Bold(true)
	}
	idTag := idStyle.Render("#" + v.Run.ID[len(v.Run.ID)-8:])

	taskLabel := instanceLabel(v.Run.TaskName, v.Run.InstanceIndex, v.InstanceCount)
	headerLeft := bgLight.Render("  ") +
		backBtn +
		bgLight.Render("  ") +
		bgLight.Bold(true).Foreground(uikit.ColorTextBright).Render(taskLabel) +
		bgLight.Render("  ")
	idX0 := uikit.SidebarWidth + lipgloss.Width(headerLeft)
	headerLeft = headerLeft + idTag
	idX1 := uikit.SidebarWidth + lipgloss.Width(headerLeft)
	v.headerLayout.add(HeaderFocusID, idX0, idX1, 1)
	headerRight := statusBadge + bgLight.Render("  ")
	headerGap := w - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if headerGap < 1 {
		headerGap = 1
	}
	b.WriteString(uikit.PadLine(headerLeft+bgLight.Render(strings.Repeat(" ", headerGap))+headerRight, w, uikit.ColorBgLight))
	b.WriteString("\n")

	dur := uikit.FormatDuration(*v.Run)
	startedAt := v.Run.CreatedAt.Local().Format("2006-01-02 15:04:05")
	metaSep := bgLight.Foreground(uikit.ColorTextMuted).Render("  ·  ")

	metaPrefix := bgLight.Render("  ")
	currentX := uikit.SidebarWidth + lipgloss.Width(metaPrefix)

	startedField := v.renderMetaField("Started", startedAt, HeaderFocusStarted)
	startedX0 := currentX
	startedX1 := startedX0 + lipgloss.Width(startedField)
	v.headerLayout.add(HeaderFocusStarted, startedX0, startedX1, 2)
	currentX = startedX1 + lipgloss.Width(metaSep)

	durField := v.renderMetaField("Duration", dur, HeaderFocusDuration)
	durationX0 := currentX
	durationX1 := durationX0 + lipgloss.Width(durField)
	v.headerLayout.add(HeaderFocusDuration, durationX0, durationX1, 2)
	currentX = durationX1 + lipgloss.Width(metaSep)

	metaLine := metaPrefix +
		startedField +
		metaSep +
		durField +
		metaSep

	// Resolved run params surface as a bounded "Params N" chip (full key=value
	// list lives in the on-demand modal). Shown only when the run carries params,
	// so the header stays a fixed height and never overflows the line width.
	if v.hasParams() {
		paramsField := v.renderMetaField("Params", strconv.Itoa(len(v.Run.Params)), HeaderFocusParams)
		paramsX0 := currentX
		paramsX1 := paramsX0 + lipgloss.Width(paramsField)
		v.headerLayout.add(HeaderFocusParams, paramsX0, paramsX1, 2)
		metaLine = metaLine + paramsField + metaSep
	}

	metaLine += bgLight.Foreground(uikit.ColorTextMuted).Render(string(v.Run.TriggeredBy))
	followIndicator := ""
	if v.Pane.Follow && (v.Run.Status == model.PhaseRunning || v.Run.Status == model.PhasePending) {
		followIndicator = lipgloss.NewStyle().Background(uikit.ColorBgLight).Foreground(uikit.ColorSecondary).Bold(true).Render("  ● FOLLOW")
	}

	actionBtns := v.renderActionButtons()
	if actionBtns != "" {
		actionW := lipgloss.Width(actionBtns)
		gap := w - lipgloss.Width(metaLine) - lipgloss.Width(followIndicator) - actionW - 1
		if gap < 2 {
			gap = 2
		}
		actionX1 := uikit.SidebarWidth + w
		v.headerLayout.add(HeaderFocusAction, actionX1-actionW, actionX1, 2)
		metaLine = metaLine + followIndicator +
			bgLight.Render(strings.Repeat(" ", gap)) +
			actionBtns
	} else {
		metaLine = metaLine + followIndicator
	}
	b.WriteString(uikit.PadLine(metaLine, w, uikit.ColorBgLight))
	b.WriteString("\n")

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	v.Pane.RenderLines(&b, v.HeaderFocus != HeaderFocusNone, v.LoadingOlder)

	return b.String()
}
