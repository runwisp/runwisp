// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"io"
	"strings"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

// StreamManager reads process output, writes each line to the LogWriter (which
// is the single source of absolute line numbers), and publishes one
// LogLineEvent per line on the bus.
type StreamManager struct {
	eventBus events.EventBus
	nowMs    func() int64
}

func NewStreamManager(eventBus events.EventBus) *StreamManager {
	return &StreamManager{
		eventBus: eventBus,
		nowMs:    func() int64 { return time.Now().UnixMilli() },
	}
}

// StreamToFile reads from reader and writes lines to writer. Each completed
// line is written via writer.WriteLineEvent (which assigns the absolute line
// number under the writer's mutex), and a LogLineEvent is published with that
// number. Lines that LineBuffer split because they exceeded MaxLineBufferSize
// are emitted as separate events with Continued=true on segments 2..N.
func (s *StreamManager) StreamToFile(reader io.Reader, writer *LogWriter, task *model.Task, run *model.Run, stream string) {
	externalExecutionID := ""
	if run.ExternalExecutionID != nil {
		externalExecutionID = *run.ExternalExecutionID
	}

	// `incomplete` is true when the previous LineBuffer callback delivered a
	// fragment without a trailing newline (LineBuffer overflow flush). The
	// next call is then segment 2..N of the same logical line.
	incomplete := false

	publish := func(text string, lineNum int64, continued bool) {
		if s.eventBus == nil {
			return
		}
		s.eventBus.Publish(events.EventLogLine, events.LogLineEvent{
			TaskName:            task.Name,
			RunID:               run.ID,
			ExternalExecutionID: externalExecutionID,
			LineNum:             lineNum,
			Timestamp:           s.nowMs(),
			Stream:              stream,
			Text:                text,
			Continued:           continued,
		})
	}

	lineBuf := NewLineBuffer(func(line string) {
		isContinuation := incomplete
		incomplete = !strings.HasSuffix(line, "\n")
		text := strings.TrimSuffix(line, "\n")

		n, err := writer.WriteLineEvent(text, stream)
		if err != nil {
			slog.Warn("Failed to write log line to file", "stream", stream, "err", err)
			return
		}
		if n < 0 {
			// Line was dropped (writer stopped / overflow). Skip the event so
			// downstream subscribers never see a line that isn't on disk.
			return
		}
		publish(text, n, isContinuation)
	})

	buf := make([]byte, StreamReadBufferSize)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			lineBuf.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	lineBuf.Flush()
}
