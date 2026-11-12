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
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/logarchive"
)

const (
	// logStreamInterval is how often a logStream run's new output is shipped.
	// A run that ends sooner never streams: its archive covers it.
	logStreamInterval = 5 * time.Second
	// logChunkMaxBytes caps one chunk's raw size; a firehose ships several
	// chunks per tick instead of one huge upload.
	logChunkMaxBytes = 4 << 20
	// logChunkBatch is how many chunk targets one upload:request asks for.
	logChunkBatch = 20
	// logChunkRequestRetry spaces out asking again after a request failed
	// (typically while disconnected).
	logChunkRequestRetry = 30 * time.Second
	logChunkPutTimeout   = 30 * time.Second
)

// chunkStream is one logStream run's incremental upload. It is owned by its
// stream goroutine, and by StopStream once that goroutine is gone.
type chunkStream struct {
	executionID string
	path        string
	file        *os.File
	off         int64
	stop        chan struct{}
	done        chan struct{}

	targets  []protocol.UploadTarget // for chunks seq, seq+1, ...
	seq      int                     // next chunk to upload == chunks uploaded so far
	failedAt time.Time               // last failed upload:request
	capped   bool                    // the station handed out its last target, or refused one
}

// StartStream begins shipping a running execution's log in chunks when its
// dispatch asked for logStream. A no-op otherwise, or when already streaming.
func (u *LogUploader) StartStream(ctx context.Context, executionID, logFilePath string) {
	if !u.wantsStream(executionID) {
		return
	}
	s := &chunkStream{executionID: executionID, path: logFilePath, stop: make(chan struct{}), done: make(chan struct{})}
	u.mu.Lock()
	if _, running := u.streams[executionID]; running {
		u.mu.Unlock()
		return
	}
	u.streams[executionID] = s
	u.mu.Unlock()
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
// uploaded, nil when the run never streamed. A stream that already shipped
// something ships its tail too, so the chunks alone hold the whole log should
// the archive fail.
func (u *LogUploader) StopStream(ctx context.Context, executionID string) *int {
	u.mu.Lock()
	s, ok := u.streams[executionID]
	delete(u.streams, executionID)
	u.mu.Unlock()
	if !ok {
		return nil
	}
	close(s.stop)
	<-s.done
	if s.seq > 0 {
		u.flushChunks(ctx, s, true)
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	return new(s.seq)
}

// flushChunks uploads everything new in the log file, one chunk per target.
// A failed upload leaves the offset alone so the next tick retries the seq.
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
		target, ok := u.nextTarget(ctx, s)
		if !ok {
			return
		}
		putCtx, cancel := context.WithTimeout(ctx, logChunkPutTimeout)
		_, err = logarchive.PutChunk(putCtx, u.httpClient, logarchive.Target{URL: target.URL, Method: target.Method, Headers: target.Headers}, data, u.insecure)
		cancel()
		if err != nil {
			if _, perm := errors.AsType[*logarchive.PermanentError](err); perm {
				s.capped, s.targets = true, nil
			}
			slog.Warn("log chunk upload failed", "executionId", s.executionID, "err", err)
			return
		}
		s.off += int64(len(data))
		s.seq++
		s.targets = s.targets[1:]
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

// nextTarget returns the target for the next chunk, asking the station for a
// batch when none is left. Fewer targets than asked means the station's cap
// for this execution is reached: the stream uses what it got, then stops.
func (u *LogUploader) nextTarget(ctx context.Context, s *chunkStream) (protocol.UploadTarget, bool) {
	if len(s.targets) == 0 && !s.capped && time.Since(s.failedAt) >= logChunkRequestRetry {
		res, err := u.request(ctx, protocol.UploadRequest{ExecutionID: s.executionID, Purpose: protocol.UploadPurposeLogChunk, FromSeq: s.seq, Count: logChunkBatch})
		switch se, isStation := errors.AsType[*StationError](err); {
		case err == nil:
			s.targets = res.Uploads
			s.capped = len(res.Uploads) < logChunkBatch
		case isStation && !se.Retryable:
			s.capped = true
		default:
			s.failedAt = time.Now()
		}
	}
	if len(s.targets) == 0 {
		return protocol.UploadTarget{}, false
	}
	return s.targets[0], true
}
