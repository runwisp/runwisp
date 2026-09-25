// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logstream

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mirror of TestStreamLoop_LargePositiveGapReplaysTail for a tail request: a
// negative `from` wider than the replay budget replays the newest lines, or the
// lines between the backfill and the live tail are never delivered.
func TestStreamLoop_LargeNegativeFromReplaysTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	var b strings.Builder
	for i := 0; i < 8000; i++ {
		b.WriteString("line")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n")
	}
	require.NoError(t, os.WriteFile(logPath, []byte(b.String()), 0o600))

	m := &mockSender{}
	s := newStreamer(m, logPath)
	bus := events.NewEventBus()
	s.streamLoop(context.Background(), "run1", bus, nil, -8000, 5000, true)

	require.Len(t, m.lines, 5000, "exactly the replay budget of lines is backfilled")
	assert.Equal(t, int64(3000), m.lines[0].N, "backfill should start at the tail anchor, not line 0")
	assert.Equal(t, int64(7999), m.lines[len(m.lines)-1].N, "newest line should be replayed")
}
