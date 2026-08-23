// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
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
// When a `.log.prev` segment is present, requests that reach back that far are
// served from it — numbered from meta.PrevStart, per its doc — before falling
// through to the current segment, the same two-segment order ScanLines uses.
// Only lines below PrevStart (or RotatedLines, absent a `.prev`) are treated
// as genuinely gone.
//
// limit > 0 caps the returned slice; pass 0 for an internal default.
func ReadLineRange(logPath string, from, limit int64) (lines []LogLineRecord, firstAvailable, totalLines int64, err error) {
	if limit <= 0 {
		limit = 1000
	}
	sc := ReadSidecar(logPath)
	meta := sc.Meta
	prevPath, prevExists, firstAvailable := resolvePrevSegment(logPath, meta)

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
	indices := sc.Index
	totalLines = int64(CalculateTotalLines(file, indices, stat.Size(), meta))
	if totalLines <= firstAvailable {
		return nil, firstAvailable, totalLines, nil
	}

	startLine := resolveStartLine(from, firstAvailable, totalLines)
	if startLine >= totalLines {
		return nil, firstAvailable, totalLines, nil
	}

	if prevExists && startLine < meta.RotatedLines {
		lines, err = readSegmentRange(prevPath, meta.PrevStart, startLine, limit)
		if err != nil {
			return lines, firstAvailable, totalLines, err
		}
		if int64(len(lines)) >= limit {
			return lines, firstAvailable, totalLines, nil
		}
	}

	// Either starting directly in the current segment, or continuing into it
	// right after the .prev segment ended (which lines up exactly at
	// RotatedLines by construction — see rotateTail).
	currentStart := startLine
	if len(lines) > 0 {
		currentStart = meta.RotatedLines
	}

	startOffset := CalculateLineOffset(file, indices, int(currentStart), meta)
	if _, seekErr := file.Seek(startOffset, io.SeekStart); seekErr != nil {
		return lines, firstAvailable, totalLines, seekErr
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, ScanBufferSize), 1024*1024)
	more, scanErr := collectLines(scanner, currentStart, limit-int64(len(lines)))
	lines = append(lines, more...)
	if scanErr != nil {
		return lines, firstAvailable, totalLines, scanErr
	}
	return lines, firstAvailable, totalLines, nil
}

// resolvePrevSegment reports whether logPath has a `.log.prev` segment and
// returns the resulting firstAvailable: numbered from meta.PrevStart when a
// `.prev` survives, else meta.RotatedLines.
func resolvePrevSegment(logPath string, meta LogMeta) (prevPath string, exists bool, firstAvailable int64) {
	prevPath = PrevPath(logPath)
	_, statErr := os.Stat(prevPath)
	exists = statErr == nil
	firstAvailable = meta.RotatedLines
	if exists {
		firstAvailable = meta.PrevStart
	}
	return prevPath, exists, firstAvailable
}

// resolveStartLine converts `from` into an absolute line number: negative
// values count back from totalLines (a tail request), then the result is
// clamped to the oldest surviving line, firstAvailable.
func resolveStartLine(from, firstAvailable, totalLines int64) int64 {
	start := from
	if start < 0 {
		start = totalLines + from
	}
	if start < firstAvailable {
		start = firstAvailable
	}
	return start
}

// collectLines scans up to `limit` lines from scanner, numbering them
// sequentially from startLineNum. Shared by ReadLineRange's current-segment
// read and readSegmentRange's `.prev` read.
func collectLines(scanner *bufio.Scanner, startLineNum, limit int64) ([]LogLineRecord, error) {
	var lines []LogLineRecord
	current := startLineNum
	for scanner.Scan() && int64(len(lines)) < limit {
		stream, text := ParseStreamPrefix(scanner.Text())
		lines = append(lines, LogLineRecord{LineNum: current, Stream: stream, Text: text})
		current++
	}
	return lines, scanner.Err()
}

