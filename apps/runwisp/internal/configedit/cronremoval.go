// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"fmt"
	"os"
	"strings"

	"github.com/runwisp/runwisp/internal/config"
)

// This file is the crontab half of promoting a live cron-sourced task.
// Promote copies the block a crontab produced into root/runwisp.toml
// (block.go); this is what turns that copy into a real move instead of a
// second, silent definition of the same job — the exact source line comes out
// of the crontab in the same transaction, so a system cron daemon that is
// still alive and unmasked has nothing left to fire a second time.
//
// The verification here is deliberately conservative: a name whose recorded
// line can't be re-confirmed byte-for-byte, right now, refuses the whole
// promotion rather than falling back to "leave the crontab alone" — that
// fallback is the exact bug this exists to close, so there is no safe
// degraded mode to fall back to.

// CronRemoval is one crontab line a promotion will delete: the file it lives
// in, its 1-based line, and the exact text being removed. It is what a
// --dry-run shows for the crontab side of the move, and what a completed
// promote reports having removed.
type CronRemoval struct {
	Name string
	File string
	Line int
	Text string
}

// CronSourceMismatchError reports that a cron-sourced task's recorded source
// line could not be re-confirmed against the crontab on disk — it was edited,
// shortened, removed, or never captured in the first place. Promoting on a
// stale line number would risk deleting the wrong job, so this refuses
// instead: nothing is written, for any task in the batch.
type CronSourceMismatchError struct {
	Name, File, Reason string
	Line               int
}

func (e *CronSourceMismatchError) Error() string {
	if e.File == "" {
		return fmt.Sprintf("task %q: %s", e.Name, e.Reason)
	}
	return fmt.Sprintf("%s:%d (task %q): %s", e.File, e.Line, e.Name, e.Reason)
}

// PreviewCronRemovals resolves what promoting the named cron-sourced tasks
// would remove from their crontabs, re-reading each source file fresh and
// refusing — before anything is written, for the whole batch — if any
// recorded line has moved, changed, or disappeared since the config was
// loaded. Callers pass only names already known to be cron-sourced (see
// splitByProvenance); a name with no recorded line is treated the same as a
// changed one, since either way promote cannot safely say what to delete.
func PreviewCronRemovals(names []string, cfg *config.Config) ([]CronRemoval, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make([]CronRemoval, 0, len(names))
	for _, name := range names {
		file, line, text, ok := cfg.CronSourceLine(name)
		if !ok {
			return nil, &CronSourceMismatchError{
				Name:   name,
				Reason: "RunWisp has no recorded crontab line for it, so promoting would leave the source untouched and unrecognisable to a later reload",
			}
		}
		if err := verifyCronLine(file, line, text); err != nil {
			return nil, &CronSourceMismatchError{Name: name, File: file, Line: line, Reason: err.Error()}
		}
		out = append(out, CronRemoval{Name: name, File: file, Line: line, Text: text})
	}
	return out, nil
}

// verifyCronLine re-reads path and confirms line n still holds exactly want,
// refusing if the file is gone, shorter than it was, or that line has changed
// — the "don't remove a changed or ambiguous line based on a stale line
// number" rule, checked at the last possible moment before a write.
func verifyCronLine(path string, n int, want string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read it anymore: %w", err)
	}
	lines := splitCronLines(data)
	if n < 1 || n > len(lines) {
		return fmt.Errorf("it now has %d line(s), so line %d no longer exists", len(lines), n)
	}
	if got := lines[n-1]; got != want {
		return fmt.Errorf("that line changed since it was read (was %q, now %q)", want, got)
	}
	return nil
}

// cronRemovalEdits groups removals by file and computes each file's new
// content with just those lines deleted — every other byte, including
// comments, blank lines, and jobs not being promoted, kept exactly as it was.
func cronRemovalEdits(removals []CronRemoval) (map[string][]byte, error) {
	drop := make(map[string]map[int]bool, len(removals))
	for _, r := range removals {
		if drop[r.File] == nil {
			drop[r.File] = map[int]bool{}
		}
		drop[r.File][r.Line] = true
	}

	out := make(map[string][]byte, len(drop))
	for file, lineSet := range drop {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		out[file] = removeCronLines(data, lineSet)
	}
	return out, nil
}

// removeCronLines returns data with every 1-based line number in drop deleted.
// Kept lines are rejoined with the same terminator convention split by
// splitCronLinesKeepEnds, so a file missing its final newline stays that way.
func removeCronLines(data []byte, drop map[int]bool) []byte {
	lines := splitCronLinesKeepEnds(data)
	var kept strings.Builder
	for i, ln := range lines {
		if drop[i+1] {
			continue
		}
		kept.WriteString(ln)
	}
	return []byte(kept.String())
}

// splitCronLines splits raw crontab bytes into 1-based lines with terminators
// stripped, matching bufio.Scanner's line count (a trailing newline must not
// manufacture a phantom extra line) so "line N" means the same thing here as
// it does in importer/cron.go's Item.Line and in config package's own
// splitCronLines, which this mirrors exactly.
func splitCronLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, "\r")
	}
	return lines
}

// splitCronLinesKeepEnds splits raw crontab bytes into 1-based lines, each
// retaining its own terminator, so deleting a subset and rejoining the rest
// reproduces every untouched byte exactly.
func splitCronLinesKeepEnds(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, string(data[start:i+1]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}
