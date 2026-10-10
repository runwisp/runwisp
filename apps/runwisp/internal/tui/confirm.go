// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// buttonKind is how loud a dialog button is when selected: neutral for
// cancel, primary for a safe action, danger for one that stops or destroys.
type buttonKind int

const (
	btnNeutral buttonKind = iota
	btnPrimary
	btnDanger
)

// fill is the button's background when selected (or hovered, when lighter).
func (k buttonKind) fill(hover bool) color.Color {
	switch {
	case k == btnPrimary && hover:
		return uikit.ColorPrimaryHover
	case k == btnPrimary:
		return uikit.ColorPrimary
	case k == btnDanger && hover:
		return uikit.ColorErrorHover
	case k == btnDanger:
		return uikit.ColorError
	}
	return uikit.ColorTextMuted
}

// ConfirmDialog renders a centered modal asking the user to confirm an action.
type ConfirmDialog struct {
	title     string
	message   string
	noteLines []string // optional muted lines below the message
	yesLabel  string
	noLabel   string
	yesKind   buttonKind
	noKind    buttonKind
	onConfirm tea.Cmd
	onDeny    tea.Cmd // if non-nil, called when user selects No (instead of just closing)
	selected  int     // 0 = Yes, 1 = No
	hovered   int     // -1 = none, 0 = Yes, 1 = No

	shuttingDown bool
	spinner      spinner.Model

	// Layout info cached during View() for mouse hit detection.
	btnYesX1, btnYesX2 int
	btnNoX1, btnNoX2   int
	btnY               int
}

// WithNote attaches one or more muted lines rendered below the
// message. Empty strings render as visual spacers. Returns the dialog
// for chained construction.
func (d *ConfirmDialog) WithNote(lines ...string) *ConfirmDialog {
	d.noteLines = lines
	return d
}

func NewConfirmDialog(title, message string, onConfirm tea.Cmd) *ConfirmDialog {
	return &ConfirmDialog{
		title:     title,
		message:   message,
		yesLabel:  "Yes",
		noLabel:   "No",
		yesKind:   btnPrimary,
		noKind:    btnNeutral,
		onConfirm: onConfirm,
		selected:  1,
		hovered:   -1,
	}
}

// Danger marks the confirm action as one that stops or destroys something: its
// button and the dialog's accent bar turn red. Returns the dialog for chaining.
func (d *ConfirmDialog) Danger() *ConfirmDialog {
	d.yesKind = btnDanger
	return d
}

// NewChoiceDialog offers two actions; both trigger a callback and only Esc
// cancels without action. noDanger marks the second as the destructive one.
func NewChoiceDialog(title, message, yesLabel, noLabel string, noDanger bool, onConfirm, onDeny tea.Cmd) *ConfirmDialog {
	noKind := btnPrimary
	if noDanger {
		noKind = btnDanger
	}
	return &ConfirmDialog{
		title:     title,
		message:   message,
		yesLabel:  yesLabel,
		noLabel:   noLabel,
		yesKind:   btnPrimary,
		noKind:    noKind,
		onConfirm: onConfirm,
		onDeny:    onDeny,
		selected:  0,
		hovered:   -1,
	}
}

// Returns a command and whether the dialog should close.
func (d *ConfirmDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	if d.shuttingDown {
		return nil, false
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return d.handleKeyMsg(msg.String())
	case tea.MouseMotionMsg:
		d.updateHover(msg.X, msg.Y)
		return nil, false
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			return d.handleClick(msg.X, msg.Y)
		}
	}
	return nil, false
}

func (d *ConfirmDialog) handleKeyMsg(key string) (tea.Cmd, bool) {
	switch key {
	case "left", "h", "tab":
		d.selected = 1 - d.selected
	case "right", "l":
		d.selected = 1 - d.selected
	case "enter":
		if d.selected == 0 {
			return d.onConfirm, true
		}
		if d.onDeny != nil {
			return d.onDeny, true
		}
		return nil, true
	case "esc":
		return nil, true
	case "n":
		if d.onDeny != nil {
			return d.onDeny, true
		}
		return nil, true
	case "y":
		return d.onConfirm, true
	}
	return nil, false
}

func (d *ConfirmDialog) handleClick(x, y int) (tea.Cmd, bool) {
	if y == d.btnY {
		if x >= d.btnYesX1 && x < d.btnYesX2 {
			return d.onConfirm, true
		}
		if x >= d.btnNoX1 && x < d.btnNoX2 {
			if d.onDeny != nil {
				return d.onDeny, true
			}
			return nil, true
		}
	}
	return nil, false
}

