// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	// ScanBufferSize is the buffer size for scanning log files.
	ScanBufferSize = 32 * 1024
	// maxLogLineSize is the longest log line newLineScanner accepts.
	maxLogLineSize = 1024 * 1024
)

// newLineScanner returns a line scanner over r sized for log files.
func newLineScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, ScanBufferSize), maxLogLineSize)
	return scanner
}

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

// SystemLine formats msg as a run log's system line, the form
// ParseStreamPrefix reads back as StreamSystem.
func SystemLine(now time.Time, msg string) string {
	return fmt.Sprintf("[%s] [SYSTEM] %s\n", now.Format("2006-01-02 15:04:05"), msg)
}

// AppendSystemLine appends a system line to a run log no LogWriter has open,
// such as one left behind by a daemon that crashed mid-run.
func AppendSystemLine(logPath string, now time.Time, msg string) error {
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(SystemLine(now, msg))
	return errors.Join(err, f.Close())
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
	total, countErr := CalculateTotalLines(file, indices, stat.Size(), meta)
	if countErr != nil {
		return nil, firstAvailable, firstAvailable, countErr
	}
	totalLines = int64(total)
	if totalLines <= firstAvailable {
		return nil, firstAvailable, totalLines, nil
	}

	startLine := ResolveStartLine(from, firstAvailable, totalLines)
	if startLine >= totalLines {
		return nil, firstAvailable, totalLines, nil
	}

	// Either starting directly in the current segment, or continuing into it
	// right after the .prev segment ended, which lines up at RotatedLines
	// (see rotateTail) even when .prev yielded nothing.
	currentStart := startLine
	if prevExists && startLine < meta.RotatedLines {
		lines, err = readSegmentRange(prevPath, meta.PrevStart, startLine, limit)
		if err != nil {
			return lines, firstAvailable, totalLines, err
		}
		if int64(len(lines)) >= limit {
			return lines, firstAvailable, totalLines, nil
		}
		currentStart = meta.RotatedLines
	}

	more, scanErr := readCurrentSegment(file, indices, meta, currentStart, limit-int64(len(lines)))
	lines = append(lines, more...)
	return lines, firstAvailable, totalLines, scanErr
}

// readCurrentSegment reads up to limit lines of the current segment starting at
// absolute line startLine, seeking through the sidecar index.
func readCurrentSegment(file *os.File, indices []int64, meta LogMeta, startLine, limit int64) ([]LogLineRecord, error) {
	startOffset := CalculateLineOffset(file, indices, int(startLine), meta)
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return nil, err
	}
	scanner := newLineScanner(file)
	return collectLines(scanner, startLine, limit)
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

// ResolveStartLine converts `from` into an absolute line number: negative
// values count back from totalLines (a tail request), then the result is
// clamped to the oldest surviving line, firstAvailable.
func ResolveStartLine(from, firstAvailable, totalLines int64) int64 {
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
	f, start, err := openSegmentAt(path, nil, segmentStart, startAt)
	if f == nil {
		return nil, err
	}
	defer f.Close()
	return collectLines(newLineScanner(f), start, limit)
}

// openSegmentAt opens a segment whose first line is absolute line segmentStart
// and seeks to absolute line max(line, segmentStart), returned as start.
// indices is the segment's sidecar index, or nil to skip linearly. A missing
// segment returns a nil file and nil error.
func openSegmentAt(path string, indices []int64, segmentStart, line int64) (f *os.File, start int64, err error) {
	f, err = os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return nil, 0, err
	}
	start = max(line, segmentStart)
	if start > segmentStart {
		offset, err := calculateLineOffset(f, indices, start-segmentStart)
		if err == nil || errors.Is(err, io.EOF) {
			_, err = f.Seek(offset, io.SeekStart)
		}
		if err != nil {
			f.Close()
			return nil, 0, err
		}
	}
	return f, start, nil
}

// ScanLines streams a run's surviving lines starting at firstN, in display
// order. Line numbers are absolute across both segments. A false return from
// visit stops the scan successfully.
func ScanLines(ctx context.Context, logPath string, firstN int64, visit func(LogLineRecord) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sc := ReadSidecar(logPath)
	meta := sc.Meta
	if firstN < meta.RotatedLines {
		done, err := scanSegment(ctx, PrevPath(logPath), nil, meta.PrevStart, firstN, visit)
		if err != nil || done {
			return err
		}
	}
	_, err := scanSegment(ctx, logPath, sc.Index, meta.RotatedLines, firstN, visit)
	return err
}

// scanSegment returns done when visit asked to stop. A missing segment is
// skipped, since there may be no .prev or the run may have been pruned.
func scanSegment(ctx context.Context, path string, indices []int64, segmentStart, firstN int64, visit func(LogLineRecord) bool) (bool, error) {
	f, current, err := openSegmentAt(path, indices, segmentStart, firstN)
	if f == nil {
		return false, err
	}
	defer f.Close()
	scanner := newLineScanner(f)
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

// scanAllLines counts all remaining lines from the current reader position:
// every newline, plus one for a trailing line that has no newline yet (a run
// still writing). Unlike bufio.Scanner it has no per-line length limit.
func scanAllLines(rs io.Reader, startPos int64) (int64, int, error) {
	buf := make([]byte, ScanBufferSize)
	pos := startPos
	lines := 0
	var last byte
	for {
		n, err := rs.Read(buf)
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
			pos += int64(n)
		}
		if errors.Is(err, io.EOF) {
			if pos > startPos && last != '\n' {
				lines++
			}
			return pos, lines, nil
		}
		if err != nil {
			return pos, lines, err
		}
	}
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
func CalculateTotalLines(rs io.ReadSeeker, indices []int64, fileSize int64, meta LogMeta) (int, error) {
	if meta.Finalized {
		return int(meta.RotatedLines + meta.FinalLines), nil
	}

	var currentLines int
	if len(indices) == 0 {
		if fileSize > 0 {
			_, lines, err := ScanOffset(rs, 0, -1)
			if err != nil {
				return 0, err
			}
			currentLines = lines
		}
	} else {
		lastIdx := len(indices) - 1
		_, tailLines, err := ScanOffset(rs, indices[lastIdx], -1)
		if err != nil {
			return 0, err
		}
		currentLines = (lastIdx * LogIndexInterval) + tailLines
	}

	return int(meta.RotatedLines) + currentLines, nil
}

func CalculateLineOffset(rs io.ReadSeeker, indices []int64, line int, meta LogMeta) int64 {
	offset, _ := calculateLineOffset(rs, indices, max(0, int64(line)-meta.RotatedLines))
	return offset
}

func calculateLineOffset(rs io.ReadSeeker, indices []int64, localLine int64) (int64, error) {
	var baseOffset int64
	skip := localLine
	if len(indices) > 0 {
		chunkIdx := min(localLine/LogIndexInterval, int64(len(indices)-1))
		baseOffset = indices[chunkIdx]
		skip -= chunkIdx * LogIndexInterval
	}
	offset, _, err := ScanOffset(rs, baseOffset, int(skip))
	return offset, err
}
