// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRunGetter implements the runGetter interface for testing.
type mockRunGetter struct {
	run *model.Run
	err error
}

func (m *mockRunGetter) GetRun(id string) (*model.Run, error) {
	return m.run, m.err
}

// flusherRecorder is an httptest.ResponseRecorder that also implements http.Flusher.
type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushCount int
}

func (f *flusherRecorder) Flush() {
	f.flushCount++
	f.ResponseRecorder.Flush()
}

func newFlusherRecorder() *flusherRecorder {
	return &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func TestNewLogStreamer(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("hello\n"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	assert.Equal(t, logPath, s.logPath)
	assert.NotNil(t, s.file)
}

func TestNewLogStreamer_FileNotFound(t *testing.T) {
	fr := newFlusherRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newLogStreamer(ctx, fr, fr, "/nonexistent/log.log")
	assert.Error(t, err)
}

func TestOpenLogFile_WaitsForFileCreation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "late.log")

	go func() {
		time.Sleep(300 * time.Millisecond)
		os.WriteFile(logPath, []byte("created\n"), 0644)
	}()

	file, err := openLogFile(context.Background(), logPath)
	require.NoError(t, err)
	file.Close()
}

func TestLogStreamer_FileSize(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := "hello world\n"
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	size, err := s.fileSize()
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), size)
}

func TestLogStreamer_VirtualFileSize_NoRotation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := "data\n"
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	virtual, phys, err := s.virtualFileSize()
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), phys)
	assert.Equal(t, int64(len(content)), virtual)
}

func TestLogStreamer_VirtualFileSize_WithRotation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := "data\n"
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	// Write rotation metadata
	logutil.WriteLogMeta(logPath, logutil.LogMeta{RotatedBytes: 1000, RotatedLines: 50})

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	virtual, phys, err := s.virtualFileSize()
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), phys)
	assert.Equal(t, int64(1000+len(content)), virtual)
}

func TestLogStreamer_EmitMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("x"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	s.emitMetadata(42)
	assert.Contains(t, fr.Body.String(), "event: metadata")
	assert.Contains(t, fr.Body.String(), `{"fileSize":42}`)
	assert.Equal(t, 1, fr.flushCount)
}

func TestLogStreamer_EmitChunk(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("x"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	s.emitChunk([]byte("hello world\n"))
	body := fr.Body.String()
	assert.Contains(t, body, "data: ")

	// The data should be JSON-encoded string
	var decoded string
	// Extract JSON after "data: "
	idx := len("data: ")
	endIdx := len(body) - 2 // strip trailing \n\n
	require.NoError(t, json.Unmarshal([]byte(body[idx:endIdx]), &decoded))
	assert.Equal(t, "hello world\n", decoded)
}

func TestLogStreamer_EmitDone(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("x"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	s.emitDone()
	assert.Contains(t, fr.Body.String(), "event: done")
	assert.Contains(t, fr.Body.String(), "Run completed")
}

func TestLogStreamer_EmitInitialData(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := "line 1\nline 2\nline 3\n"
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	err = s.emitInitialData(int64(len(content)))
	require.NoError(t, err)

	body := fr.Body.String()
	assert.Contains(t, body, "line 1")
	assert.Contains(t, body, "line 2")
	assert.Contains(t, body, "line 3")
	assert.Equal(t, int64(len(content)), s.lastSize)
}

func TestLogStreamer_SeekToOffset_Zero(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := "abcdefghij"
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	remaining, err := s.seekToOffset("0", int64(len(content)), int64(len(content)))
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), remaining)
}

func TestLogStreamer_SeekToOffset_Middle(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := "abcdefghij" // 10 bytes
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	remaining, err := s.seekToOffset("5", 10, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(5), remaining)
}

