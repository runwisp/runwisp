// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/internal/config"
)

// This file is the crontab half of promoting a live cron-sourced task. Promote
// copies the block a crontab produced into root/runwisp.toml (block.go); this
// turns that copy into a real handover by commenting the source line out — a
// live cron daemon ignores a '#' line, so the job it was firing stops there,
// and RunWisp (now the only definition) takes over. The line is commented, not
// deleted, so the operator can still see what moved and where, and undo it by
// hand if they need to.
//
// The recorded line is verified byte-for-byte against the file on disk right
// before it is rewritten; any name whose line can't be re-confirmed refuses the
// whole batch rather than commenting out the wrong line.

// CronCommentOut is one crontab line a promotion will comment out: the file it
// lives in, its 1-based line, and the exact (still-uncommented) text. It is
// what a --dry-run shows for the crontab side of the move, and what a completed
// promote reports.
type CronCommentOut struct {
	Name string
	File string
	Line int
	Text string
}

// CronSourceMismatchError reports that a cron-sourced task's recorded source
// line could not be re-confirmed against the crontab on disk — it was edited,
// shortened, removed, or never captured. Promoting on a stale line number would
// risk touching the wrong job, so this refuses instead: nothing is written, for
// any task in the batch.
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

// PlanCronCommentOuts resolves what promoting the named cron-sourced tasks
// would change in their crontabs: the lines to comment out, and the rewritten
// content for each affected file with those lines commented and annotated with
// rootPath. Each file is read once and every recorded line is verified against
// those same bytes before the edit is built, so verification and the rewrite
// can never disagree. It refuses the whole batch — before anything is written —
// if any recorded line has moved, changed, or gone missing since the config was
// loaded. Callers pass only names already known to be cron-sourced (see
// splitByProvenance); a name with no recorded line refuses too, since promote
// then cannot say which line to touch.
func PlanCronCommentOuts(names []string, cfg *config.Config, rootPath string) ([]CronCommentOut, map[string][]byte, error) {
	if len(names) == 0 {
		return nil, nil, nil
	}
	if abs, err := filepath.Abs(rootPath); err == nil {
		rootPath = abs
	}

	byFile, order, outs, err := groupCronTargets(names, cfg)
	if err != nil {
		return nil, nil, err
	}
	edits := make(map[string][]byte, len(order))
	for _, file := range order {
		data, err := commentOutFile(file, byFile[file], rootPath)
		if err != nil {
			return nil, nil, err
		}
		edits[file] = data
	}
	return outs, edits, nil
}

// cronTarget is one recorded (name, line, text) a promotion means to comment
// out, grouped by the crontab file it lives in.
type cronTarget struct {
	name, text string
	line       int
}

// groupCronTargets resolves each name's recorded crontab line and buckets the
// targets by file (order preserving first-seen file order for determinism). It
// refuses if any name has no recorded line — promote then can't say what to
// touch. outs is the flat, names-ordered report of what would be commented out.
func groupCronTargets(names []string, cfg *config.Config) (map[string][]cronTarget, []string, []CronCommentOut, error) {
	byFile := map[string][]cronTarget{}
	var order []string
	var outs []CronCommentOut
	for _, name := range names {
		file, line, text, ok := cfg.CronSourceLine(name)
		if !ok {
			return nil, nil, nil, &CronSourceMismatchError{
				Name:   name,
				Reason: "RunWisp has no recorded crontab line for it, so promote can't say which line to comment out",
			}
		}
		if _, seen := byFile[file]; !seen {
			order = append(order, file)
		}
		byFile[file] = append(byFile[file], cronTarget{name: name, text: text, line: line})
		outs = append(outs, CronCommentOut{Name: name, File: file, Line: line, Text: text})
	}
	return byFile, order, outs, nil
}

// commentOutFile reads one crontab once, verifies every target against those
// exact bytes, and returns the rewritten content with the targets commented
// out — so verification and the rewrite can never disagree. Any mismatch
// refuses the whole batch.
func commentOutFile(file string, targets []cronTarget, rootPath string) ([]byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		t := targets[0]
		return nil, &CronSourceMismatchError{Name: t.name, File: file, Line: t.line,
			Reason: fmt.Sprintf("cannot read it anymore: %v", err)}
	}
	lines := splitCronLinesKeepEnds(data)
	drop := make(map[int]bool, len(targets))
	for _, t := range targets {
		if err := verifyCronTarget(lines, t, file); err != nil {
			return nil, err
		}
		drop[t.line] = true
	}
	return commentOutCronLines(lines, drop, rootPath), nil
}

// verifyCronTarget confirms t's recorded line still exists and still holds
// exactly t.text in lines, refusing on a shifted line number or changed text —
// the "never touch a line on a stale line number alone" rule.
func verifyCronTarget(lines []string, t cronTarget, file string) error {
	if t.line < 1 || t.line > len(lines) {
		return &CronSourceMismatchError{Name: t.name, File: file, Line: t.line,
			Reason: fmt.Sprintf("it now has %d line(s), so line %d no longer exists", len(lines), t.line)}
	}
	if got := strings.TrimRight(lines[t.line-1], "\r\n"); got != t.text {
		return &CronSourceMismatchError{Name: t.name, File: file, Line: t.line,
			Reason: fmt.Sprintf("that line changed since it was read (was %q, now %q)", t.text, got)}
	}
	return nil
}

// commentOutCronLines rewrites the split crontab, prefixing each 1-based line in
// drop with '#' and inserting an annotation above it that points at the
// runwisp.toml the job moved to. Every other byte — comments, blank lines, jobs
// not being promoted, and each kept line's own terminator — is preserved, so a
// file missing its final newline stays that way.
func commentOutCronLines(lines []string, drop map[int]bool, rootPath string) []byte {
	var b strings.Builder
	for i, ln := range lines {
		if drop[i+1] {
			b.WriteString("# runwisp: this job was promoted to " + rootPath)
			b.WriteString(lineTerminator(ln))
			b.WriteByte('#')
		}
		b.WriteString(ln)
	}
	return []byte(b.String())
}

// lineTerminator returns the terminator a kept line carries, so an annotation
// inserted above it matches the file's newline convention. A final line with no
// terminator falls back to "\n" — the annotation still needs to end somewhere.
func lineTerminator(s string) string {
	if strings.HasSuffix(s, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// splitCronLinesKeepEnds splits raw crontab bytes into 1-based lines, each
// retaining its own terminator, so commenting a subset and rejoining the rest
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
