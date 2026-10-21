// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/logarchive"
)

const (
	// logStreamInterval is how often a logStream run's new output is shipped.
	// A run that ends sooner never streams: its terminal archive covers it.
	logStreamInterval = 5 * time.Second
	// logChunkMaxBytes caps one chunk's raw size; a firehose ships several
	// chunks per tick instead of one huge PUT.
	logChunkMaxBytes = 4 << 20
	// logChunkURLLowWater triggers the next log:chunkUrlsRequest while enough
	// URLs remain to cover the round trip.
	logChunkURLLowWater = 10
	// logChunkRequestRetry re-asks for URLs when a request went unanswered
	// (e.g. it was dropped during a reconnect).
	logChunkRequestRetry = 30 * time.Second
	logChunkPutTimeout   = 30 * time.Second
)

// chunkStream is one logStream run's incremental upload state. The file and
// offset are owned by the stream goroutine (and by StopStream once that
// goroutine is gone); mu guards what the inbound log:chunkUrls handler touches.
type chunkStream struct {
	executionID string
	path        string
	send        func(any) error
	file        *os.File
	off         int64
	stop        chan struct{}
	done        chan struct{}

	mu          sync.Mutex
	urls        []string // signed PUT URLs for chunks seq, seq+1, ...
	seq         int      // next chunk to upload == chunks uploaded so far
	requestedAt time.Time
	capped      bool // the station handed out its last URL, or a PUT was refused
}

// StartStream begins shipping a running execution's log in chunks when its
// dispatch asked for logStream. A no-op otherwise, or when already streaming.
func (u *LogUploader) StartStream(ctx context.Context, executionID, logFilePath string, send func(any) error) {
	entry, ok := u.lookup(executionID)
	if !ok || !entry.stream {
		return
	}
	s := &chunkStream{
		executionID: executionID,
		path:        logFilePath,
		send:        send,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	u.mu.Lock()
	if _, running := u.streams[executionID]; running {
		u.mu.Unlock()
		return
	}
	u.streams[executionID] = s
	u.mu.Unlock()

	s.requestURLs()
	go u.runStream(ctx, s)
}

func (u *LogUploader) runStream(ctx context.Context, s *chunkStream) {
	defer crashguard.Guard()
	defer close(s.done)
	ticker := time.NewTicker(logStreamInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			u.flushChunks(ctx, s, false)
		}
	}
}

// StopStream ends executionID's stream and returns how many chunks were
// uploaded; ok is false when the run never streamed. A stream that already
// shipped something ships its tail too, so the chunks alone hold the whole log
// should the terminal archive fail.
func (u *LogUploader) StopStream(ctx context.Context, executionID string) (chunks int, ok bool) {
	u.mu.Lock()
	s, ok := u.streams[executionID]
	delete(u.streams, executionID)
	u.mu.Unlock()
	if !ok {
		return 0, false
	}
	close(s.stop)
	<-s.done
	if s.uploaded() > 0 {
		u.flushChunks(ctx, s, true)
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	return s.uploaded(), true
}

// OnChunkUrls appends the station's URL batch to the matching stream. A batch
// that doesn't continue where the stream's URLs end is stale and dropped; an
// empty one means the station's per-execution cap was reached.
func (u *LogUploader) OnChunkUrls(message protocol.LogChunkUrlsMessage) {
	u.mu.Lock()
	s, ok := u.streams[message.ExecutionID]
	u.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if message.FromSeq != s.seq+len(s.urls) {
		return
	}
	s.urls = append(s.urls, message.Urls...)
	s.requestedAt = time.Time{}
	if len(message.Urls) == 0 {
		s.capped = true
	}
}

// flushChunks uploads everything new in the log file, one chunk per URL. A
// failed PUT leaves the offset alone so the next tick retries the same seq.
func (u *LogUploader) flushChunks(ctx context.Context, s *chunkStream, final bool) {
	for {
		data, rotated, err := s.next(final)
		if err != nil {
			slog.Debug("log stream read failed", "executionId", s.executionID, "err", err)
			return
		}
		if len(data) == 0 {
			if !rotated {
				return
			}
			// The old segment is drained; continue with the new file.
			_ = s.file.Close()
			s.file, s.off = nil, 0
			continue
		}
		url, ok := s.nextURL()
		if !ok {
			return
		}
		putCtx, cancel := context.WithTimeout(ctx, logChunkPutTimeout)
		_, err = logarchive.PutChunk(putCtx, u.httpClient, url, data, u.insecure)
		cancel()
		if err != nil {
			if _, perm := errors.AsType[*logarchive.PermanentError](err); perm {
				s.mu.Lock()
				s.capped, s.urls = true, nil
				s.mu.Unlock()
			}
			slog.Warn("log chunk upload failed", "executionId", s.executionID, "err", err)
			return
		}
		s.off += int64(len(data))
		s.mu.Lock()
		s.seq++
		s.urls = s.urls[1:]
		s.mu.Unlock()
	}
}

// next reads the next chunk from the held log file. Unless final, it stops at
// the last complete line so a chunk never splits one. rotated reports that the
// log path now names a different file (drop_old moved this one to .prev): the
// held handle still reads the old segment to its end before switching.
func (s *chunkStream) next(final bool) ([]byte, bool, error) {
	if s.file == nil {
		f, err := os.Open(s.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		s.file = f
	}
	rotated := false
	if held, err := s.file.Stat(); err == nil {
		cur, statErr := os.Stat(s.path)
		rotated = statErr == nil && !os.SameFile(held, cur)
	}
	buf := make([]byte, logChunkMaxBytes)
	n, err := s.file.ReadAt(buf, s.off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, rotated, err
	}
	buf = buf[:n]
	if !final && !rotated && n < logChunkMaxBytes {
		buf = buf[:bytes.LastIndexByte(buf, '\n')+1]
	}
	return buf, rotated, nil
}

// nextURL returns the URL for the next chunk, asking for more when running low.
func (s *chunkStream) nextURL() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.urls) <= logChunkURLLowWater {
		s.requestURLsLocked()
	}
	if len(s.urls) == 0 {
		return "", false
	}
	return s.urls[0], true
}

func (s *chunkStream) requestURLs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestURLsLocked()
}

// requestURLsLocked sends one log:chunkUrlsRequest unless the station capped
// the stream or a request is still outstanding. A send that fails (no session)
// leaves nothing outstanding, so the next chunk asks again.
func (s *chunkStream) requestURLsLocked() {
	if s.capped || (!s.requestedAt.IsZero() && time.Since(s.requestedAt) < logChunkRequestRetry) {
		return
	}
	if err := s.send(NewLogChunkUrlsRequestMessage(s.executionID, s.seq+len(s.urls))); err != nil {
		return
	}
	s.requestedAt = time.Now()
}

func (s *chunkStream) uploaded() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}
