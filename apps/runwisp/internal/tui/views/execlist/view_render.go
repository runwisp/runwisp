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

func (v *ExecView) renderActionButton() string {
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
	default:
		return ""
	}
}

func (v *ExecView) renderDeleteButton() string {
	if !v.CanDelete() {
		return ""
	}
	if v.HoveredHeader == HeaderFocusDelete || v.HeaderFocus == HeaderFocusDelete {
		return uikit.BtnStopHoverStyle.Render("Delete (D)")
	}
	return uikit.BtnStopStyle.Render("Delete (D)")
}

func (v *ExecView) View() string {
	var b strings.Builder

	if v.fullscreen {
		v.headerLayout.reset()
		v.Pane.RenderLines(&b, false, v.LoadingOlder)
		return b.String()
	}

	w := v.Pane.Width
	v.headerLayout.reset()
	metaLine, btnLine := v.placeButtons(v.renderMetaRow(w), w)
	for _, line := range []string{"", v.renderTitleRow(w), metaLine, btnLine} {
		b.WriteString(uikit.PadLine(line, w, uikit.ColorBgLight))
		b.WriteString("\n")
	}

	v.Pane.RenderLines(&b, v.HeaderFocus != HeaderFocusNone, v.LoadingOlder)

	return b.String()
}

// renderTitleRow draws header row 1: Back, task label, run ID and status.
func (v *ExecView) renderTitleRow(w int) string {
	statusStr := v.Run.DisplayStatus()
	statusBadge := uikit.StatusStyle(statusStr).Render(statusStr)
	bgLight := lipgloss.NewStyle().Background(uikit.ColorBgLight)
	backBtn := v.renderBackButton()

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

	taskLabel := model.InstanceLabel(v.Run.TaskName, v.Run.InstanceIndex, v.InstanceCount)
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
	return headerLeft + bgLight.Render(strings.Repeat(" ", headerGap)) + headerRight
}

// renderMetaRow draws header row 2's meta fields (without the buttons) and
// registers their hit boxes.
func (v *ExecView) renderMetaRow(w int) string {
	bgLight := lipgloss.NewStyle().Background(uikit.ColorBgLight)
	dur := uikit.FormatDuration(*v.Run)
	startedAt := uikit.FormatTimestamp(v.Run.CreatedAt, v.Loc)
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
		durField
	// Optional trailing meta is appended only while it fits: an 80-col
	// terminal leaves the header ~52 cols, and a line wider than that is
	// clipped by the terminal (hiding the buttons and misplacing hit boxes).
	fits := func(extra string) bool {
		return lipgloss.Width(metaLine)+lipgloss.Width(extra) <= w
	}

	// Resolved run params surface as a bounded "Params N" chip (full key=value
	// list lives in the on-demand modal). Shown only when the run carries params,
	// so the header stays a fixed height and never overflows the line width.
	v.paramsHidden = false
	if v.hasParams() {
		paramsField := v.renderMetaField("Params", strconv.Itoa(len(v.Run.Params)), HeaderFocusParams)
		if fits(metaSep + paramsField) {
			paramsX0 := currentX
			v.headerLayout.add(HeaderFocusParams, paramsX0, paramsX0+lipgloss.Width(paramsField), 2)
			metaLine += metaSep + paramsField
		} else {
			// Keep keyboard focus off a chip that isn't drawn; `i` still shows params.
			v.paramsHidden = true
			if v.HeaderFocus == HeaderFocusParams {
				v.HeaderFocus = HeaderFocusDuration
			}
		}
	}

	if trigger := metaSep + bgLight.Foreground(uikit.ColorTextMuted).Render(string(v.Run.TriggeredBy)); fits(trigger) {
		metaLine += trigger
	}
	if v.Pane.Follow && (v.Run.Status == model.PhaseRunning || v.Run.Status == model.PhasePending) {
		if follow := lipgloss.NewStyle().Background(uikit.ColorBgLight).Foreground(uikit.ColorSecondary).Bold(true).Render("  ● FOLLOW"); fits(follow) {
			metaLine += follow
		}
	}

	return metaLine
}

// placeButtons appends the Action/Delete buttons to the meta row, or returns
// them as row 3 when they don't fit, registering their hit boxes. It returns
// the final row 2 and row 3.
func (v *ExecView) placeButtons(metaLine string, w int) (string, string) {
	// Buttons sit right-aligned on the meta row when they fit beside it,
	// otherwise on the (otherwise blank) line below, so the header height
	// never changes and nothing is clipped at narrow widths.
	bgLight := lipgloss.NewStyle().Background(uikit.ColorBgLight)
	actionBtn := v.renderActionButton()
	deleteBtn := v.renderDeleteButton()
	btns := actionBtn
	if actionBtn != "" && deleteBtn != "" {
		btns += bgLight.Render(" ")
	}
	btns += deleteBtn
	if btns == "" {
		return metaLine, ""
	}
	btnsW := lipgloss.Width(btns)
	onMetaRow := lipgloss.Width(metaLine)+2+btnsW+1 <= w
	base, btnY := metaLine, 2
	if !onMetaRow {
		base, btnY = "", 3
	}
	gap := max(w-lipgloss.Width(base)-btnsW-1, 0)
	x0 := uikit.SidebarWidth + lipgloss.Width(base) + gap
	x1 := x0 + btnsW
	if actionBtn != "" {
		v.headerLayout.add(HeaderFocusAction, x0, x0+lipgloss.Width(actionBtn), btnY)
	}
	if deleteBtn != "" {
		v.headerLayout.add(HeaderFocusDelete, x1-lipgloss.Width(deleteBtn), x1, btnY)
	}
	line := base + bgLight.Render(strings.Repeat(" ", gap)) + btns
	if onMetaRow {
		return line, ""
	}
	return metaLine, line
}