func (d *ConfirmDialog) updateHover(x, y int) {
	if y == d.btnY {
		if x >= d.btnYesX1 && x < d.btnYesX2 {
			d.hovered = 0
			return
		}
		if x >= d.btnNoX1 && x < d.btnNoX2 {
			d.hovered = 1
			return
		}
	}
	d.hovered = -1
}

// StartShutdown transitions the dialog into the shutting-down spinner state.
func (d *ConfirmDialog) StartShutdown() tea.Cmd {
	d.shuttingDown = true
	d.title = "Shutting Down"
	d.message = "Stopping daemon..."
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(uikit.ColorPrimary)
	d.spinner = s
	return d.spinnerTick()
}

// UpdateSpinner forwards a spinner tick and returns the next tick command.
func (d *ConfirmDialog) UpdateSpinner(innerMsg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.spinner, cmd = d.spinner.Update(innerMsg)
	if cmd == nil {
		return nil
	}
	return d.wrapSpinnerCmd(cmd)
}

func (d *ConfirmDialog) spinnerTick() tea.Cmd {
	return d.wrapSpinnerCmd(d.spinner.Tick)
}

func (d *ConfirmDialog) wrapSpinnerCmd(cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		return uikit.SpinnerTickMsg{Inner: cmd()}
	}
}

func (d *ConfirmDialog) View(screenWidth, screenHeight int) string {
	dialogWidth, innerWidth := modalDimensions(screenWidth, 46, 46)
	titleStr := modalSurfaceLine(d.title, innerWidth, uikit.ColorTextBright, true)
	msgStr := modalSurfaceLine(d.message, innerWidth, uikit.ColorText, false)

	var lines []string
	var yesBtnW, noBtnW int
	if d.shuttingDown {
		lines = d.renderShuttingDownLines(innerWidth, titleStr)
	} else {
		lines, yesBtnW, noBtnW = d.renderButtonLines(innerWidth, titleStr, msgStr)
	}

	accent := uikit.ColorPrimary
	if d.yesKind == btnDanger {
		accent = uikit.ColorError
	}
	box := renderModalBox(screenWidth, screenHeight, dialogWidth, accent, lines)

	if !d.shuttingDown {
		msgH := lipgloss.Height(msgStr)
		d.btnY = box.top + 1 + 3 + msgH + len(d.noteLines) + 1
		gapW := 3
		totalBtnW := yesBtnW + gapW + noBtnW
		btnAreaLeft := box.left + 2 + (box.innerWidth-totalBtnW)/2
		d.btnYesX1 = btnAreaLeft
		d.btnYesX2 = btnAreaLeft + yesBtnW
		d.btnNoX1 = btnAreaLeft + yesBtnW + gapW
		d.btnNoX2 = d.btnNoX1 + noBtnW
	}

	return box.view
}

func (d *ConfirmDialog) renderShuttingDownLines(innerWidth int, titleStr string) []string {
	// Render spinner and message as separately styled strings so the
	// spinner's internal ANSI reset doesn't kill the outer background.
	spinnerPart := d.spinner.View()
	msgPart := uikit.OnBg(uikit.ColorBgLight, uikit.ColorText).
		Render(" " + d.message)
	spinnerLine := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Width(innerWidth).
		Align(lipgloss.Center).
		Render(spinnerPart + msgPart)
	lines := []string{
		modalEmptyLine(innerWidth),
		titleStr,
		modalEmptyLine(innerWidth),
		spinnerLine,
	}
	return append(lines, modalFooter("ctrl+c force quit", innerWidth)...)
}

func (d *ConfirmDialog) renderButtonLines(innerWidth int, titleStr, msgStr string) (lines []string, yesBtnW, noBtnW int) {
	yesStyle := d.buttonStyle(0, d.yesKind)
	noStyle := d.buttonStyle(1, d.noKind)

	yesBtn := yesStyle.Render(d.yesLabel)
	noBtn := noStyle.Render(d.noLabel)
	btnGap := lipgloss.NewStyle().Background(uikit.ColorBgLight).Render("   ")
	buttons := lipgloss.JoinHorizontal(lipgloss.Center, yesBtn, btnGap, noBtn)
	buttonsLine := lipgloss.NewStyle().
		Width(innerWidth).
		Background(uikit.ColorBgLight).
		Align(lipgloss.Center).
		Render(buttons)

	hintText := "y confirm · n/esc cancel · ←→ switch"
	if d.onDeny != nil {
		hintText = "enter select · esc cancel · ←→ switch"
	}
	lines = []string{
		modalEmptyLine(innerWidth),
		titleStr,
		modalEmptyLine(innerWidth),
		msgStr,
	}
	for _, note := range d.noteLines {
		lines = append(lines, modalSurfaceLine(note, innerWidth, uikit.ColorTextMuted, false))
	}
	lines = append(lines, modalEmptyLine(innerWidth), buttonsLine)
	lines = append(lines, modalFooter(hintText, innerWidth)...)
	return lines, lipgloss.Width(yesBtn), lipgloss.Width(noBtn)
}