func TestLogStreamer_EmitNewData(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("initial\n"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	s.lastSize = 8 // "initial\n" is 8 bytes

	// Append new data
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, err = f.WriteString("appended\n")
	require.NoError(t, err)
	f.Close()

	// Re-open since os.File caches position
	s.file.Close()
	s.file, err = os.Open(logPath)
	require.NoError(t, err)

	err = s.emitNewData()
	require.NoError(t, err)

	assert.Contains(t, fr.Body.String(), "appended")
	assert.Equal(t, int64(17), s.lastSize) // 8 + 9
}

func TestLogStreamer_EmitNewData_NoNewData(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	content := "hello\n"
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	s.lastSize = int64(len(content))

	err = s.emitNewData()
	require.NoError(t, err)
	assert.Empty(t, fr.Body.String())
}

func TestLogStreamer_HandleRotation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("old content\n"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	s.lastSize = 0

	// No rotation yet — same file
	rotated := s.handleRotation()
	assert.False(t, rotated)

	// Simulate rotation: remove old file, create new one at same path
	os.Remove(logPath)
	require.NoError(t, os.WriteFile(logPath, []byte("new content\n"), 0644))

	rotated = s.handleRotation()
	assert.True(t, rotated)
	assert.Equal(t, int64(0), s.lastSize)

	// Should have drained old file's content (emitted via SSE)
	body := fr.Body.String()
	assert.Contains(t, body, "old content")
}

func TestLogStreamer_HandleRotation_NoPathFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("data"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	// Delete the file at path (but file handle still open)
	os.Remove(logPath)

	rotated := s.handleRotation()
	assert.False(t, rotated) // os.Stat fails → returns false
}

func TestLogStreamer_CheckRunCompleted_StillRunning(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("x"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	db := &mockRunGetter{
		run: &model.Run{ID: "run1", Status: model.PhaseRunning},
	}

	completed := s.checkRunCompleted("run1", db)
	assert.False(t, completed)
}

func TestLogStreamer_CheckRunCompleted_Ended(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("x"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	db := &mockRunGetter{
		run: &model.Run{ID: "run1", Status: model.PhaseEnded},
	}

	completed := s.checkRunCompleted("run1", db)
	assert.True(t, completed)
	assert.Contains(t, fr.Body.String(), "event: done")
}

func TestLogStreamer_CheckRunCompleted_Error(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("x"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	db := &mockRunGetter{err: fmt.Errorf("db error")}

	completed := s.checkRunCompleted("run1", db)
	assert.True(t, completed) // errors cause early exit
}

func TestLogStreamer_CheckRunCompleted_PhasePending(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("x"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()

	db := &mockRunGetter{
		run: &model.Run{ID: "run1", Status: model.PhasePending},
	}

	completed := s.checkRunCompleted("run1", db)
	assert.False(t, completed) // PhasePending must NOT trigger done
	assert.NotContains(t, fr.Body.String(), "event: done")
}

func TestLogStreamer_PollLoop_ImmediateDBCheck(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("fast output\n"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()
	s.lastSize = 0

	db := &mockRunGetter{
		run: &model.Run{ID: "run1", Status: model.PhaseEnded},
	}
	bus := events.NewEventBus()

	start := time.Now()
	s.pollLoop(context.Background(), "run1", bus, db)
	elapsed := time.Since(start)

	body := fr.Body.String()
	assert.Contains(t, body, "fast output")
	assert.Contains(t, body, "event: done")
	assert.Less(t, elapsed, 1*time.Second, "pollLoop should exit immediately via the initial DB check")
}

func TestLogStreamer_PollLoop_ContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("data\n"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()
	s.lastSize = 5

	db := &mockRunGetter{
		run: &model.Run{ID: "run1", Status: model.PhaseRunning},
	}
	bus := events.NewEventBus()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	s.pollLoop(ctx, "run1", bus, db)
	// Should return without blocking
}

func TestLogStreamer_PollLoop_EmitsNewDataAndDone(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	require.NoError(t, os.WriteFile(logPath, []byte("initial\n"), 0644))

	fr := newFlusherRecorder()
	s, err := newLogStreamer(context.Background(), fr, fr, logPath)
	require.NoError(t, err)
	defer s.Close()
	s.lastSize = int64(len("initial\n"))

	db := &mockRunGetter{
		run: &model.Run{ID: "run1", Status: model.PhaseRunning},
	}
	bus := events.NewEventBus()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// After a short delay, append data
		time.Sleep(200 * time.Millisecond)
		f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0644)
		f.WriteString("appended\n")
		f.Close()

		// After another poll interval, signal completion via EventBus
		time.Sleep(600 * time.Millisecond)
		run := &model.Run{ID: "run1", Status: model.PhaseEnded}
		bus.PublishSync(events.EventRunCompleted, events.RunEvent{Run: run})
	}()

	go func() {
		time.Sleep(3 * time.Second)
		cancel() // Safety timeout
	}()

	s.pollLoop(ctx, "run1", bus, db)

	body := fr.Body.String()
	assert.Contains(t, body, "appended")
	assert.Contains(t, body, "event: done")
}
