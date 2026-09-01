// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"strconv"
	"strings"
	"testing"
)

// commitGroup is one finalized commit group recorded by the harness: the
// group's lines plus the whole-region frame history attached to it.
type commitGroupRecord struct {
	lines  []string
	frames [][]string
}

// rendererHarness drives a TerminalRenderer and records what it emits.
type rendererHarness struct {
	tr        *TerminalRenderer
	committed []string // all committed lines, flattened across groups
	continued []bool
	groups    []commitGroupRecord
	lastProv  []string // most recent provisional snapshot
	provEpoch int
	nowMsVal  int64 // fake wall clock for history throttling; advance via tick
}

func newHarness() *rendererHarness {
	h := &rendererHarness{}
	h.tr = NewTerminalRenderer(
		func(lines []committedLine, frames [][]string) {
			g := commitGroupRecord{frames: frames}
			for _, l := range lines {
				h.committed = append(h.committed, l.text)
				h.continued = append(h.continued, l.continued)
				g.lines = append(g.lines, l.text)
			}
			h.groups = append(h.groups, g)
		},
		func(epoch int, rows []string) {
			h.provEpoch = epoch
			h.lastProv = append([]string(nil), rows...)
		},
		func() int64 { return h.nowMsVal },
	)
	return h
}

// tick advances the harness's fake clock by ms, so history throttling can be
// exercised deterministically.
func (h *rendererHarness) tick(ms int64) { h.nowMsVal += ms }

// framesFor returns the frame history attached to the commit group whose first
// line equals text, and whether such a group was found.
func (h *rendererHarness) framesFor(text string) ([][]string, bool) {
	for _, g := range h.groups {
		if len(g.lines) > 0 && g.lines[0] == text {
			return g.frames, true
		}
	}
	return nil, false
}

// feed writes the input split into the given chunk size (0 = one shot).
func (h *rendererHarness) feed(s string, chunk int) {
	b := []byte(s)
	if chunk <= 0 {
		chunk = len(b)
	}
	for i := 0; i < len(b); i += chunk {
		end := min(i+chunk, len(b))
		h.tr.Write(b[i:end])
	}
}

