// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"io"
	"os"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

type logReadResult struct {
	Data   []byte
	Offset int64
	Final  bool
}

func emptyLogResult(offset int64, final bool) *logReadResult {
	return &logReadResult{Data: []byte{}, Offset: offset, Final: final}
}

func readExecutionLogChunk(run *model.Run, logDir string, requestedOffset, requestedLimit int64) (*logReadResult, error) {
	if run == nil {
		return emptyLogResult(0, true), nil
	}

	offset := max(requestedOffset, 0)

	limit := requestedLimit
	if limit <= 0 {
		limit = 1024
	}
	if limit > maxProtocolLogChunkSize {
		limit = maxProtocolLogChunkSize
	}

	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)

	meta := logutil.ReadLogMeta(logPath)

	file, err := os.Open(logPath)
	if err != nil {
		return emptyLogResult(offset, run.Status.IsTerminal()), nil
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}

	fileSize := fileInfo.Size()

	// Translate the virtual offset (which is continuous across rotations)
	// into a file-local position.
	fileOffset := offset - meta.RotatedBytes
	if fileOffset < 0 {
		fileOffset = 0
	}
	if fileOffset > fileSize {
		fileOffset = fileSize
	}

	if _, err := file.Seek(fileOffset, io.SeekStart); err != nil {
		return nil, err
	}

	buffer := make([]byte, limit)
	readBytes, readErr := file.Read(buffer)
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}

	data := buffer[:readBytes]
	virtualOffset := meta.RotatedBytes + fileOffset
	final := (fileOffset+int64(readBytes)) >= fileSize && run.Status.IsTerminal()

	return &logReadResult{
		Data:   data,
		Offset: virtualOffset,
		Final:  final,
	}, nil
}

func getLogFileSize(run *model.Run, logDir string) int64 {
	if run == nil {
		return 0
	}
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	meta := logutil.ReadLogMeta(logPath)
	info, err := os.Stat(logPath)
	if err != nil {
		return meta.RotatedBytes
	}
	return meta.RotatedBytes + info.Size()
}
