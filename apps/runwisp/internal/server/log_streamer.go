// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// runGetter abstracts fetching a run by ID for log streaming.
type runGetter interface {
	GetRun(string) (*model.Run, error)
}

// logStreamer manages SSE streaming of a log file, including rotation
// detection and polling for new data on active runs.
type logStreamer struct {
	logPath  string
	meta     logutil.LogMeta
	file     *os.File
	flusher  http.Flusher
	resp     http.ResponseWriter
	lastSize int64
}

func newLogStreamer(ctx context.Context, resp http.ResponseWriter, flusher http.Flusher, logPath string) (*logStreamer, error) {
	meta := logutil.ReadLogMeta(logPath)

	file, err := openLogFile(ctx, logPath)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}

	return &logStreamer{
		logPath: logPath,
		meta:    meta,
		file:    file,
		flusher: flusher,
		resp:    resp,
	}, nil
}

// openLogFile opens the log file at logPath, waiting up to LogFileWaitTimeout
// for it to be created if it does not yet exist.
func openLogFile(ctx context.Context, logPath string) (*os.File, error) {
	file, err := os.Open(logPath)
	if err == nil {
		return file, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	// File doesn't exist yet — poll until it appears or the deadline passes.
	timeout := time.NewTimer(LogFileWaitTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(LogStreamInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, err
		case <-ticker.C:
			file, err = os.Open(logPath)
			if err == nil {
				return file, nil
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
}

func (s *logStreamer) Close() {
	if s.file != nil {
		s.file.Close()
	}
}

func (s *logStreamer) fileSize() (int64, error) {
	info, err := s.file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *logStreamer) virtualFileSize() (int64, int64, error) {
	physSize, err := s.fileSize()
	if err != nil {
		return 0, 0, err
	}
	return s.meta.RotatedBytes + physSize, physSize, nil
}

func (s *logStreamer) emitMetadata(virtualSize int64) {
	fmt.Fprintf(s.resp, "event: metadata\ndata: {\"fileSize\":%d}\n\n", virtualSize)
	s.flusher.Flush()
}

func (s *logStreamer) seekToOffset(offsetStr string, virtualSize, physSize int64) (int64, error) {
	offset := logutil.ParseLogOffset(offsetStr, virtualSize)
	fileOffset := offset - s.meta.RotatedBytes
	if fileOffset < 0 {
		fileOffset = 0
	}
	if fileOffset > physSize {
		fileOffset = physSize
	}
	if _, err := s.file.Seek(fileOffset, io.SeekStart); err != nil {
		return 0, err
	}
	return physSize - fileOffset, nil
}

func (s *logStreamer) emitInitialData(remaining int64) error {
	if remaining > MaxLogBytes {
		remaining = MaxLogBytes
	}
	buf := make([]byte, LogReadBufferSize)
	for remaining > 0 {
		toRead := int64(len(buf))
		if toRead > remaining {
			toRead = remaining
		}
		n, err := s.file.Read(buf[:toRead])
		if n > 0 {
			s.emitChunk(buf[:n])
			remaining -= int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	pos, err := s.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	s.lastSize = pos
	return nil
}

func (s *logStreamer) emitChunk(data []byte) {
	jsonData, err := json.Marshal(string(data))
	if err != nil {
		log.Warn("Failed to marshal log chunk for SSE", "err", err)
		return
	}
	fmt.Fprintf(s.resp, "data: %s\n\n", jsonData)
	s.flusher.Flush()
}

func (s *logStreamer) emitDone() {
	fmt.Fprintf(s.resp, "event: done\ndata: Run completed\n\n")
	s.flusher.Flush()
}

// pollLoop polls the log file for new data while the run is active.
// It uses EventBus to detect run completion in real-time, with a periodic
// DB fallback for safety (in case the event was published before subscription).
func (s *logStreamer) pollLoop(ctx context.Context, runID string, bus events.EventBus, db runGetter) {
	doneCh := make(chan struct{}, 1)

	unsubCompleted := bus.Subscribe(events.EventRunCompleted, func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run.ID == runID {
			log.Debug("SSE: EventRunCompleted received via bus", "runID", runID)
			select {
			case doneCh <- struct{}{}:
			default:
			}
		}
	})
	defer unsubCompleted()

	unsubFailed := bus.Subscribe(events.EventRunFailed, func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run.ID == runID {
			log.Debug("SSE: EventRunFailed received via bus", "runID", runID)
			select {
			case doneCh <- struct{}{}:
			default:
			}
		}
	})
	defer unsubFailed()

	ticker := time.NewTicker(LogStreamInterval)
	defer ticker.Stop()

	// DB fallback: check every 5s in case the event was published before we subscribed.
	fallbackTicker := time.NewTicker(5 * time.Second)
	defer fallbackTicker.Stop()

	log.Debug("SSE: pollLoop started, subscribed to EventBus", "runID", runID)

	// Immediate check: the run may have completed between the initial GetRun
	// in the handler and the EventBus subscription above, so the event was
	// already published and we missed it. Without this, fast-completing runs
	// would wait up to 5s for the fallback ticker.
	if s.checkRunCompleted(runID, db) {
		log.Debug("SSE: immediate DB check triggered done", "runID", runID)
		return
	}

	for {
		select {
		case <-ctx.Done():
			log.Debug("SSE: pollLoop context cancelled", "runID", runID)
			return
		case <-doneCh:
			log.Debug("SSE: doneCh fired, emitting final data + done", "runID", runID, "lastSize", s.lastSize)
			s.emitNewData()
			s.emitDone()
			log.Debug("SSE: done event emitted", "runID", runID)
			return
		case <-ticker.C:
			if s.handleRotation() {
				continue
			}
			if err := s.emitNewData(); err != nil {
				log.Error("SSE: emitNewData error in pollLoop", "runID", runID, "err", err)
				return
			}
		case <-fallbackTicker.C:
			log.Debug("SSE: fallback DB check", "runID", runID)
			if s.checkRunCompleted(runID, db) {
				log.Debug("SSE: fallback DB check triggered done", "runID", runID)
				return
			}
		}
	}
}

func (s *logStreamer) handleRotation() bool {
	pathInfo, pathErr := os.Stat(s.logPath)
	openInfo, _ := s.file.Stat()
	if pathErr != nil || os.SameFile(pathInfo, openInfo) {
		return false
	}

	// Drain remaining bytes from the old file
	oldEnd, seekErr := s.file.Seek(0, io.SeekEnd)
	if seekErr != nil {
		log.Warn("Failed to seek to end of old log file during rotation", "err", seekErr)
	} else if oldEnd > s.lastSize {
		if _, seekErr := s.file.Seek(s.lastSize, io.SeekStart); seekErr == nil {
			drainSize := oldEnd - s.lastSize
			if drainSize > int64(LogStreamBufferSize) {
				drainSize = int64(LogStreamBufferSize)
			}
			drainBuf := make([]byte, drainSize)
			n, _ := s.file.Read(drainBuf)
			if n > 0 {
				s.emitChunk(drainBuf[:n])
			}
		}
	}

	s.file.Close()
	newFile, err := os.Open(s.logPath)
	if err != nil {
		log.Error("Failed to reopen log file after rotation", "err", err)
		s.file = nil
		return true
	}
	s.file = newFile
	s.lastSize = 0
	return true
}

func (s *logStreamer) emitNewData() error {
	currentSize, err := s.file.Seek(0, io.SeekEnd)
	if err != nil {
		log.Error("Failed to seek to end of log file", "err", err)
		return err
	}
	if currentSize <= s.lastSize {
		return nil
	}

	if _, err := s.file.Seek(s.lastSize, io.SeekStart); err != nil {
		log.Error("Failed to seek in log file", "err", err)
		return err
	}

	toRead := currentSize - s.lastSize
	if toRead > int64(LogStreamBufferSize) {
		toRead = int64(LogStreamBufferSize)
	}
	buf := make([]byte, toRead)
	n, err := s.file.Read(buf)
	if err != nil && err != io.EOF {
		log.Error("Failed to read log file", "err", err)
		return err
	}
	if n > 0 {
		s.emitChunk(buf[:n])
	}
	s.lastSize += int64(n)
	return nil
}

func (s *logStreamer) checkRunCompleted(runID string, db runGetter) bool {
	updatedRun, err := db.GetRun(runID)
	if err != nil {
		log.Error("Failed to fetch run status", "err", err)
		return true
	}
	if updatedRun != nil && updatedRun.Status == model.PhaseEnded {
		s.emitNewData()
		s.emitDone()
		return true
	}
	return false
}