func eq(a, b []string) bool {
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

func TestRendererForwardLines(t *testing.T) {
	h := newHarness()
	h.feed("alpha\nbeta\ngamma\n", 0)
	if !eq(h.committed, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("commits = %q", h.committed)
	}
	h.tr.Close()
	if !eq(h.committed, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("after close commits = %q", h.committed)
	}
}

func TestRendererPartialTailVisibleThenCommitsOnClose(t *testing.T) {
	h := newHarness()
	h.feed("done line\npartial", 0)
	if !eq(h.committed, []string{"done line"}) {
		t.Fatalf("commits before close = %q", h.committed)
	}
	if !eq(h.lastProv, []string{"partial"}) {
		t.Fatalf("provisional tail = %q", h.lastProv)
	}
	h.tr.Close()
	if !eq(h.committed, []string{"done line", "partial"}) {
		t.Fatalf("commits after close = %q", h.committed)
	}
	if len(h.lastProv) != 0 {
		t.Fatalf("overlay should be cleared after close, got %q", h.lastProv)
	}
}

func TestRendererCarriageReturnSingleLine(t *testing.T) {
	h := newHarness()
	h.feed("build: 10%\rbuild: 55%\rbuild: 100%\n", 0)
	if !eq(h.committed, []string{"build: 100%"}) {
		t.Fatalf("CR bar should commit only final frame, got %q", h.committed)
	}
}

func TestRendererCarriageReturnNoFinalNewline(t *testing.T) {
	h := newHarness()
	h.feed("10%\r100%", 0)
	if !eq(h.lastProv, []string{"100%"}) {
		t.Fatalf("provisional = %q", h.lastProv)
	}
	if len(h.committed) != 0 {
		t.Fatalf("nothing should commit before close, got %q", h.committed)
	}
	h.tr.Close()
	if !eq(h.committed, []string{"100%"}) {
		t.Fatalf("commit after close = %q", h.committed)
	}
}

func TestRendererCRLF(t *testing.T) {
	h := newHarness()
	h.feed("one\r\ntwo\r\n", 0)
	if !eq(h.committed, []string{"one", "two"}) {
		t.Fatalf("CRLF commits = %q", h.committed)
	}
}

func TestRendererCRShorterOverwrite(t *testing.T) {
	// "Downloading........." then "\rDone" must not leave trailing old chars.
	h := newHarness()
	h.feed("Downloading.........\rDone\n", 0)
	got := h.committed
	// "Done" overwrites the first 4 cells; the rest of the old text remains.
	want := "Done" + "Downloading........."[4:]
	if len(got) != 1 || got[0] != want {
		t.Fatalf("overwrite result = %q, want %q", got, want)
	}
}

func TestRendererEraseLineAfterCR(t *testing.T) {
	// Progress tools usually clear the line: "old text\r\x1b[KDone\n".
	h := newHarness()
	h.feed("old text\r\x1b[KDone\n", 0)
	if !eq(h.committed, []string{"Done"}) {
		t.Fatalf("erase-line commit = %q", h.committed)
	}
}

func TestRendererSGRColorRoundTrip(t *testing.T) {
	h := newHarness()
	h.feed("\x1b[31mred\x1b[0m plain\n", 0)
	if len(h.committed) != 1 {
		t.Fatalf("commits = %q", h.committed)
	}
	if h.committed[0] != "\x1b[31mred\x1b[0m plain" {
		t.Fatalf("SGR round-trip = %q", h.committed[0])
	}
}

func TestRendererMultiLineRedraw(t *testing.T) {
	// A tool that hides the cursor, draws three rows, moves back up, rewrites
	// them, then shows the cursor. Should commit the final frame of each row.
	var b strings.Builder
	b.WriteString("\x1b[?25l")        // hide cursor -> redraw session
	b.WriteString("a 0%\nb 0%\nc 0%") // three rows (cursor on row c)
	b.WriteString("\x1b[2A\r")        // up to row a, col 0
	b.WriteString("a 100%")           // rewrite a
	b.WriteString("\x1b[1B\r")        // down to row b
	b.WriteString("b 100%")           // rewrite b
	b.WriteString("\x1b[1B\r")        // down to row c
	b.WriteString("c 100%")           // rewrite c
	b.WriteString("\x1b[?25h")        // show cursor -> finalize

	h := newHarness()
	h.feed(b.String(), 0)
	if !eq(h.committed, []string{"a 100%", "b 100%", "c 100%"}) {
		t.Fatalf("multi-line redraw commits = %q", h.committed)
	}
}

func TestRendererMultiLineProvisionalSnapshot(t *testing.T) {
	var b strings.Builder
	b.WriteString("\x1b[?25l")
	b.WriteString("a 0%\nb 0%\nc 50%")
	h := newHarness()
	h.feed(b.String(), 0)
	if !eq(h.lastProv, []string{"a 0%", "b 0%", "c 50%"}) {
		t.Fatalf("provisional region = %q", h.lastProv)
	}
	if len(h.committed) != 0 {
		t.Fatalf("nothing committed mid-redraw, got %q", h.committed)
	}
}

func TestRendererChunkBoundarySplitsSequence(t *testing.T) {
	// Feed one byte at a time so escape sequences and the bar straddle chunks.
	h := newHarness()
	h.feed("\x1b[32mok\x1b[0m\rDONE\n", 1)
	if len(h.committed) != 1 {
		t.Fatalf("commits = %q", h.committed)
	}
	if h.committed[0] != "DONE" {
		t.Fatalf("byte-split render = %q", h.committed[0])
	}
}

func TestRendererUTF8AcrossChunks(t *testing.T) {
	h := newHarness()
	// "héllo\n" with the two bytes of é split across chunks of size 2.
	h.feed("héllo\n", 2)
	if !eq(h.committed, []string{"héllo"}) {
		t.Fatalf("utf8 commit = %q", h.committed)
	}
}

func TestRendererOversizedLineSplits(t *testing.T) {
	h := newHarness()
	big := strings.Repeat("x", maxRowCells+10)
	h.feed(big+"\n", 0)
	if len(h.committed) != 2 {
		t.Fatalf("expected oversized split into 2 commits, got %d", len(h.committed))
	}
	if h.continued[0] {
		t.Fatalf("first segment must not be marked continued")
	}
	if !h.continued[1] {
		t.Fatalf("second segment must be marked continued")
	}
}

func TestRendererClearScreenBumpsEpoch(t *testing.T) {
	h := newHarness()
	h.feed("before\x1b[2Jafter\n", 0)
	if h.tr.epoch == 0 {
		t.Fatalf("epoch should have advanced after clear screen")
	}
	// "before" is finalized by the clear; "after" commits on its newline.
	if !eq(h.committed, []string{"before", "after"}) {
		t.Fatalf("clear-screen commits = %q", h.committed)
	}
}

func framesEqual(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !eq(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestRendererCRBarRecordsPriorFrames(t *testing.T) {
	h := newHarness()
	// A bar that ticks slowly enough (past the throttle interval) to record each
	// state it passed through, settling on the final committed line.
	h.tr.Write([]byte("build 0%\r"))
	h.tick(300)
	h.tr.Write([]byte("build 50%\r"))
	h.tick(300)
	h.tr.Write([]byte("build 100%\n"))

	if !eq(h.committed, []string{"build 100%"}) {
		t.Fatalf("CR bar commit = %q", h.committed)
	}
	frames, ok := h.framesFor("build 100%")
	if !ok {
		t.Fatalf("no commit group for final line")
	}
	want := [][]string{{"build 0%"}, {"build 50%"}}
	if !framesEqual(frames, want) {
		t.Fatalf("CR bar frames = %v, want %v", frames, want)
	}
}

func TestRendererCRBarBurstCollapsesToOneFrame(t *testing.T) {
	h := newHarness()
	// Every percentage arrives in one chunk at the same instant: no animation
	// actually happened on screen, so history must collapse to at most one frame.
	h.tr.Write([]byte("0%\r25%\r50%\r75%\r100%\n"))
	frames, ok := h.framesFor("100%")
	if !ok {
		t.Fatalf("no commit group for final line")
	}
	if len(frames) > 1 {
		t.Fatalf("instant burst should collapse to <=1 frame, got %d: %v", len(frames), frames)
	}
}

func TestRendererMultiLineRedrawRecordsWholeFrames(t *testing.T) {
	var first strings.Builder
	first.WriteString("\x1b[?25l")        // hide cursor -> redraw session
	first.WriteString("a 0%\nb 0%\nc 0%") // three rows, cursor on row c

	var second strings.Builder
	second.WriteString("\x1b[2A\r") // up to row a (captures the whole 0% frame)
	second.WriteString("a 100%")
	second.WriteString("\x1b[1B\r")
	second.WriteString("b 100%")
	second.WriteString("\x1b[1B\r")
	second.WriteString("c 100%")
	second.WriteString("\x1b[?25h") // show cursor -> finalize

	h := newHarness()
	h.tr.Write([]byte(first.String()))
	h.tick(300)
	h.tr.Write([]byte(second.String()))

	if !eq(h.committed, []string{"a 100%", "b 100%", "c 100%"}) {
		t.Fatalf("redraw commits = %q", h.committed)
	}
	// History is keyed to the group's first (anchor) line and holds the whole
	// region per frame, not per-row timelines.
	frames, ok := h.framesFor("a 100%")
	if !ok {
		t.Fatalf("no commit group anchored at first redraw line")
	}
	want := [][]string{{"a 0%", "b 0%", "c 0%"}}
	if !framesEqual(frames, want) {
		t.Fatalf("multi-line frames = %v, want %v", frames, want)
	}
}

func TestRendererFrameHistoryCappedAndDecimated(t *testing.T) {
	h := newHarness()
	// Far more distinct states than the cap, each well past any decimated
	// interval, so capture always fires and the cap/decimation path is hit.
	for i := 0; i < 400; i++ {
		h.tr.Write([]byte(strconv.Itoa(i) + "%\r"))
		h.tick(10000)
	}
	h.tr.Write([]byte("done\n"))

	frames, ok := h.framesFor("done")
	if !ok {
		t.Fatalf("no commit group for final line")
	}
	if len(frames) == 0 {
		t.Fatalf("expected some recorded frames")
	}
	if len(frames) > maxHistoryFrames {
		t.Fatalf("frame history exceeded cap: %d > %d", len(frames), maxHistoryFrames)
	}
}

func TestRendererForwardLinesHaveNoFrames(t *testing.T) {
	h := newHarness()
	h.feed("plain output line\nanother line\n", 0)
	for _, g := range h.groups {
		if len(g.frames) != 0 {
			t.Fatalf("plain forward line %q should have no frames, got %v", g.lines, g.frames)
		}
	}
}

func TestRendererPartialLineHasNoFrames(t *testing.T) {
	h := newHarness()
	h.feed("partial with no newline", 0)
	h.tr.Close()
	frames, ok := h.framesFor("partial with no newline")
	if !ok {
		t.Fatalf("partial line should commit on close")
	}
	if len(frames) != 0 {
		t.Fatalf("partial line should have no frame history, got %v", frames)
	}
}

func TestRendererIgnoresUnhandledSequences(t *testing.T) {
	h := newHarness()
	// OSC title set + bracketed-paste mode should be dropped, not rendered.
	h.feed("\x1b]0;my title\x07hello\x1b[?2004h world\n", 0)
	if !eq(h.committed, []string{"hello world"}) {
		t.Fatalf("unhandled-sequence render = %q", h.committed)
	}
}

func TestRendererBackspaceAndTab(t *testing.T) {
	h := newHarness()
	// Backspace moves the cursor left so the next write overwrites in place.
	h.feed("abc\b\bX\n", 0)
	if !eq(h.committed, []string{"aXc"}) {
		t.Fatalf("backspace render = %q", h.committed)
	}
	// Tab advances to the next 8-column stop, padding with spaces.
	h2 := newHarness()
	h2.feed("a\tb\n", 0)
	if !eq(h2.committed, []string{"a       b"}) {
		t.Fatalf("tab render = %q", h2.committed)
	}
}

func TestRendererCursorHorizontalMoves(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"forward", "ab\x1b[2Cc\n", "ab  c"},  // CSI C: cursor forward (pads)
		{"back", "abcd\x1b[2DX\n", "abXd"},    // CSI D: cursor back
		{"absolute", "abc\x1b[1GX\n", "Xbc"},  // CSI G: column absolute (1-based)
		{"back-clamps", "ab\x1b[9DX\n", "Xb"}, // CSI D past col 0 clamps to 0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			h.feed(tc.in, 0)
			if !eq(h.committed, []string{tc.want}) {
				t.Fatalf("%s render = %q, want %q", tc.name, h.committed, tc.want)
			}
		})
	}
}

