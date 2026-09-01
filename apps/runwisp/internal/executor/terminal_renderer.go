// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// TerminalRenderer interprets a single output stream's bytes as a simplified
// terminal would, so that carriage-return overwrites (progress bars) and
// multi-line ANSI redraws turn into clean, faithful log lines instead of raw
// escape soup.
//
// It is fed raw byte chunks via Write and emits two kinds of output through its
// callbacks:
//
//   - onCommit(lines, frames): a finalized commit group. lines are durable and
//     each gets a permanent absolute line number from the caller. A plain
//     forward line commits as a group of one on '\n'; a multi-line redraw
//     commits all its region rows as one group when it settles (screen clear,
//     EOF). frames is the throttled history of whole-region snapshots the group
//     animated through before settling (nil for plain output) — the caller
//     persists it keyed to the group's first line number so an operator can
//     rewind a progress bar / redraw. The committed (final) state is not
//     repeated in frames.
//   - onProvisional(epoch, rows): the current state of the live region (the
//     not-yet-finalized tail / redraw area) for in-place display. These are
//     ephemeral, never written to disk. rows[i] is region row i; the whole
//     snapshot replaces any previous one for the same epoch. epoch increments
//     when the region is reset (screen clear / alt-screen), so a stale snapshot
//     can never paint over a fresh region.
//
// One renderer instance handles one stream (stdout or stderr); they are
// independent because RunWisp captures them as separate byte streams.
type TerminalRenderer struct {
	onCommit      func(lines []committedLine, frames [][]string)
	onProvisional func(epoch int, rows []string)
	nowMs         func() int64 // wall-clock ms source for history throttling

	pending []byte       // bytes of an incomplete sequence/grapheme carried across Write calls
	parser  *ansi.Parser // collects CSI/ESC command + params during decode

	rows   []*termRow // the live region; rows[0] is the top
	curRow int        // cursor row index into rows
	curCol int        // cursor column (cell index) within the current row

	savedRow, savedCol int
	hasSaved           bool

	// screenLocked is true once the stream has moved the cursor up or addressed
	// an absolute position — i.e. it is redrawing in place across multiple rows.
	// While locked, '\n' no longer finalizes rows; the whole region is retained
	// and rendered as a live overlay until it scrolls past maxRegionRows, the
	// screen is cleared, or the stream ends.
	screenLocked bool

	// curPen is the interned id of the SGR state applied to subsequently written
	// cells. pen 0 is the default (no styling).
	curPen   uint16
	penText  string // raw accumulated SGR string for curPen (best-effort round-trip)
	pens     []string
	penIndex map[string]uint16

	// pendingContinued marks that the next finalized line continues an oversized
	// line that was force-committed without a newline (mirrors the old
	// LineBuffer / events.Continued contract).
	pendingContinued bool

	epoch       int
	dirty       bool // region changed since the last provisional snapshot
	lastSnapLen int  // rows in the last provisional snapshot (to detect shrink)

	// regionFrames is the throttled history of whole-region snapshots since the
	// current commit group started — the prior states a `\r` bar or multi-line
	// redraw passed through. It is attached to the next commit group and reset.
	regionFrames    [][]string
	lastFrameMs     int64 // nowMs of the last captured frame
	frameIntervalMs int64 // min ms between captures; doubles on decimation
}

// committedLine is one finalized line in a commit group: its rendered text plus
// the oversized-split Continued flag.
type committedLine struct {
	text      string
	continued bool
}

type termCell struct {
	r   rune
	pen uint16
}

type termRow struct {
	cells []termCell
}

