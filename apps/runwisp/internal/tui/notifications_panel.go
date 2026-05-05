// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/runwisp/runwisp/internal/apiclient"
)

const (
	notificationsCollapsedH    = 1
	notificationsViewportLines = 8
	notificationsExpandedH     = notificationsViewportLines + 2 // header + viewport + spacer
)

// notificationsPanel mirrors the web UI's bell + popover: an always-on
// summary line that the operator can expand into a scrollable list. Items
// are tracked by ID so SSE updates mutate in place; the cursor highlights
// one row at a time inside a bubble's viewport.
type notificationsPanel struct {
	items   map[string]apiclient.Notification
	ordered []string // ULIDs DESC

	expanded   bool
	cursor     int // index into `ordered`; only meaningful when expanded
	unread     int
	lastReadAt time.Time

	viewport viewport.Model

	width int
}

func newNotificationsPanel() notificationsPanel {
	vp := viewport.New(0, notificationsViewportLines)
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
		p.ordered = append(p.ordered, n.ID)
		sort.Sort(sort.Reverse(sort.StringSlice(p.ordered)))
		if p.lastReadAt.IsZero() || n.LastOccurredAt.After(p.lastReadAt) {
			p.unread++
		}
		changed = true
	} else if n.Count > prev.Count {
		if p.lastReadAt.IsZero() || n.LastOccurredAt.After(p.lastReadAt) {
			p.unread += n.Count - prev.Count
		}
		changed = true
	} else if !prev.LastOccurredAt.Equal(n.LastOccurredAt) {
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

// Unread returns the number of items strictly newer than lastReadAt.
func (p *notificationsPanel) Unread() int { return p.unread }

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

// SetUnread records the server-side unread count fetched at startup.
func (p *notificationsPanel) SetUnread(n int) { p.unread = n }

// MarkAllReadLocal applies the visual side of "mark all read"; call after
// the API call has succeeded.
func (p *notificationsPanel) MarkAllReadLocal(at time.Time) {
	p.unread = 0
	p.lastReadAt = at
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
	if len(p.items) == 0 && p.unread == 0 {
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

func (p *notificationsPanel) renderCollapsed() string {
	label := fmt.Sprintf("Notifications (%d)", len(p.items))
	if p.unread > 0 {
		label = fmt.Sprintf("Notifications (%d, %d unread)", len(p.items), p.unread)
	}
	prefix := notificationsPanelPrefixStyle.Render(label)
	hint := notificationsPanelHintStyle.Render("  press n to expand")
	body := prefix + hint

	if latest := p.latest(); latest != nil {
		sev := notificationSeverityStyle(latest.Severity).Render(strings.ToUpper(latest.Severity))
		summary := fmt.Sprintf("  %s · %s · %s", sev, summarizeNotification(*latest), relativeTime(latest.LastOccurredAt))
		body = prefix + lipgloss.NewStyle().Background(colorBg).Render(summary) + hint
	}

	if p.width > 0 {
		body = truncateLine(body, p.width)
	}
	return notificationsPanelStyle.Render(padLine(body, p.width, colorBg))
}

func (p *notificationsPanel) renderExpanded() string {
	title := "Notifications"
	if p.unread > 0 {
		title = fmt.Sprintf("Notifications (%d unread)", p.unread)
	}
	headerL := notificationsPanelPrefixStyle.Render("  " + title)
	headerR := notificationsPanelHintStyle.Render("n collapse · j/k navigate · r mark read · enter open  ")
	header := joinHeaderLine(headerL, headerR, p.width)

	body := p.viewport.View()
	// Pad the viewport's content area so the background colour is consistent.
	bodyLines := strings.Split(body, "\n")
	for i, line := range bodyLines {
		bodyLines[i] = padLine(line, p.width, colorBg)
	}
	body = strings.Join(bodyLines, "\n")

	spacer := padLine("", p.width, colorBg)
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
	if len(p.ordered) == 0 {
		p.viewport.SetContent(notificationsPanelHintStyle.Render("  No notifications yet."))
		return
	}
	lines := make([]string, 0, len(p.ordered))
	for i, id := range p.ordered {
		n := p.items[id]
		lines = append(lines, p.renderRow(n, i == p.cursor))
	}
	p.viewport.SetContent(strings.Join(lines, "\n"))
}

func (p *notificationsPanel) renderRow(n apiclient.Notification, selected bool) string {
	bg := colorBg
	if selected {
		bg = colorBgLight
	}
	indicator := "  "
	if selected {
		indicator = "▸ "
	}
	sev := notificationSeverityStyle(n.Severity).Background(bg).Render("●")

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
	return padLine(line, p.viewport.Width, bg)
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

// relativeTime returns the same buckets the rhythm phrase uses, but stripped
// to a single token suitable for inline rendering. Mirrors the Go and TS
// rhythm helpers.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < 30*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
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

var (
	notificationsPanelStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorTextDim)

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
