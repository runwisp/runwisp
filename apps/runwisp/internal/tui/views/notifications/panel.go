// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notifications

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/internal/tui/rhythm"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

const (
	CollapsedH    = 1
	viewportLines = 8
	ExpandedH     = viewportLines + 2 // header + viewport + spacer

	// InitialPageSize matches the web UI's PAGE_SIZE so both
	// surfaces hydrate from the same initial slice.
	InitialPageSize = 50

	// boundaryFlashDuration is how long the cursor row pulses with
	// an accent background when a navigation keypress can't move the cursor.
	boundaryFlashDuration = 150 * time.Millisecond
)

// Panel mirrors the web UI's bell + popover: an always-on
// summary line that the operator can expand into a scrollable list. Items
// are tracked by ID so SSE updates mutate in place; the cursor highlights
// one row at a time inside a bubble's viewport. Read state is per-item
// (carried on server.NotificationDTO.ReadAt).
type Panel struct {
	items   map[string]server.NotificationDTO
	ordered []string // ULIDs DESC

	expanded bool
	cursor   int // index into `ordered`; only meaningful when expanded

	// unread is the server-authoritative count of rows with read_at IS NULL.
	// It is *replaced* (never delta'd) — seeded once at startup from
	// /api/notifications/unread-count and overwritten on every SSE event that
	// carries an unread_count. Local mark-read/unread does NOT touch it; the
	// next SSE event corrects the badge within ~1 round-trip.
	unread int

	// flashCursorUntil paints the cursor row with an accent background until
	// this moment, then reverts. Used as boundary feedback when j/k/arrows
	// can't advance.
	flashCursorUntil time.Time

	viewport viewport.Model

	width int
}

func NewPanel() Panel {
	vp := viewport.New(0, viewportLines)
	// Bubbles' viewport pads visible lines to its Width with unstyled spaces;
	// without a styled background the trailing pad and any blank rows below
	// the content render with the terminal's default colour. Apply uikit.ColorBg so
	// the entire viewport rectangle stays in theme.
	vp.Style = lipgloss.NewStyle().Background(uikit.ColorBg)
	return Panel{
		items:    make(map[string]server.NotificationDTO),
		viewport: vp,
	}
}

// Upsert applies a created/updated SSE event. Returns true when the panel
// changed in a way that warrants a repaint. The unread badge is owned by
// SetUnread (server-driven), not Upsert.
func (p *Panel) Upsert(n server.NotificationDTO) bool {
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
		p.insertOrdered(idx, n.ID)
		changed = true
	} else if n.Count != prev.Count ||
		!prev.LastOccurredAt.Equal(n.LastOccurredAt) ||
		isUnread(prev) != isUnread(n) {
		changed = true
	}
	if changed {
		p.rebuildContent()
		if p.expanded {
			p.ensureCursorVisible()
		}
	}
	return changed
}

// insertOrdered splices id into p.ordered at idx and keeps the cursor pinned to
// the same notification: an insert at or above the cursor shifts every tracked
// row down by one, so the cursor must move with it or actions (open, toggle
// read) would land on the neighbour that slid under the highlight.
func (p *Panel) insertOrdered(idx int, id string) {
	p.ordered = append(p.ordered, "")
	copy(p.ordered[idx+1:], p.ordered[idx:])
	p.ordered[idx] = id
	// Only meaningful while expanded — a collapsed panel has no positioned
	// cursor (Toggle snaps it into range on expand).
	if p.expanded && idx <= p.cursor {
		p.cursor++
	}
}

// IsExpanded reports whether the panel is in expanded mode.
func (p *Panel) IsExpanded() bool { return p.expanded }

// Total returns the count of distinct tracked notifications.
func (p *Panel) Total() int { return len(p.items) }

// Unread returns the snapshot+delta-tracked unread count.
func (p *Panel) Unread() int { return p.unread }