const (
	// maxRegionRows bounds how tall an in-place redraw region may grow before
	// the topmost row scrolls out and is finalized. It is the supported
	// multi-line redraw depth and the cap on uncommitted (in-memory) rows.
	maxRegionRows = 64
	// maxRowCells caps a single row before it is force-committed as an oversized
	// split (mirrors MaxLineBufferSize for the old line buffer).
	maxRowCells = MaxLineBufferSize
	// maxPenLen bounds the accumulated SGR string so a stream that never resets
	// its colours cannot grow it without limit.
	maxPenLen = 256
	// maxHistoryFrames caps how many prior frames one commit group retains. On
	// overflow the history is decimated (every other frame kept), preserving the
	// full timespan at half the resolution instead of dropping the start.
	maxHistoryFrames = 40
	// defaultFrameIntervalMs is the minimum wall-clock gap between captured
	// frames. It collapses an instant burst (a bar that prints every percentage
	// in one chunk) to ~1 frame while sampling a real animation about 4×/second.
	defaultFrameIntervalMs = 250
)

// NewTerminalRenderer creates a renderer for one stream. onCommit must not be
// nil; onProvisional may be nil if the caller does not stream a live overlay.
// nowMs supplies wall-clock milliseconds for history throttling; nil defaults
// to time.Now so callers that don't inject a clock still behave sanely.
func NewTerminalRenderer(onCommit func(lines []committedLine, frames [][]string), onProvisional func(epoch int, rows []string), nowMs func() int64) *TerminalRenderer {
	if nowMs == nil {
		nowMs = func() int64 { return time.Now().UnixMilli() }
	}
	return &TerminalRenderer{
		onCommit:        onCommit,
		onProvisional:   onProvisional,
		nowMs:           nowMs,
		parser:          ansi.NewParser(),
		rows:            []*termRow{{}},
		penIndex:        map[string]uint16{},
		frameIntervalMs: defaultFrameIntervalMs,
	}
}

// Write feeds a chunk of raw stream bytes. Incomplete escape sequences or UTF-8
// graphemes at the end of the chunk are buffered and combined with the next
// chunk.
func (tr *TerminalRenderer) Write(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	tr.pending = append(tr.pending, chunk...)
	b := tr.pending
	off := 0
	for off < len(b) {
		seq, _, n, newState := ansi.DecodeSequence(b[off:], ansi.NormalState, tr.parser)
		if n == 0 {
			break
		}
		atEnd := off+n >= len(b)
		if atEnd && newState != ansi.NormalState {
			break // incomplete escape sequence; wait for more bytes
		}
		if atEnd && len(seq) > 0 && seq[0] >= 0x80 && !utf8.Valid(seq) {
			break // incomplete trailing UTF-8 grapheme; wait for more bytes
		}
		tr.handleToken(seq)
		off += n
	}
	tr.pending = append(tr.pending[:0], b[off:]...)
	tr.emitProvisional()
}

// Close flushes the renderer at end of stream: any buffered incomplete bytes
// are treated as printable, then every remaining region row is finalized.
func (tr *TerminalRenderer) Close() {
	if len(tr.pending) > 0 {
		for _, r := range string(tr.pending) {
			tr.writeRune(r)
		}
		tr.pending = tr.pending[:0]
	}
	tr.commitAll()
	tr.rows = []*termRow{{}}
	tr.curRow, tr.curCol = 0, 0
	if tr.onProvisional != nil && tr.lastSnapLen > 0 {
		tr.onProvisional(tr.epoch, nil)
		tr.lastSnapLen = 0
	}
}

// commitAll finalizes every region row in order, dropping a trailing run of
// empty rows (a fresh post-newline tail or cursor-rest row is not a real line).
func (tr *TerminalRenderer) commitAll() {
	end := len(tr.rows)
	for end > 0 && len(tr.rows[end-1].cells) == 0 {
		end--
	}
	if end == 0 {
		// Region settled with nothing drawn: no line to anchor history to.
		tr.takeFrames(nil)
		return
	}
	tr.commitRows(tr.rows[:end], true)
}

