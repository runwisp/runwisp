// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/notify/render"
)

const (
	notificationsCollapsedH    = 1
	notificationsViewportLines = 8
	notificationsExpandedH     = notificationsViewportLines + 2 // header + viewport + spacer

	// notificationsInitialPageSize matches the web UI's PAGE_SIZE so both
	// surfaces hydrate from the same initial slice.
	notificationsInitialPageSize = 50

	// notificationsBoundaryFlashDuration is how long the cursor row pulses with
	// an accent background when a navigation keypress can't move the cursor.
	notificationsBoundaryFlashDuration = 150 * time.Millisecond
)

// notificationsPanel mirrors the web UI's bell + popover: an always-on
// summary line that the operator can expand into a scrollable list. Items
// are tracked by ID so SSE updates mutate in place; the cursor highlights
// one row at a time inside a bubble's viewport. Read state is per-item
// (carried on apiclient.Notification.ReadAt) — the panel never holds a
// global "last read" marker.
type notificationsPanel struct {
	items   map[string]apiclient.Notification
	ordered []string // ULIDs DESC

	expanded bool
	cursor   int // index into `ordered`; only meaningful when expanded

	// hintUnreadFloor seeds the unread badge before the initial page has
	// loaded — the dedicated unread-count endpoint resolves first, and the
	// snapshot count covers items beyond what's currently in `items`. Once
	// the panel observes a derived count >= floor (or the user explicitly
	// changes a row's read state), the floor stops shadowing the derivation.
	hintUnreadFloor int

	// flashCursorUntil paints the cursor row with an accent background until
	// this moment, then reverts. Used as boundary feedback when j/k/arrows
	// can't advance.
	flashCursorUntil time.Time

	viewport viewport.Model

	width int
}

func newNotificationsPanel() notificationsPanel {
	vp := viewport.New(0, notificationsViewportLines)
	// Bubbles' viewport pads visible lines to its Width with unstyled spaces;
	// without a styled background the trailing pad and any blank rows below
	// the content render with the terminal's default colour. Apply colorBg so
	// the entire viewport rectangle stays in theme.
	vp.Style = lipgloss.NewStyle().Background(colorBg)
	return notificationsPanel{
		items:    make(map[string]apiclient.Notification),
		viewport: vp,
	}
}

// Upsert applies a created/updated SSE event. Returns true when the panel
// changed in a way that warrants a repaint.
func (p *notificationsPanel) Upsert(n apiclient.Notification) bool {
	if n.ID == "" {
		return false
	}
	prev, existed := p.items[n.ID]
	p.items[n.ID] = n
	changed := false
	if !existed {
		idx := sort.Search(len(p.ordered), func(i int) bool {
			return p.ordered[i] < n.ID
		})
		p.ordered = append(p.ordered, "")
		copy(p.ordered[idx+1:], p.ordered[idx:])
		p.ordered[idx] = n.ID
		changed = true
	} else if n.Count != prev.Count ||
		!prev.LastOccurredAt.Equal(n.LastOccurredAt) ||
		readStateOf(prev) != readStateOf(n) {
		changed = true
	}
	if changed {
		p.rebuildContent()
	}
	return changed
}

// IsExpanded reports whether the panel is in expanded mode.
func (p *notificationsPanel) IsExpanded() bool { return p.expanded }

// Total returns the count of distinct tracked notifications.
func (p *notificationsPanel) Total() int { return len(p.items) }

// Unread returns the number of items currently considered unread. When the
// snapshot count fetched at startup exceeds the count we can derive from the
// loaded items (older unread items beyond the first page), the snapshot wins.
func (p *notificationsPanel) Unread() int {
	derived := 0
	for _, id := range p.ordered {
		if p.items[id].ReadAt == nil {
			derived++
		}
	}
	if p.hintUnreadFloor > derived {
		return p.hintUnreadFloor
	}
	return derived
}

// Toggle flips expanded state and snaps the cursor into a valid range.
func (p *notificationsPanel) Toggle() {
	p.expanded = !p.expanded
	if p.expanded {
		if p.cursor < 0 {
			p.cursor = 0
		}
		if p.cursor >= len(p.ordered) && len(p.ordered) > 0 {
			p.cursor = len(p.ordered) - 1
		}
		p.rebuildContent()
		p.ensureCursorVisible()
	}
}