// readSegmentRange reads up to `limit` lines from a single segment file
// starting at the absolute line number `startAt`, which must be >=
// segmentStart (the absolute line number the segment's first line
// represents). The segment has no byte-offset index of its own (that index is
// reset on rotation), so the skip is a linear scan — acceptable since this
// only runs for reads reaching back into `.prev`, not the common current-
// segment path, which still uses the sidecar index via CalculateLineOffset.
func readSegmentRange(path string, segmentStart, startAt, limit int64) ([]LogLineRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	skip := int(startAt - segmentStart)
	if skip < 0 {
		skip = 0
	}
	offset, _, scanErr := ScanOffset(f, 0, skip)
	if scanErr != nil && !errors.Is(scanErr, io.EOF) {
		return nil, scanErr
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, ScanBufferSize), 1024*1024)
	return collectLines(scanner, segmentStart+int64(skip), limit)
}

// ScanLines streams every on-disk line of a run in display order: the
// rotated-away segment (`.log.prev`) first, then the current segment.
// Each line is numbered with its absolute (cumulative) line index, matching
// the numbering used by ReadLineRange and the SSE log wire.
//
// visit is invoked for every line; it returns false to stop the scan early
// (treated as success — ScanLines returns nil). ctx.Err() is checked
// between lines so a cancelled context aborts within one iteration.
func ScanLines(ctx context.Context, logPath string, visit func(LogLineRecord) bool) error {
	meta := ReadLogMeta(logPath)

	done, err := scanSegment(ctx, PrevPath(logPath), meta.PrevStart, visit)
	if err != nil || done {
		return err
	}
	_, err = scanSegment(ctx, logPath, meta.RotatedLines, visit)
	return err
}

// scanSegment scans one segment file, numbering lines starting at startLine.
// Returns (done=true, nil) when visit asked to stop, so the caller can
// short-circuit subsequent segments. Returns (false, nil) if the file does
// not exist — rotation may not have produced a .prev yet.
func scanSegment(ctx context.Context, path string, startLine int64, visit func(LogLineRecord) bool) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, ScanBufferSize), 1024*1024)
	current := startLine
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		stream, text := ParseStreamPrefix(scanner.Text())
		if !visit(LogLineRecord{LineNum: current, Stream: stream, Text: text}) {
			return true, nil
		}
		current++
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// ScanOffset scans a reader from startPos, counting newlines.
// If linesToSkip < 0, counts all remaining lines.
func ScanOffset(rs io.ReadSeeker, startPos int64, linesToSkip int) (int64, int, error) {
	if _, err := rs.Seek(startPos, io.SeekStart); err != nil {
		return startPos, 0, err
	}
	if linesToSkip < 0 {
		return scanAllLines(rs, startPos)
	}
	return scanToLine(rs, startPos, linesToSkip)
}

// scanAllLines counts all remaining newlines from the current reader position.
func scanAllLines(rs io.ReadSeeker, startPos int64) (int64, int, error) {
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

// scanToLine advances through the reader counting newlines until linesToSkip
// have been passed, returning the byte offset after the last counted newline.
func scanToLine(rs io.ReadSeeker, startPos int64, linesToSkip int) (int64, int, error) {
	currentPos := startPos
	linesFound := 0
	buf := make([]byte, ScanBufferSize)

	for linesFound < linesToSkip {
		n, err := rs.Read(buf)
		if n > 0 {
			offset, newCount := countNewlinesInBuf(buf[:n], linesFound, linesToSkip)
			linesFound = newCount
			if offset >= 0 {
				return currentPos + int64(offset), linesFound, nil
			}
			currentPos += int64(n)
		}
		if err != nil {
			return currentPos, linesFound, err
		}
	}
	return currentPos, linesFound, nil
}

// countNewlinesInBuf scans buf for newlines, stopping when linesToSkip have
// been seen in total. Returns the byte offset after the linesToSkip-th newline
// within buf and the updated total count; offset is -1 when buf did not
// contain enough newlines to reach linesToSkip.
func countNewlinesInBuf(buf []byte, linesFound, linesToSkip int) (offset, newLinesFound int) {
	for i, b := range buf {
		if b != '\n' {
			continue
		}
		linesFound++
		if linesFound == linesToSkip {
			return i + 1, linesFound
		}
	}
	return -1, linesFound
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