func TestRendererSaveRestoreCursor(t *testing.T) {
	// DECSC/DECRC (ESC 7 / ESC 8): save the cursor, write ahead, then restore and
	// overwrite at the saved position. Restoring locks the region (it is a redraw),
	// so the result settles on Close.
	h := newHarness()
	h.feed("abc\x1b7def\x1b8X", 0)
	h.tr.Close()
	if !eq(h.committed, []string{"abcXef"}) {
		t.Fatalf("DECSC/DECRC render = %q", h.committed)
	}
	// ANSI.SYS CSI s / CSI u behave identically.
	h2 := newHarness()
	h2.feed("abc\x1b[sdef\x1b[uX", 0)
	h2.tr.Close()
	if !eq(h2.committed, []string{"abcXef"}) {
		t.Fatalf("CSI s/u render = %q", h2.committed)
	}
}

func TestRendererCursorPositionRedraw(t *testing.T) {
	// Hide cursor, draw three rows, jump to an absolute position with CSI H, and
	// rewrite the top row; showing the cursor finalizes the region.
	h := newHarness()
	h.feed("\x1b[?25la\nb\nc\x1b[1;1HX\x1b[?25h", 0)
	if !eq(h.committed, []string{"X", "b", "c"}) {
		t.Fatalf("CSI H redraw render = %q", h.committed)
	}
}

