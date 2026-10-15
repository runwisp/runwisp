// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runlog

import (
	"bytes"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/stretchr/testify/assert"
)

func publishLine(bus *events.Bus, task, stream, text string) {
	bus.Publish(events.EventLogLine, events.LogLineEvent{
		TaskName: task, RunID: "01HRUN", Stream: stream, Text: text, Timestamp: 1700000000000,
	})
}

func TestOutputMirror_TextSplitsStreamsAndPrefixesTask(t *testing.T) {
	var stdout, stderr bytes.Buffer
	bus := events.NewEventBus()
	m := NewOutputMirror(&stdout, &stderr, false, true)
	stop := m.Subscribe(bus)

	publishLine(bus, "backup", logutil.StreamStdout, "dumping")
	publishLine(bus, "backup", logutil.StreamStderr, "disk almost full")
	stop() // drains what is queued

	assert.Equal(t, "backup | dumping\n", stdout.String())
	assert.Equal(t, "backup | disk almost full\n", stderr.String())
}

func TestOutputMirror_JSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	bus := events.NewEventBus()
	m := NewOutputMirror(&stdout, &stderr, true, true)
	stop := m.Subscribe(bus)
	publishLine(bus, "backup", logutil.StreamStdout, `say "hi"`)
	stop()

	assert.JSONEq(t,
		`{"time":"2023-11-14T22:13:20Z","msg":"output","task":"backup","run":"01HRUN","stream":"stdout","text":"say \"hi\""}`,
		stdout.String())
}

func TestOutputMirror_OffByDefaultAndLiveToggle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	bus := events.NewEventBus()
	m := NewOutputMirror(&stdout, &stderr, false, false)
	stop := m.Subscribe(bus)

	publishLine(bus, "t", logutil.StreamStdout, "before")
	m.SetEnabled(true)
	publishLine(bus, "t", logutil.StreamStdout, "after")
	stop()

	assert.Equal(t, "t | after\n", stdout.String())
}

// A full buffer drops lines and says so, instead of blocking the executor.
func TestOutputMirror_FullBufferDropsAndReports(t *testing.T) {
	var stdout, stderr bytes.Buffer
	m := NewOutputMirror(&stdout, &stderr, false, true)
	// No writer running yet, so the buffer fills.
	for range outputBuffer + 3 {
		m.enqueue(events.Event{Type: events.EventLogLine, Data: events.LogLineEvent{TaskName: "t", Text: "x"}})
	}
	bus := events.NewEventBus()
	stop := m.Subscribe(bus)
	stop()

	assert.Contains(t, stderr.String(), "3 output lines were not mirrored")
}