// buttonStyle renders button i (0 = yes, 1 = no). Selected or hovered, it
// fills with its kind's colour; otherwise it sits on a raised surface in
// normal text, so the unselected choice reads as available, not disabled.
func (d *ConfirmDialog) buttonStyle(i int, kind buttonKind) lipgloss.Style {
	style := lipgloss.NewStyle().Padding(0, 3).Bold(true)
	hover := d.hovered == i
	if d.selected == i || hover {
		return style.Background(kind.fill(hover)).Foreground(uikit.ColorWhite)
	}
	return style.Background(uikit.ColorButtonRaised).Foreground(uikit.ColorText)
}

type modalBox struct {
	left       int
	top        int
	innerWidth int
	view       string
}

func modalDimensions(screenWidth, desiredWidth, minWidth int) (int, int) {
	dialogWidth := max(desiredWidth, minWidth)
	maxWidth := max(screenWidth-4, 4)
	dialogWidth = min(dialogWidth, maxWidth)
	innerWidth := max(dialogWidth-4, 1)
	return dialogWidth, innerWidth
}

func renderModalBox(screenWidth, screenHeight, dialogWidth int, accent color.Color, lines []string) modalBox {
	card := lipgloss.NewStyle().
		Width(dialogWidth).
		Background(uikit.ColorBgLight).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
	accentBar := lipgloss.NewStyle().
		Background(accent).
		Width(dialogWidth).
		Height(1).
		Render("")
	box := lipgloss.JoinVertical(lipgloss.Left, accentBar, card)
	boxWidth := lipgloss.Width(box)
	boxHeight := lipgloss.Height(box)
	return modalBox{
		left:       (screenWidth - boxWidth) / 2,
		top:        (screenHeight - boxHeight) / 2,
		innerWidth: dialogWidth - 4,
		view: lipgloss.Place(screenWidth, screenHeight,
			lipgloss.Center, lipgloss.Center,
			box,
			lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(uikit.ColorBg)),
		),
	}
}

func modalSurfaceLine(text string, innerWidth int, fg color.Color, bold bool) string {
	style := uikit.OnBg(uikit.ColorBgLight, fg).
		Width(innerWidth).
		Align(lipgloss.Center)
	if bold {
		style = style.Bold(true)
	}
	return style.Render(text)
}

// modalFooter is the muted key-hint line every modal closes with, framed by
// blank rows.
func modalFooter(hint string, innerWidth int) []string {
	return []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine(wrapHints(hint, innerWidth), innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	}
}

// wrapHints breaks a " · "-separated key legend between hints, never inside
// one, so "enter run" can't split into "enter" / "run" across rows.
func wrapHints(hint string, width int) string {
	const sep = " · "
	var rows []string
	row := ""
	for _, h := range strings.Split(hint, sep) {
		switch {
		case row == "":
			row = h
		case lipgloss.Width(row+sep+h) <= width:
			row += sep + h
		default:
			rows = append(rows, row)
			row = h
		}
	}
	return strings.Join(append(rows, row), "\n")
}

func modalEmptyLine(innerWidth int) string {
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Width(innerWidth).
		Render("")
}

// modalSectionLine renders a left-aligned bold section header inside a modal.
func modalSectionLine(title string, innerWidth int) string {
	return uikit.OnBg(uikit.ColorBgLight, uikit.ColorSecondary).
		Bold(true).
		Width(innerWidth).
		Render(title)
}

// modalSeg renders one coloured inline segment on the modal surface, so a
// composed line keeps the dialog's fill behind each piece.
func modalSeg(text string, fg color.Color) string {
	return uikit.OnBg(uikit.ColorBgLight, fg).Render(text)
}

// isTextModalDismiss reports whether msg closes a read-only text modal: a close
// key, or a right-click (left-click is reserved for terminal text selection).
func isTextModalDismiss(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "enter", "backspace", "q":
			return true
		}
	case tea.MouseClickMsg:
		return msg.Button == tea.MouseRight
	}
	return false
}