// Toggle flips expanded state and snaps the cursor into a valid range.
func (p *Panel) Toggle() {
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
func (p *Panel) MoveCursor(delta int) bool {
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
func (p *Panel) BumpBoundaryFlash() {
	if !p.expanded || len(p.ordered) == 0 {
		return
	}
	p.flashCursorUntil = time.Now().Add(boundaryFlashDuration)
	p.rebuildContent()
}

// ClearBoundaryFlash repaints if the flash window has fully elapsed. Rapid
// repeat keypresses extend flashCursorUntil into the future; the guard keeps
// the most recent flash alive instead of cutting it short.
func (p *Panel) ClearBoundaryFlash() {
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
func (p *Panel) flashActive() bool {
	return !p.flashCursorUntil.IsZero() && time.Now().Before(p.flashCursorUntil)
}

// ScheduleFlashClear returns a tea.Cmd that nudges the panel to
// repaint after the flash window elapses.
func ScheduleFlashClear() tea.Cmd {
	return tea.Tick(boundaryFlashDuration, func(time.Time) tea.Msg {
		return uikit.NotificationBoundaryFlashClearedMsg{}
	})
}

// Selected returns the currently-highlighted notification or nil.
func (p *Panel) Selected() *server.NotificationDTO {
	if !p.expanded || len(p.ordered) == 0 {
		return nil
	}
	if p.cursor < 0 || p.cursor >= len(p.ordered) {
		return nil
	}
	n := p.items[p.ordered[p.cursor]]
	return &n
}

// SetUnread overwrites the badge with the server's authoritative count.
// Called from the startup snapshot and from every SSE event that carries an
// unread_count. Negative values (the server's "query failed" sentinel) are
// ignored so the badge keeps the last known good value.
func (p *Panel) SetUnread(n int) {
	if n < 0 {
		return
	}
	p.unread = n
	p.rebuildContent()
}

// LoadHistorical seeds the panel with the initial page fetched from the REST
// list endpoint at startup.
func (p *Panel) LoadHistorical(items []server.NotificationDTO) bool {
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
		p.insertOrdered(idx, n.ID)
		changed = true
	}
	if changed {
		p.rebuildContent()
		if p.expanded {
			p.ensureCursorVisible()
		}
	}
	return changed
}

// RefreshLabels rebuilds the cached expanded viewport content so relative
// time labels ("5m ago") tick forward without waiting for an SSE event.
// Cheap: a string-builder pass over the in-memory ordered list.
func (p *Panel) RefreshLabels() {
	if p.expanded {
		p.rebuildContent()
	}
}

// MarkReadLocal stamps a single notification's ReadAt. No-op if the id is
// unknown or the row was already read. The badge does not change here — the
// authoritative count arrives on the next SSE event.
func (p *Panel) MarkReadLocal(id string, at time.Time) bool {
	n, ok := p.items[id]
	if !ok || n.ReadAt != nil {
		return false
	}
	stamp := at
	n.ReadAt = &stamp
	p.items[id] = n
	p.rebuildContent()
	return true
}

// MarkAllReadLocal stamps every still-unread notification's ReadAt and zeroes
// the badge optimistically. Returns true when anything changed. The
// authoritative count is corrected by the next SSE unread-count event.
func (p *Panel) MarkAllReadLocal(at time.Time) bool {
	changed := false
	for id, n := range p.items {
		if n.ReadAt != nil {
			continue
		}
		stamp := at
		n.ReadAt = &stamp
		p.items[id] = n
		changed = true
	}
	if p.unread != 0 {
		p.unread = 0
		changed = true
	}
	if changed {
		p.rebuildContent()
	}
	return changed
}

// MarkUnreadLocal clears a single notification's ReadAt. No-op if the id is
// unknown or the row was already unread. The badge does not change here —
// the authoritative count arrives on the next SSE event.
func (p *Panel) MarkUnreadLocal(id string) bool {
	n, ok := p.items[id]
	if !ok || n.ReadAt == nil {
		return false
	}
	n.ReadAt = nil
	p.items[id] = n
	p.rebuildContent()
	return true
}

// UnreadIDsForRun returns the IDs of all locally-known notifications attached
// to the given run that are still unread. Used to mark them read when the
// operator opens the run's exec view.
func (p *Panel) UnreadIDsForRun(runID string) []string {
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
func (p *Panel) SetWidth(w int) {
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
func (p *Panel) PanelHeight() int {
	if len(p.items) == 0 && p.unread == 0 {
		return 0
	}
	if p.expanded {
		return ExpandedH
	}
	return CollapsedH
}

// View renders the panel.
func (p *Panel) View() string {
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
func (p *Panel) countLabel() string {
	if p.unread == 0 {
		return "Notifications"
	}
	return fmt.Sprintf("Notifications (%d)", p.unread)
}

func (p *Panel) renderCollapsed() string {
	prefix := panelPrefixStyle.Render("  " + p.countLabel())
	hint := panelHintStyle.Render("  press n to expand")
	body := prefix + hint

	if latest := p.latest(); latest != nil {
		// Each segment sets its own background. Wrapping a pre-styled string in
		// another Render would inject ANSI reset codes mid-line, dropping the
		// background back to the terminal default for everything after the
		// embedded style.
		summaryStyle := lipgloss.NewStyle().Background(uikit.ColorBgLight).Foreground(uikit.ColorTextDim)
		sev := severityStyle(latest.Severity).Background(uikit.ColorBgLight).Render(strings.ToUpper(latest.Severity))
		leading := summaryStyle.Render("  ")
		trailing := summaryStyle.Render(" · " + summarizeNotification(*latest) + " · " + relativeTime(latest.LastOccurredAt))
		body = prefix + leading + sev + trailing + hint
	}

	if p.width > 0 {
		body = truncateLine(body, p.width)
	}
	return uikit.PadLine(body, p.width, uikit.ColorBgLight)
}

func (p *Panel) renderExpanded() string {
	headerL := panelPrefixStyle.Render("  " + p.countLabel())
	// Same notification keybindings as the help bar and overlay (keys package),
	// joined with the panel's own separator so the three never drift.
	hint := strings.Join([]string{
		keys.NotifCollapse.Bar, keys.Move.Bar, keys.NotifRead.Bar, keys.NotifReadAll.Bar, keys.NotifOpen.Bar,
	}, " · ") + "  "
	headerR := panelHintStyle.Render(hint)
	header := joinHeaderLine(headerL, headerR, p.width)

	// renderRow already pads each row to viewport.Width with uikit.ColorBg, and the
	// viewport's Style fills its remaining rectangle with uikit.ColorBg too.
	body := p.viewport.View()

	spacer := stripeFocusLine("", p.width-1, uikit.ColorBg)
	return header + "\n" + body + "\n" + spacer
}

func joinHeaderLine(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := width - lw - rw
	if gap < 1 {
		gap = 1
	}
	pad := lipgloss.NewStyle().Background(uikit.ColorBgLight).Render(strings.Repeat(" ", gap))
	line := left + pad + right
	if width > 0 {
		line = uikit.PadLine(line, width, uikit.ColorBgLight)
	}
	return line
}

func (p *Panel) rebuildContent() {
	if !p.expanded {
		return
	}
	rest := p.viewport.Width - 1
	if rest < 0 {
		rest = 0
	}
	if len(p.ordered) == 0 {
		hint := lipgloss.NewStyle().
			Background(uikit.ColorBg).
			Foreground(uikit.ColorTextMuted).
			Render("  No notifications yet.")
		p.viewport.SetContent(stripeFocusLine(hint, rest, uikit.ColorBg))
		return
	}
	lines := make([]string, 0, len(p.ordered))
	for i, id := range p.ordered {
		n := p.items[id]
		lines = append(lines, p.renderRow(n, i == p.cursor))
	}
	for len(lines) < viewportLines {
		lines = append(lines, stripeFocusLine("", rest, uikit.ColorBg))
	}
	p.viewport.SetContent(strings.Join(lines, "\n"))
}

func (p *Panel) renderRow(n server.NotificationDTO, selected bool) string {
	bg := uikit.ColorBg
	if selected {
		bg = uikit.ColorBgLight
		if p.flashActive() {
			bg = uikit.ColorPrimaryDim
		}
	}
	indicator := "  "
	if selected {
		indicator = "▸ "
	}
	sev := lipgloss.NewStyle().Background(bg).Render(" ")
	if isUnread(n) {
		sev = severityStyle(n.Severity).Background(bg).Render("●")
	}

	title := n.Title
	if title == "" {
		title = n.Kind
	}
	if n.Count > 1 {
		title = fmt.Sprintf("%s ×%d", title, n.Count)
	}
	when := relativeTime(n.LastOccurredAt)

	indentStyle := lipgloss.NewStyle().Background(bg).Foreground(uikit.ColorTextMuted)
	titleStyle := lipgloss.NewStyle().Background(bg).Foreground(uikit.ColorText).Bold(selected)
	whenStyle := lipgloss.NewStyle().Background(bg).Foreground(uikit.ColorTextMuted)

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
	stripe := lipgloss.NewStyle().Background(uikit.ColorPrimary).Render(" ")
	if restWidth < 0 {
		restWidth = 0
	}
	return stripe + uikit.PadLine(content, restWidth, restBg)
}

func (p *Panel) latest() *server.NotificationDTO {
	for _, id := range p.ordered {
		n := p.items[id]
		if isUnread(n) {
			return &n
		}
	}
	return nil
}

func (p *Panel) ensureCursorVisible() {
	top := p.viewport.YOffset
	bottom := top + p.viewport.Height - 1
	if p.cursor < top {
		p.viewport.SetYOffset(p.cursor)
	} else if p.cursor > bottom {
		p.viewport.SetYOffset(p.cursor - p.viewport.Height + 1)
	}
}

func summarizeNotification(n server.NotificationDTO) string {
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
	return rhythm.Relative(t, time.Now())
}

// truncateLine trims a (possibly ANSI-styled) line to at most max display
// columns. It slices by visible column via uikit.SliceLineColumns — never by
// raw bytes — so a cut never lands mid escape-sequence or mid-rune and garbles
// the styled collapsed summary.
func truncateLine(s string, max int) string {
	if max <= 1 || lipgloss.Width(s) <= max {
		return s
	}
	if max <= 3 {
		sliced, _ := uikit.SliceLineColumns(s, 0, max)
		return sliced
	}
	sliced, _ := uikit.SliceLineColumns(s, 0, max-1)
	return sliced + "…"
}

func isUnread(n server.NotificationDTO) bool { return n.ReadAt == nil }

var (
	panelPrefixStyle = lipgloss.NewStyle().
				Background(uikit.ColorBgLight).
				Foreground(uikit.ColorTextBright).
				Bold(true)

	panelHintStyle = lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorTextMuted)
)

func severityStyle(severity string) lipgloss.Style {
	base := lipgloss.NewStyle().Background(uikit.ColorBg).Bold(true)
	switch severity {
	case "error":
		return base.Foreground(uikit.ColorError)
	case "warn":
		return base.Foreground(uikit.ColorWarning)
	default:
		return base.Foreground(uikit.ColorSuccess)
	}
}