func (tr *TerminalRenderer) handleToken(seq []byte) {
	if len(seq) == 0 {
		return
	}
	switch {
	case ansi.HasCsiPrefix(seq):
		tr.handleCSI()
	case ansi.HasOscPrefix(seq), ansi.HasDcsPrefix(seq), ansi.HasApcPrefix(seq),
		ansi.HasSosPrefix(seq), ansi.HasPmPrefix(seq):
		// String sequences (titles, hyperlinks, etc): consume and ignore.
	case ansi.HasEscPrefix(seq):
		tr.handleESC(seq)
	case len(seq) == 1 && (seq[0] < 0x20 || seq[0] == 0x7f):
		tr.handleControl(seq[0])
	default:
		for _, r := range string(seq) {
			tr.writeRune(r)
		}
	}
}

func (tr *TerminalRenderer) handleControl(c byte) {
	switch c {
	case '\n':
		tr.newline()
	case '\r':
		tr.recordRegionFrame()
		tr.curCol = 0
	case '\b':
		if tr.curCol > 0 {
			tr.curCol--
		}
	case '\t':
		next := (tr.curCol/8 + 1) * 8
		for tr.curCol < next {
			tr.writeRune(' ')
		}
	}
	// Other C0/C1 controls are ignored.
}

func (tr *TerminalRenderer) handleESC(seq []byte) {
	if len(seq) < 2 {
		return
	}
	switch seq[len(seq)-1] {
	case '7': // DECSC save cursor
		tr.saveCursor()
	case '8': // DECRC restore cursor
		tr.restoreCursor()
	case 'c': // RIS full reset
		tr.resetScreen()
	}
}

func (tr *TerminalRenderer) handleCSI() {
	cmd := ansi.Cmd(tr.parser.Command())
	if cmd.Prefix() != 0 {
		// Private DEC modes. We act on a few that signal in-place redraw intent.
		if cmd.Prefix() == '?' && (cmd.Final() == 'h' || cmd.Final() == 'l') {
			tr.handlePrivateMode(cmd.Final() == 'h')
		}
		return
	}
	switch cmd.Final() {
	case 'A': // cursor up
		tr.cursorUp(tr.param(0, 1))
	case 'B': // cursor down
		tr.cursorDown(tr.param(0, 1))
	case 'C': // cursor forward
		tr.curCol += tr.param(0, 1)
	case 'D': // cursor back
		tr.curCol -= tr.param(0, 1)
		if tr.curCol < 0 {
			tr.curCol = 0
		}
	case 'E': // cursor next line
		tr.cursorDown(tr.param(0, 1))
		tr.curCol = 0
	case 'F': // cursor previous line
		tr.cursorUp(tr.param(0, 1))
		tr.curCol = 0
	case 'G': // cursor horizontal absolute (1-based)
		tr.curCol = max(0, tr.param(0, 1)-1)
	case 'H', 'f': // cursor position row;col (1-based)
		tr.cursorPos(tr.param(0, 1), tr.param(1, 1))
	case 'J': // erase in display
		tr.eraseDisplay(tr.param(0, 0))
	case 'K': // erase in line
		tr.eraseLine(tr.param(0, 0))
	case 'm': // SGR
		tr.applySGR()
	case 's': // save cursor (ANSI.SYS)
		tr.saveCursor()
	case 'u': // restore cursor (ANSI.SYS)
		tr.restoreCursor()
	}
}

// handlePrivateMode acts on the private DEC modes that signal in-place redraw
// intent (alt-screen enter and cursor show/hide). set is true for `h` (enable),
// false for `l` (disable).
func (tr *TerminalRenderer) handlePrivateMode(set bool) {
	for i := 0; ; i++ {
		p, ok := tr.parser.Param(i, 0)
		if !ok {
			break
		}
		switch {
		case (p == 1049 || p == 47 || p == 1047) && set:
			// Entering the alternate screen: clear and redraw in place.
			tr.resetScreen()
			tr.screenLocked = true
		case (p == 1049 || p == 47 || p == 1047) && !set:
			// Exiting the alternate screen: finalize whatever it left showing
			// and return to durable forward output on the main screen,
			// symmetric with entry. Without this the region stays locked
			// forever and everything printed afterwards is only ever
			// ephemeral (provisional), never persisted.
			tr.resetScreen()
		case p == 25 && !set:
			// Hiding the cursor almost always precedes an in-place redraw.
			tr.screenLocked = true
		case p == 25 && set:
			// Showing the cursor again typically ends the redraw: finalize the
			// region and return to durable forward output.
			tr.resetScreen()
		}
	}
}

