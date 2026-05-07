// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	// ScanBufferSize is the buffer size for scanning log files.
	ScanBufferSize = 32 * 1024
)

// Stream identifiers used in LogLineEvent payloads and on-disk prefix mapping.
// These match the values published on the wire ("stdout" / "stderr" / "system").
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
	StreamSystem = "system"
)

// FormatLine returns the on-disk representation of a single line for the given
// stream. Output is always exactly one '\n'-terminated line.
//
//   - stdout: text + "\n"
//   - stderr: "[ERR] " + text + "\n"
//   - anything else (system, custom prefixes): "[" + uppercase(stream) + "] " + text + "\n"
//
// `text` should be the raw content WITHOUT trailing newline. Existing trailing
// newlines are tolerated and stripped before re-appending exactly one.
func FormatLine(text, stream string) string {
	trimmed := strings.TrimSuffix(text, "\n")
	switch stream {
	case StreamStdout:
		return trimmed + "\n"
	case StreamStderr:
		return "[ERR] " + trimmed + "\n"
	default:
		return "[" + strings.ToUpper(stream) + "] " + trimmed + "\n"
	}
}

// ParseStreamPrefix splits an on-disk log line into its (stream, text) parts.
// Recognized prefixes:
//   - "[ERR] foo" → ("stderr", "foo")
//   - "[<ts>] [SYSTEM] foo" → ("system", "foo") (timestamp prefix tolerated)
//   - "[SYSTEM] foo" → ("system", "foo")
//   - anything else → ("stdout", line as-is)
//
// The trailing newline (if any) is stripped from the returned text.
func ParseStreamPrefix(line string) (stream, text string) {
	trimmed := strings.TrimSuffix(line, "\n")
	if rest, ok := strings.CutPrefix(trimmed, "[ERR] "); ok {
		return StreamStderr, rest
	}
	if rest, ok := stripSystemPrefix(trimmed); ok {
		return StreamSystem, rest
	}
	return StreamStdout, trimmed
}

// stripSystemPrefix peels an optional "[<ts>] " prefix followed by "[SYSTEM] ".
// Returns (rest, true) on match, ("", false) otherwise. Never panics.
func stripSystemPrefix(line string) (string, bool) {
	rest := line
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "] ")
		if end > 0 {
			head := rest[1:end]
			if head != "SYSTEM" && len(head) >= 19 {
				rest = rest[end+2:]
			}
		}
	}
	if r, ok := strings.CutPrefix(rest, "[SYSTEM] "); ok {
		return r, true
	}
	return "", false
}

// CountTailLines opens logPath, reads its index, and returns the total number
// of lines across rotated and current segments. Returns 0 when the file does
// not exist. Closes its file handle before returning.
func CountTailLines(logPath string) int64 {
	f, err := os.Open(logPath)
	if err != nil {
		return 0
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0
	}
	indices, _ := ReadLogIndex(logPath + ".idx")
	meta := ReadLogMeta(logPath)
	return int64(CalculateTotalLines(f, indices, info.Size(), meta))
}

// LogLineRecord is one line read back from disk by ReadLineRange. LineNum is
// the absolute line index (= rotated_lines + offset_within_segment), Stream
// and Text are the parsed parts of the on-disk representation.
type LogLineRecord struct {
	LineNum int64
	Stream  string
	Text    string
}

// ReadLineRange returns up to `limit` lines starting at the absolute line
// number `from`. When `from` is negative, the request is interpreted as a
// tail: the last (-from) lines available. The returned slice is sorted by
// ascending LineNum.
//
// firstAvailable is the lowest line number that survives on disk (lines below
// this were rotated away). totalLines is the total number of lines produced
// across all segments.
//
// limit > 0 caps the returned slice; pass 0 for an internal default.
func ReadLineRange(logPath string, from, limit int64) (lines []LogLineRecord, firstAvailable, totalLines int64, err error) {
	if limit <= 0 {
		limit = 1000
	}
	meta := ReadLogMeta(logPath)
	firstAvailable = meta.RotatedLines

	file, openErr := os.Open(logPath)
	if openErr != nil {
		if os.IsNotExist(openErr) {
			return nil, firstAvailable, firstAvailable, nil
		}
		return nil, firstAvailable, firstAvailable, openErr
	}
	defer file.Close()

	stat, statErr := file.Stat()
	if statErr != nil {
		return nil, firstAvailable, firstAvailable, statErr
	}
	indices, _ := ReadLogIndex(logPath + ".idx")
	totalLines = int64(CalculateTotalLines(file, indices, stat.Size(), meta))

	if totalLines <= firstAvailable {
		return nil, firstAvailable, totalLines, nil
	}

	startLine := from
	if startLine < 0 {
		startLine = totalLines + from
	}
	if startLine < firstAvailable {
		startLine = firstAvailable
	}
	if startLine >= totalLines {
		return nil, firstAvailable, totalLines, nil
	}

	startOffset := CalculateLineOffset(file, indices, int(startLine), meta)
	if _, seekErr := file.Seek(startOffset, io.SeekStart); seekErr != nil {
		return nil, firstAvailable, totalLines, seekErr
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, ScanBufferSize), 1024*1024)
	current := startLine
	for scanner.Scan() && int64(len(lines)) < limit {
		stream, text := ParseStreamPrefix(scanner.Text())
		lines = append(lines, LogLineRecord{
			LineNum: current,
			Stream:  stream,
			Text:    text,
		})
		current++
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return lines, firstAvailable, totalLines, scanErr
	}
	return lines, firstAvailable, totalLines, nil
}

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
