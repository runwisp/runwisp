// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runlog

import (
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
)

// outputBuffer bounds the lines waiting for the writer. A full buffer drops
// lines (counted, and reported once the writer catches up) rather than blocking
// the executor: the stored run log is the record, this is a copy.
const outputBuffer = 4096

// OutputMirror copies the output lines of every run to the daemon's own stdout
// and stderr, so `docker logs` and `journalctl` show what the jobs printed. It
// is the counterpart of supervisord's `stdout_logfile=/dev/stdout`.
//
// Lines are already secret-redacted when they reach the event bus. Text format
// prefixes each line with its task name; JSON format writes one object per
// line. A line from the task's stderr goes to the daemon's stderr, the rest to
// its stdout.
//
// It is off by default; SetEnabled flips it live, which is how a reload of
// [daemon] log_output takes effect.
type OutputMirror struct {
	stdout, stderr io.Writer
	jsonFormat     bool
	enabled        atomic.Bool
	lines          chan events.LogLineEvent
	dropped        atomic.Int64
}

// NewOutputMirror returns a mirror writing to stdout and stderr, in JSON when
// jsonFormat is set.
func NewOutputMirror(stdout, stderr io.Writer, jsonFormat, enabled bool) *OutputMirror {
	m := &OutputMirror{
		stdout:     stdout,
		stderr:     stderr,
		jsonFormat: jsonFormat,
		lines:      make(chan events.LogLineEvent, outputBuffer),
	}
	m.enabled.Store(enabled)
	return m
}

// SetEnabled turns the mirror on or off.
func (m *OutputMirror) SetEnabled(on bool) { m.enabled.Store(on) }

// Subscribe starts mirroring bus's log lines and returns the func that stops it
// and waits for the writer to finish.
func (m *OutputMirror) Subscribe(bus *events.Bus) (stop func()) {
	unsub := bus.Subscribe(events.EventLogLine, m.enqueue)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer crashguard.Guard()
		m.write(done)
	}()
	return func() {
		unsub()
		close(done)
		<-finished
	}
}

// enqueue runs inside the executor's Publish call, so it never blocks.
func (m *OutputMirror) enqueue(e events.Event) {
	if !m.enabled.Load() {
		return
	}
	line, ok := e.Data.(events.LogLineEvent)
	if !ok {
		return
	}
	select {
	case m.lines <- line:
	default:
		m.dropped.Add(1)
	}
}

func (m *OutputMirror) write(done <-chan struct{}) {
	for {
		select {
		case line := <-m.lines:
			m.print(line)
		case <-done:
			// Finish what is already queued, then stop.
			for {
				select {
				case line := <-m.lines:
					m.print(line)
				default:
					return
				}
			}
		}
	}
}

func (m *OutputMirror) print(line events.LogLineEvent) {
	if n := m.dropped.Swap(0); n > 0 {
		fmt.Fprintf(m.stderr, "runwisp: %d output lines were not mirrored (the run logs have them)\n", n)
	}
	w := m.stdout
	if line.Stream == logutil.StreamStderr {
		w = m.stderr
	}
	if !m.jsonFormat {
		fmt.Fprintf(w, "%s | %s\n", line.TaskName, line.Text)
		return
	}
	b, err := json.Marshal(struct {
		Time   string `json:"time"`
		Msg    string `json:"msg"`
		Task   string `json:"task"`
		Run    string `json:"run"`
		Stream string `json:"stream"`
		Text   string `json:"text"`
	}{time.UnixMilli(line.Timestamp).UTC().Format(time.RFC3339Nano), "output", line.TaskName, line.RunID, line.Stream, line.Text})
	if err == nil {
		fmt.Fprintf(w, "%s\n", b)
	}
}