func (tr *TerminalRenderer) param(i, def int) int {
	v, _ := tr.parser.Param(i, def)
	if v < 0 {
		return def
	}
	return v
}

// --- cursor / region operations ---

func (tr *TerminalRenderer) cur() *termRow {
	for tr.curRow >= len(tr.rows) {
		tr.rows = append(tr.rows, &termRow{})
	}
	return tr.rows[tr.curRow]
}

func (tr *TerminalRenderer) writeRune(r rune) {
	row := tr.cur()
	for len(row.cells) < tr.curCol {
		row.cells = append(row.cells, termCell{r: ' '})
	}
	if tr.curCol < len(row.cells) {
		row.cells[tr.curCol] = termCell{r: r, pen: tr.curPen}
	} else {
		row.cells = append(row.cells, termCell{r: r, pen: tr.curPen})
	}
	tr.curCol++
	tr.dirty = true
	if len(row.cells) > maxRowCells {
		// Oversized line: force-commit and continue on a fresh row. No history —
		// this is a hard split mid-line, not a settled redraw.
		tr.commitRows([]*termRow{row}, false)
		tr.pendingContinued = true
		*row = termRow{}
		tr.curCol = 0
	}
}

func (tr *TerminalRenderer) newline() {
	if !tr.screenLocked {
		// Forward mode: finalize the row we are leaving and reuse it as the new
		// empty tail. The region stays a single live row (the unterminated tail).
		row := tr.rows[tr.curRow]
		tr.commitRows([]*termRow{row}, true)
		tr.pendingContinued = false
		*row = termRow{}
		tr.curCol = 0
		tr.dirty = true
		return
	}
	// Locked (redraw) mode: move down a row, scrolling the region if it overflows.
	tr.curRow++
	tr.curCol = 0
	tr.cur()
	tr.scrollIfNeeded()
	tr.dirty = true
}

func (tr *TerminalRenderer) scrollIfNeeded() {
	for len(tr.rows) > maxRegionRows {
		// Scroll-off: emit the top row mid-redraw. No history attached and the
		// frame buffer is left intact so the region that survives keeps animating.
		tr.commitRows(tr.rows[:1], false)
		tr.rows = tr.rows[1:]
		if tr.curRow > 0 {
			tr.curRow--
		}
	}
}

func (tr *TerminalRenderer) cursorUp(n int) {
	tr.recordRegionFrame()
	tr.screenLocked = true
	tr.curRow -= n
	if tr.curRow < 0 {
		tr.curRow = 0
	}
	tr.dirty = true
}

func (tr *TerminalRenderer) cursorDown(n int) {
	tr.recordRegionFrame()
	tr.screenLocked = true
	tr.curRow += n
	tr.cur()
	tr.scrollIfNeeded()
	tr.dirty = true
}

func (tr *TerminalRenderer) cursorPos(row, col int) {
	tr.recordRegionFrame()
	tr.screenLocked = true
	tr.curRow = max(0, row-1)
	tr.curCol = max(0, col-1)
	tr.cur()
	tr.scrollIfNeeded()
	tr.dirty = true
}

func (tr *TerminalRenderer) saveCursor() {
	tr.savedRow, tr.savedCol = tr.curRow, tr.curCol
	tr.hasSaved = true
}

func (tr *TerminalRenderer) restoreCursor() {
	if !tr.hasSaved {
		return
	}
	tr.recordRegionFrame()
	tr.screenLocked = true
	tr.curRow, tr.curCol = tr.savedRow, tr.savedCol
	tr.cur()
	tr.dirty = true
}

