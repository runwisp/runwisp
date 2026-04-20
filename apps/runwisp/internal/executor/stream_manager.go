// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

// StreamManager reads process output and writes log lines to a writer,
// batching log events for the event bus.
type StreamManager struct {
	eventBus events.EventBus
}

func NewStreamManager(eventBus events.EventBus) *StreamManager {
	return &StreamManager{eventBus: eventBus}
}

// StreamToFile reads from reader, writes lines to writer, and publishes batched log events.
func (s *StreamManager) StreamToFile(reader io.Reader, writer io.Writer, task *model.Task, run *model.Run, prefix string) {
	var batch strings.Builder
	batchLines := 0
	batchTimer := time.NewTicker(EventBatchInterval)
	defer batchTimer.Stop()

	externalExecutionID := ""
	if run.ExternalExecutionID != nil {
		externalExecutionID = *run.ExternalExecutionID
	}

	flushBatch := func() {
		if batchLines == 0 {
			return
		}
		if s.eventBus != nil {
			s.eventBus.Publish(events.EventLogLine, events.LogLineEvent{
				TaskName:            task.Name,
				RunID:               run.ID,
				ExternalExecutionID: externalExecutionID,
				Line:                batch.String(),
				Stream:              prefix,
			})
		}
		batch.Reset()
		batchLines = 0
	}

	lineBuf := NewLineBuffer(func(line string) {
		formatted := FormatLine(line, prefix)
		if _, err := writer.Write([]byte(formatted)); err != nil {
			log.Warn("Failed to write log line to file", "stream", prefix, "err", err)
		}
		batch.WriteString(formatted)
		batchLines++
		if batchLines >= EventBatchSize {
			flushBatch()
		}
	})

	buf := make([]byte, StreamReadBufferSize)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			lineBuf.Write(buf[:n])
		}

		select {
		case <-batchTimer.C:
			flushBatch()
		default:
		}

		if err != nil {
			break
		}
	}

	lineBuf.Flush()
	flushBatch()
}

// FormatLine prepends a stream prefix for non-stdout lines.
// STDOUT lines are returned as-is; STDERR gets an [ERR] tag; other
// prefixes are wrapped in brackets. The line is always \n-terminated.
func FormatLine(line string, prefix string) string {
	switch prefix {
	case "STDOUT":
		return ensureNewline(line)
	case "STDERR":
		return "[ERR] " + strings.TrimSuffix(line, "\n") + "\n"
	default:
		return "[" + prefix + "] " + strings.TrimSuffix(line, "\n") + "\n"
	}
}

func ensureNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s
	}
	return s + "\n"
}