func TestRendererCursorDownLocksRegionLikeOtherCursorMoves(t *testing.T) {
	// CSI B (cursor down) is a region-addressing move exactly like CSI A (up)
	// and CSI H (position), which both lock the region so a following '\n'
	// stays inside the live redraw instead of finalizing a forward line out of
	// order. Without the cursor down here ever locking, the '\n' after "row1"
	// would forward-commit whatever sits at the cursor's row instead of
	// leaving both rows for a single, correctly ordered region commit.
	h := newHarness()
	h.feed("row0\x1b[1B\rrow1\n", 0)
	h.tr.Close()
	if !eq(h.committed, []string{"row0", "row1"}) {
		t.Fatalf("cursor-down redraw render = %q, want [row0 row1] in original order", h.committed)
	}
}

func TestRendererEraseLineModes(t *testing.T) {
	// CSI 2K clears the whole line; the cursor keeps its column, so a following
	// write lands padded at that column.
	h := newHarness()
	h.feed("abc\x1b[2Kdef\n", 0)
	if !eq(h.committed, []string{"   def"}) {
		t.Fatalf("erase-whole-line render = %q", h.committed)
	}
	// CSI 1K clears from start of line to the cursor (exclusive).
	h2 := newHarness()
	h2.feed("abcdef\x1b[3D\x1b[1K\n", 0)
	if !eq(h2.committed, []string{"   def"}) {
		t.Fatalf("erase-to-cursor render = %q", h2.committed)
	}
}