func (tr *TerminalRenderer) eraseLine(mode int) {
	tr.recordRegionFrame()
	row := tr.cur()
	switch mode {
	case 0: // cursor to end
		if tr.curCol < len(row.cells) {
			row.cells = row.cells[:tr.curCol]
		}
	case 1: // start to cursor
		for i := 0; i < tr.curCol && i < len(row.cells); i++ {
			row.cells[i] = termCell{r: ' '}
		}
	case 2: // whole line
		row.cells = row.cells[:0]
	}
	tr.dirty = true
}

func (tr *TerminalRenderer) eraseDisplay(mode int) {
	tr.recordRegionFrame()
	switch mode {
	case 0: // cursor to end of screen
		tr.eraseLine(0)
		if tr.curRow+1 < len(tr.rows) {
			tr.rows = tr.rows[:tr.curRow+1]
		}
	case 1: // start of screen to cursor
		tr.eraseLine(1)
		for i := 0; i < tr.curRow; i++ {
			tr.rows[i] = &termRow{}
		}
	default: // 2 or 3: whole screen
		tr.resetScreen()
	}
	tr.dirty = true
}

// resetScreen finalizes whatever is currently visible, then starts a fresh
// region under a new epoch so a clear/redraw does not lose the last shown state.
func (tr *TerminalRenderer) resetScreen() {
	tr.commitAll()
	tr.rows = []*termRow{{}}
	tr.curRow, tr.curCol = 0, 0
	tr.screenLocked = false
	tr.hasSaved = false
	tr.epoch++
	tr.dirty = true
}

func (tr *TerminalRenderer) applySGR() {
	// Reset (CSI m or CSI 0 m) clears the pen; anything else accumulates.
	first, ok := tr.parser.Param(0, 0)
	if !ok || (first == 0 && !hasSecondParam(tr.parser)) {
		tr.penText = ""
		tr.curPen = 0
		return
	}
	raw := encodeSGR(tr.parser)
	if first == 0 {
		tr.penText = raw
	} else {
		tr.penText += raw
		if len(tr.penText) > maxPenLen {
			tr.penText = raw
		}
	}
	tr.curPen = tr.internPen(tr.penText)
}

func hasSecondParam(p *ansi.Parser) bool {
	_, ok := p.Param(1, -1)
	return ok
}

// encodeSGR rebuilds the raw SGR escape ("\x1b[...m") from the parser's params.
func encodeSGR(p *ansi.Parser) string {
	var b strings.Builder
	b.WriteString("\x1b[")
	for i := 0; ; i++ {
		v, ok := p.Param(i, 0)
		if !ok {
			break
		}
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(strconv.Itoa(v))
	}
	b.WriteByte('m')
	return b.String()
}

func (tr *TerminalRenderer) internPen(s string) uint16 {
	if s == "" {
		return 0
	}
	if id, ok := tr.penIndex[s]; ok {
		return id
	}
	if len(tr.pens) >= 1<<16-2 {
		return 0 // pen table exhausted; degrade to default
	}
	tr.pens = append(tr.pens, s)
	id := uint16(len(tr.pens)) // 0 is reserved for default, so ids start at 1
	tr.penIndex[s] = id
	return id
}

func (tr *TerminalRenderer) penString(id uint16) string {
	if id == 0 || int(id) > len(tr.pens) {
		return ""
	}
	return tr.pens[id-1]
}

// --- commit & provisional emission ---

// commitRows finalizes rows as a single commit group. When withHistory is set,
// the accumulated whole-region frame history is attached (and reset) — used when
// a `\r` line or a multi-line redraw settles. Scroll-off and oversized splits
// pass withHistory=false: they emit a line mid-animation, so the region and its
// still-growing history must survive into the eventual settling commit.
func (tr *TerminalRenderer) commitRows(rows []*termRow, withHistory bool) {
	lines := make([]committedLine, len(rows))
	finalRows := make([]string, len(rows))
	for i, row := range rows {
		t := tr.renderRow(row)
		lines[i] = committedLine{text: t, continued: tr.pendingContinued}
		finalRows[i] = t
	}
	var frames [][]string
	if withHistory {
		frames = tr.takeFrames(finalRows)
	}
	tr.onCommit(lines, frames)
}

