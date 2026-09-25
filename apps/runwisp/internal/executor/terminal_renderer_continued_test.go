// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"strings"
	"testing"
)

// An oversized split during a locked redraw marks only its own tail as
// continued; later, unrelated lines in the region must not inherit the flag.
func TestTerminalRenderer_LockedSplitContinuedDoesNotLeak(t *testing.T) {
	h := newHarness()
	h.tr.Write([]byte("\x1b[1A")) // any cursor motion locks the screen
	h.tr.Write([]byte(strings.Repeat("a", maxRowCells+1) + "tail\nother"))
	h.tr.Close()

	got := map[string]bool{}
	for i, text := range h.committed {
		got[text] = h.continued[i]
	}
	if !got["tail"] {
		t.Errorf("tail of the split line should be continued; commits=%v", h.committed)
	}
	if cont, ok := got["other"]; !ok || cont {
		t.Errorf("unrelated line %q: present=%v continued=%v, want present and not continued", "other", ok, cont)
	}
}