// MoveCursor advances the cursor; no-op when collapsed or empty.
func (p *notificationsPanel) MoveCursor(delta int) bool {
	if !p.expanded || len(p.ordered) == 0 {
		return false
	}
	next := p.cursor + delta
	if next < 0 {
		next = 0
	} else if next >= len(p.ordered) {
		next = len(p.ordered) - 1
	}
	if next == p.cursor {
		return false
	}
	p.cursor = next
	p.rebuildContent()
	p.ensureCursorVisible()
	return true
}

// BumpBoundaryFlash starts a brief visual pulse on the cursor row to confirm
// that a navigation keypress was received but the cursor was already at a
// boundary (or the list has only one item). Callers schedule a clear-tick so
// the panel repaints once the duration elapses.
func (p *notificationsPanel) BumpBoundaryFlash() {
	if !p.expanded || len(p.ordered) == 0 {
		return
	}
	p.flashCursorUntil = time.Now().Add(notificationsBoundaryFlashDuration)
	p.rebuildContent()
}

// ClearBoundaryFlash repaints if the flash window has fully elapsed. Rapid
// repeat keypresses extend flashCursorUntil into the future; the guard keeps
// the most recent flash alive instead of cutting it short.
func (p *notificationsPanel) ClearBoundaryFlash() {
	if p.flashCursorUntil.IsZero() {
		return
	}
	if time.Now().Before(p.flashCursorUntil) {
		return
	}
	p.flashCursorUntil = time.Time{}
	p.rebuildContent()
}

// flashActive reports whether the boundary flash should currently render.
func (p *notificationsPanel) flashActive() bool {
	return !p.flashCursorUntil.IsZero() && time.Now().Before(p.flashCursorUntil)
}

// scheduleNotificationFlashClear returns a tea.Cmd that nudges the panel to
// repaint after the flash window elapses.
func scheduleNotificationFlashClear() tea.Cmd {
	return tea.Tick(notificationsBoundaryFlashDuration, func(time.Time) tea.Msg {
		return notificationBoundaryFlashClearedMsg{}
	})
}

// Selected returns the currently-highlighted notification or nil.
func (p *notificationsPanel) Selected() *apiclient.Notification {
	if !p.expanded || len(p.ordered) == 0 {
		return nil
	}
	if p.cursor < 0 || p.cursor >= len(p.ordered) {
		return nil
	}
	n := p.items[p.ordered[p.cursor]]
	return &n
}

// SetUnreadHint records the unread snapshot fetched at startup. It only
// affects the badge while the local items map underestimates (older unread
// rows beyond the first loaded page).
func (p *notificationsPanel) SetUnreadHint(n int) {
	if n < 0 {
		n = 0
	}
	p.hintUnreadFloor = n
	p.rebuildContent()
}

// LoadHistorical seeds the panel with the initial page fetched from the REST
// list endpoint at startup.
func (p *notificationsPanel) LoadHistorical(items []apiclient.Notification) bool {
	changed := false
	for _, n := range items {
		if n.ID == "" {
			continue
		}
		if _, existed := p.items[n.ID]; existed {
			continue
		}
		p.items[n.ID] = n
		idx := sort.Search(len(p.ordered), func(i int) bool {
			return p.ordered[i] < n.ID
		})
		p.ordered = append(p.ordered, "")
		copy(p.ordered[idx+1:], p.ordered[idx:])
		p.ordered[idx] = n.ID
		changed = true
	}
	if changed {
		p.rebuildContent()
	}
	return changed
}

// RefreshLabels rebuilds the cached expanded viewport content so relative
// time labels ("5m ago") tick forward without waiting for an SSE event.
// Cheap: a string-builder pass over the in-memory ordered list.
func (p *notificationsPanel) RefreshLabels() {
	if p.expanded {
		p.rebuildContent()
	}
}

// MarkReadLocal stamps a single notification's ReadAt and clears the snapshot
// floor (which can no longer be trusted once per-row state is touched). No-op
// if the id is unknown locally.
func (p *notificationsPanel) MarkReadLocal(id string, at time.Time) bool {
	n, ok := p.items[id]
	if !ok {
		return false
	}
	if n.ReadAt != nil {
		return false
	}
	stamp := at
	n.ReadAt = &stamp
	p.items[id] = n
	p.hintUnreadFloor = 0
	p.rebuildContent()
	return true
}