// recordRegionFrame snapshots the whole current region into the frame history,
// throttled to one capture per frameIntervalMs. It is called at the top of every
// destructive op (carriage return, cursor move, erase) before the mutation, so
// the history holds the states the region passed through. Instant bursts (a bar
// that prints every percentage in one chunk) collapse to ~1 frame; real
// animations are sampled about 4×/second.
func (tr *TerminalRenderer) recordRegionFrame() {
	now := tr.nowMs()
	if len(tr.regionFrames) > 0 && now-tr.lastFrameMs < tr.frameIntervalMs {
		return
	}
	snap := tr.snapshotRows()
	if len(snap) == 0 {
		return
	}
	if n := len(tr.regionFrames); n > 0 && equalRows(tr.regionFrames[n-1], snap) {
		return
	}
	tr.lastFrameMs = now
	tr.regionFrames = append(tr.regionFrames, snap)
	if len(tr.regionFrames) > maxHistoryFrames {
		tr.decimateFrames()
	}
}

// snapshotRows renders every current region row, dropping a trailing run of
// empty rows so a snapshot reflects only the drawn area.
func (tr *TerminalRenderer) snapshotRows() []string {
	out := make([]string, 0, len(tr.rows))
	for _, row := range tr.rows {
		out = append(out, tr.renderRow(row))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// decimateFrames halves the retained history (keeping every other frame) and
// doubles the capture interval, preserving the full timespan at lower resolution
// instead of dropping the oldest frames once the cap is hit.
func (tr *TerminalRenderer) decimateFrames() {
	kept := make([][]string, 0, len(tr.regionFrames)/2+1)
	for i := 0; i < len(tr.regionFrames); i += 2 {
		kept = append(kept, tr.regionFrames[i])
	}
	tr.regionFrames = kept
	tr.frameIntervalMs *= 2
}

// takeFrames returns the captured history for a settling commit group and resets
// the capture state for the next group. The trailing frame(s) equal to the
// committed final rows are dropped — the final state is not a "prior" state.
func (tr *TerminalRenderer) takeFrames(finalRows []string) [][]string {
	frames := tr.regionFrames
	tr.regionFrames = nil
	tr.lastFrameMs = 0
	tr.frameIntervalMs = defaultFrameIntervalMs
	for len(frames) > 0 && equalRows(frames[len(frames)-1], finalRows) {
		frames = frames[:len(frames)-1]
	}
	if len(frames) == 0 {
		return nil
	}
	return frames
}

func equalRows(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (tr *TerminalRenderer) renderRow(row *termRow) string {
	end := len(row.cells)
	for end > 0 && row.cells[end-1].pen == 0 && (row.cells[end-1].r == ' ' || row.cells[end-1].r == 0) {
		end--
	}
	var b strings.Builder
	last := uint16(0)
	for i := 0; i < end; i++ {
		c := row.cells[i]
		if c.pen != last {
			if c.pen == 0 {
				b.WriteString("\x1b[0m")
			} else {
				b.WriteString(tr.penString(c.pen))
			}
			last = c.pen
		}
		r := c.r
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	if last != 0 {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func (tr *TerminalRenderer) emitProvisional() {
	if tr.onProvisional == nil || !tr.dirty {
		return
	}
	tr.dirty = false
	snap := make([]string, 0, len(tr.rows))
	for _, row := range tr.rows {
		snap = append(snap, tr.renderRow(row))
	}
	// Drop a trailing empty tail row so a fully-committed forward line does not
	// leave a blank overlay row.
	for len(snap) > 0 && snap[len(snap)-1] == "" {
		snap = snap[:len(snap)-1]
	}
	if len(snap) == 0 && tr.lastSnapLen == 0 {
		return
	}
	tr.lastSnapLen = len(snap)
	tr.onProvisional(tr.epoch, snap)
}