func TestRendererEraseDisplayModes(t *testing.T) {
	// CSI 0J erases from the cursor to the end of the screen, dropping later rows.
	h := newHarness()
	h.feed("\x1b[?25la\nb\nc\x1b[1;1H\x1b[0JX\x1b[?25h", 0)
	if !eq(h.committed, []string{"X"}) {
		t.Fatalf("erase-to-end-of-screen render = %q", h.committed)
	}
	// CSI 1J erases the rows above the cursor.
	h2 := newHarness()
	h2.feed("\x1b[?25la\nb\nc\x1b[3;1H\x1b[1JX\x1b[?25h", 0)
	if !eq(h2.committed, []string{"", "", "X"}) {
		t.Fatalf("erase-above-cursor render = %q", h2.committed)
	}
}

func TestRendererAltScreenEntryResets(t *testing.T) {
	// Entering the alternate screen (CSI ?1049h) finalizes prior output and starts
	// a fresh region under a new epoch.
	h := newHarness()
	h.feed("before\n\x1b[?1049hinside", 0)
	startEpoch := h.tr.epoch
	h.tr.Close()
	if !eq(h.committed, []string{"before", "inside"}) {
		t.Fatalf("alt-screen render = %q", h.committed)
	}
	if startEpoch == 0 {
		t.Fatalf("alt-screen entry should advance the epoch")
	}
}

func TestRendererAltScreenExitUnlocksForwardMode(t *testing.T) {
	// Exiting the alternate screen (CSI ?1049l) must finalize whatever the
	// alt-screen left showing and return to forward mode, symmetric with
	// entry. Without this, everything printed after a pager (less, vim,
	// htop) exits stays locked in redraw mode and is never durably committed
	// until the process eventually exits — a long-running task that leaves
	// the alt screen keeps producing output that is only ever ephemeral
	// (provisional), never persisted to the log.
	h := newHarness()
	h.feed("before\n\x1b[?1049hinside\x1b[?1049lafter\n", 0)
	if !eq(h.committed, []string{"before", "inside", "after"}) {
		t.Fatalf("alt-screen exit render = %q", h.committed)
	}
}

func TestRendererSGRAccumulatesAndCaps(t *testing.T) {
	// A leading "0;" reset followed by a colour starts a fresh pen at that colour.
	h := newHarness()
	h.feed("\x1b[0;31mred\x1b[0m\n", 0)
	if !eq(h.committed, []string{"\x1b[0;31mred\x1b[0m"}) {
		t.Fatalf("SGR reset+colour render = %q", h.committed)
	}
	// A stream that never resets its pen must not grow the accumulated SGR string
	// without bound: past the cap it collapses to the latest sequence.
	h2 := newHarness()
	var b strings.Builder
	for i := 0; i < 80; i++ {
		b.WriteString("\x1b[1m")
	}
	b.WriteString("x\n")
	h2.feed(b.String(), 0)
	if len(h2.tr.penText) > maxPenLen {
		t.Fatalf("pen text exceeded cap: %d > %d", len(h2.tr.penText), maxPenLen)
	}
}