// MarkUnreadLocal clears a single notification's ReadAt locally. Snapshot
// floor is dropped for the same reason as MarkReadLocal.
func (p *notificationsPanel) MarkUnreadLocal(id string) bool {
	n, ok := p.items[id]
	if !ok {
		return false
	}
	if n.ReadAt == nil {
		return false
	}
	n.ReadAt = nil
	p.items[id] = n
	p.hintUnreadFloor = 0
	p.rebuildContent()
	return true
}

// UnreadIDsForRun returns the IDs of all locally-known notifications attached
// to the given run that are still unread. Used to mark them read when the
// operator opens the run's exec view.
func (p *notificationsPanel) UnreadIDsForRun(runID string) []string {
	if runID == "" {
		return nil
	}
	var out []string
	for id, n := range p.items {
		if n.RunID == runID && n.ReadAt == nil {
			out = append(out, id)
		}
	}
	return out
}

// SetWidth tells the panel how much horizontal space it has.
func (p *notificationsPanel) SetWidth(w int) {
	if w < 0 {
		w = 0
	}
	p.width = w
	inner := w
	if inner < 1 {
		inner = 1
	}
	p.viewport.Width = inner
	p.rebuildContent()
}

// PanelHeight returns the total height the panel needs (0 when there are
// no items and no unread state to report; >=1 otherwise).
func (p *notificationsPanel) PanelHeight() int {
	if len(p.items) == 0 && p.Unread() == 0 {
		return 0
	}
	if p.expanded {
		return notificationsExpandedH
	}
	return notificationsCollapsedH
}

// View renders the panel.
func (p *notificationsPanel) View() string {
	if p.PanelHeight() == 0 {
		return ""
	}
	if !p.expanded {
		return p.renderCollapsed()
	}
	return p.renderExpanded()
}

// countLabel renders the header label. With unread > 0 it includes the count
// in parentheses; with zero unread the parens drop entirely so pressing "r"
// produces visible feedback.
func (p *notificationsPanel) countLabel() string {
	u := p.Unread()
	if u == 0 {
		return "Notifications"
	}
	return fmt.Sprintf("Notifications (%d)", u)
}

func (p *notificationsPanel) renderCollapsed() string {
	prefix := notificationsPanelPrefixStyle.Render("  " + p.countLabel())
	hint := notificationsPanelHintStyle.Render("  press n to expand")
	body := prefix + hint

	if latest := p.latest(); latest != nil {
		// Each segment sets its own background. Wrapping a pre-styled string in
		// another Render would inject ANSI reset codes mid-line, dropping the
		// background back to the terminal default for everything after the
		// embedded style.
		summaryStyle := lipgloss.NewStyle().Background(colorBgLight).Foreground(colorTextDim)
		sev := notificationSeverityStyle(latest.Severity).Background(colorBgLight).Render(strings.ToUpper(latest.Severity))
		leading := summaryStyle.Render("  ")
		trailing := summaryStyle.Render(" · " + summarizeNotification(*latest) + " · " + relativeTime(latest.LastOccurredAt))
		body = prefix + leading + sev + trailing + hint
	}

	if p.width > 0 {
		body = truncateLine(body, p.width)
	}
	return padLine(body, p.width, colorBgLight)
}

func (p *notificationsPanel) renderExpanded() string {
	headerL := notificationsPanelPrefixStyle.Render("  " + p.countLabel())
	headerR := notificationsPanelHintStyle.Render("n collapse · ↑/↓ navigate · r toggle read · enter open  ")
	header := joinHeaderLine(headerL, headerR, p.width)

	// renderRow already pads each row to viewport.Width with colorBg, and the
	// viewport's Style fills its remaining rectangle with colorBg too.
	body := p.viewport.View()

	spacer := stripeFocusLine("", p.width-1, colorBg)
	return header + "\n" + body + "\n" + spacer
}

func joinHeaderLine(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := width - lw - rw
	if gap < 1 {
		gap = 1
	}
	pad := lipgloss.NewStyle().Background(colorBgLight).Render(strings.Repeat(" ", gap))
	line := left + pad + right
	if width > 0 {
		line = padLine(line, width, colorBgLight)
	}
	return line
}

