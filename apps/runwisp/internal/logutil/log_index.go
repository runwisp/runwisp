// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"strconv"
)

const (
	// ScanBufferSize is the buffer size for scanning log files.
	ScanBufferSize = 32 * 1024
)

func ReadLogIndex(idxPath string) ([]int64, error) {
	f, err := os.Open(idxPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	count := info.Size() / 8
	indices := make([]int64, count)

	for i := 0; i < int(count); i++ {
		var offset uint64
		if err := binary.Read(f, binary.LittleEndian, &offset); err != nil {
			return nil, err
		}
		indices[i] = int64(offset)
	}

	return indices, nil
}

// ScanOffset scans a reader from startPos, counting newlines.
// If linesToSkip < 0, counts all remaining lines.
func ScanOffset(rs io.ReadSeeker, startPos int64, linesToSkip int) (int64, int, error) {
	if _, err := rs.Seek(startPos, io.SeekStart); err != nil {
		return startPos, 0, err
	}

	if linesToSkip < 0 {
		scanner := bufio.NewScanner(rs)
		linesFound := 0
		for scanner.Scan() {
			linesFound++
		}
		if err := scanner.Err(); err != nil {
			return startPos, linesFound, err
		}
		currentPos, _ := rs.Seek(0, io.SeekCurrent)
		return currentPos, linesFound, nil
	}

	currentPos := startPos
	linesFound := 0
	buf := make([]byte, ScanBufferSize)

	for linesFound < linesToSkip {
		n, err := rs.Read(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				if buf[i] == '\n' {
					linesFound++
					if linesFound == linesToSkip {
						return currentPos + int64(i) + 1, linesFound, nil
					}
				}
			}
			currentPos += int64(n)
		}
		if err != nil {
			return currentPos, linesFound, err
		}
	}
	return currentPos, linesFound, nil
}

// CalculateTotalLines returns the total number of lines across rotated
// and current log segments.
func CalculateTotalLines(rs io.ReadSeeker, indices []int64, fileSize int64, meta LogMeta) int {
	if meta.Finalized {
		return int(meta.RotatedLines + meta.FinalLines)
	}

	var currentLines int
	if len(indices) == 0 {
		if fileSize > 0 {
			_, lines, _ := ScanOffset(rs, 0, -1)
			currentLines = lines
		}
	} else {
		lastIdx := len(indices) - 1
		_, tailLines, _ := ScanOffset(rs, indices[lastIdx], -1)
		currentLines = (lastIdx * LogIndexInterval) + tailLines
	}

	return int(meta.RotatedLines) + currentLines
}

func CalculateLineOffset(rs io.ReadSeeker, indices []int64, line int, meta LogMeta) int64 {
	localLine := line - int(meta.RotatedLines)
	if localLine < 0 {
		localLine = 0
	}

	if len(indices) == 0 {
		offset, _, _ := ScanOffset(rs, 0, localLine)
		return offset
	}

	chunkIdx := localLine / LogIndexInterval
	if chunkIdx >= len(indices) {
		chunkIdx = len(indices) - 1
	}
	baseOffset := indices[chunkIdx]
	skip := localLine - (chunkIdx * LogIndexInterval)
	offset, _, _ := ScanOffset(rs, baseOffset, skip)
	return offset
}

// ParseLogOffset parses a string offset; negative values count from end.
func ParseLogOffset(offsetStr string, fileSize int64) int64 {
	if offsetStr == "" {
		return 0
	}
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil {
		return 0
	}
	if offset < 0 {
		if offset < -fileSize {
			return 0
		}
		offset = fileSize + offset
		if offset < 0 {
			offset = 0
		}
	}
	if offset > fileSize {
		offset = fileSize
	}
	return offset
}

// ReadWithLineBoundaries reads up to maxBytes from the reader.
func ReadWithLineBoundaries(r io.Reader, maxBytes int64) ([]byte, error) {
	limitedReader := io.LimitReader(r, maxBytes)
	data, err := io.ReadAll(limitedReader)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return data, nil
}