func (p *notificationsPanel) rebuildContent() {
	if !p.expanded {
		return
	}
	rest := p.viewport.Width - 1
	if rest < 0 {
		rest = 0
	}
	if len(p.ordered) == 0 {
		hint := lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorTextMuted).
			Render("  No notifications yet.")
		p.viewport.SetContent(stripeFocusLine(hint, rest, colorBg))
		return
	}
	lines := make([]string, 0, len(p.ordered))
	for i, id := range p.ordered {
		n := p.items[id]
		lines = append(lines, p.renderRow(n, i == p.cursor))
	}
	for len(lines) < notificationsViewportLines {
		lines = append(lines, stripeFocusLine("", rest, colorBg))
	}
	p.viewport.SetContent(strings.Join(lines, "\n"))
}

func (p *notificationsPanel) renderRow(n apiclient.Notification, selected bool) string {
	bg := colorBg
	if selected {
		bg = colorBgLight
		if p.flashActive() {
			bg = colorPrimaryDim
		}
	}
	indicator := "  "
	if selected {
		indicator = "▸ "
	}
	sev := lipgloss.NewStyle().Background(bg).Render(" ")
	if n.ReadAt == nil {
		sev = notificationSeverityStyle(n.Severity).Background(bg).Render("●")
	}

	title := n.Title
	if title == "" {
		title = n.Kind
	}
	if n.Count > 1 {
		title = fmt.Sprintf("%s ×%d", title, n.Count)
	}
	when := relativeTime(n.LastOccurredAt)

	indentStyle := lipgloss.NewStyle().Background(bg).Foreground(colorTextMuted)
	titleStyle := lipgloss.NewStyle().Background(bg).Foreground(colorText).Bold(selected)
	whenStyle := lipgloss.NewStyle().Background(bg).Foreground(colorTextMuted)

	line := indentStyle.Render(indicator) + sev + titleStyle.Render(" "+title) + whenStyle.Render("  "+when)
	rest := p.viewport.Width - 1
	if rest < 0 {
		rest = 0
	}
	return stripeFocusLine(line, rest, bg)
}

// stripeFocusLine prepends a single primary-coloured cell to a row and pads
// the remainder with the row's own background. This is the "focus stripe" the
// expanded panel uses to broadcast that it owns keyboard input — visually
// the same idea as RunsList' active-row stripe in the web UI.
func stripeFocusLine(content string, restWidth int, restBg lipgloss.Color) string {
	stripe := lipgloss.NewStyle().Background(colorPrimary).Render(" ")
	if restWidth < 0 {
		restWidth = 0
	}
	return stripe + padLine(content, restWidth, restBg)
}

func (p *notificationsPanel) latest() *apiclient.Notification {
	if len(p.ordered) == 0 {
		return nil
	}
	n := p.items[p.ordered[0]]
	return &n
}

func (p *notificationsPanel) ensureCursorVisible() {
	top := p.viewport.YOffset
	bottom := top + p.viewport.Height - 1
	if p.cursor < top {
		p.viewport.SetYOffset(p.cursor)
	} else if p.cursor > bottom {
		p.viewport.SetYOffset(p.cursor - p.viewport.Height + 1)
	}
}

func summarizeNotification(n apiclient.Notification) string {
	title := n.Title
	if title == "" {
		title = n.Kind
	}
	if n.Count > 1 {
		return fmt.Sprintf("%s (×%d)", title, n.Count)
	}
	return title
}

// relativeTime is the single source of truth for relative-time labels — kept
// in lockstep with the Web UI's rhythm phrase so operators see the same
// "just now / 5m ago / 3d ago" wording in both surfaces.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return render.Relative(t, time.Now())
}

func truncateLine(s string, max int) string {
	if max <= 1 || lipgloss.Width(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// readStateOf is a small helper so Upsert can detect read-state transitions
// without dereferencing the pointer in three places.
func readStateOf(n apiclient.Notification) bool { return n.ReadAt != nil }

var (
	notificationsPanelPrefixStyle = lipgloss.NewStyle().
					Background(colorBgLight).
					Foreground(colorTextBright).
					Bold(true)

	notificationsPanelHintStyle = lipgloss.NewStyle().
					Background(colorBgLight).
					Foreground(colorTextMuted)
)

func notificationSeverityStyle(severity string) lipgloss.Style {
	base := lipgloss.NewStyle().Background(colorBg).Bold(true)
	switch severity {
	case "error":
		return base.Foreground(colorError)
	case "warn":
		return base.Foreground(colorWarning)
	default:
		return base.Foreground(colorSuccess)
	}
}
