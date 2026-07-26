// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/runwisp/runwisp/internal/importer"
	"github.com/runwisp/runwisp/internal/model"
)

// This file implements `[daemon] include_cron`: real crontabs read as live task
// sources on every load and reload, so `crontab -e` + `runwisp reload` is the
// whole workflow and the operator converts to TOML at their own pace.
//
// The route from crontab to task is deliberately the long way round — parse with
// internal/importer, render its TOML, decode that through the *unmodified*
// parseWire. A direct crontab→model.Task path would be a second answer to
// "what does this cron line mean", and the two would drift the first time one of
// them learned something. Going through the same text `runwisp import` writes
// also makes `runwisp promote` provably behaviour-preserving: the block that
// lands in the root config is the block the daemon was already running.

// CronFinding is one thing an operator should know about a crontab RunWisp read
// live. Skipped=true means the job is not running at all; otherwise it is running
// under a name or with a caveat they would not guess from reading the crontab.
//
// Reported as durable state rather than logged once at boot: a skipped job has no
// runs, so the per-run machinery that normally keeps a failure visible has nothing
// to hang on, and the only honest substitute is state re-derived every load.
type CronFinding struct {
	// File is the crontab it came from and Line the 1-based line within it. An
	// operator looking at a crontab needs `file:line`; a derived task name they
	// have never seen is not a place they can go.
	File string
	Line int
	// Source is the best label the report row had for the job: the derived name
	// when the line parsed far enough to get one, and the raw line text when it
	// didn't. Task is the name RunWisp gave it, empty when it was skipped.
	Source string
	Task   string
	// Reason is the note's own message.
	Reason string
	// Skipped distinguishes "not running" from "running, with a caveat". Both are
	// worth a warning; only the first is a job the machine isn't doing.
	Skipped bool
}

// String renders the finding as one line for a warning list.
func (f CronFinding) String() string {
	where := f.File
	if f.Line > 0 {
		where = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	what := f.Source
	if f.Task != "" {
		what = fmt.Sprintf("%s (task %q)", f.Source, f.Task)
	}
	return fmt.Sprintf("%s: %s \u2014 %s", where, what, f.Reason)
}

// cronMerge is what mergeCronSources folded in, for the caller to record on the
// Config.
type cronMerge struct {
	globs    []string
	files    []string
	findings []CronFinding
	// blocks maps each live task name to the TOML that produced it, kept so
	// `runwisp promote` can move a cron-sourced definition into the operator's own
	// config without re-deriving it. A crontab has no TOML bytes on disk, so these
	// are the only bytes that are provably what the daemon is running.
	blocks map[string]string
}

// originSet indexes the matched files for markProvenance.
func (m cronMerge) originSet() map[string]bool {
	set := make(map[string]bool, len(m.files))
	for _, f := range m.files {
		set[f] = true
	}
	return set
}

// mergeCronSources parses every crontab matched by patterns and folds the jobs
// it can reproduce into root, recording each one's origin so path resolution and
// provenance work the same as for a TOML include.
//
// tomlMatched is the set of files [daemon].include already claimed, so a file
// pulled in twice under two different readings is a hard error rather than a
// merge conflict nobody can explain.
//
// Errors are for the file, never the job. An unreadable crontab, an untrusted
// one, or rendered TOML that won't decode rejects the whole load — which, on a
// reload, means the running task set is left exactly as it was. An individual job
// RunWisp can't reproduce is skipped and reported, because that is what crond
// does with a malformed entry and because taking down every other job in the file
// over one bad line would make include_cron unusable on a real machine.
func mergeCronSources(root *tomlConfig, patterns []string, rootDir, rootPath string,
	byName map[string]string, tomlMatched []string) (cronMerge, error) {
	if len(patterns) == 0 {
		return cronMerge{}, nil
	}

	var m cronMerge
	globs, matched, err := resolveCronIncludes(patterns, rootDir, rootPath)
	if err != nil {
		return cronMerge{}, err
	}
	m.globs = globs
	if err := assertNoIncludeOverlap(matched, tomlMatched); err != nil {
		return cronMerge{}, err
	}

	// One shared namer across every source, seeded with the names the TOML side
	// already claimed, so cron-vs-cron and cron-vs-TOML dedup both happen by
	// construction rather than by a second reconciliation pass.
	owned := ownedFromWire(root)
	for _, path := range matched {
		res, err := parseCronSource(path, owned)
		if err != nil {
			return cronMerge{}, err
		}
		if err := mergeCronResult(root, res, path, byName); err != nil {
			return cronMerge{}, err
		}
		claimOwned(owned, res)
		m.collectBlocks(res)
		m.files = append(m.files, path)
		m.findings = append(m.findings, findingsFrom(res, path)...)
	}
	return m, nil
}

// collectBlocks records the rendered TOML behind each of one parse's live tasks.
func (m *cronMerge) collectBlocks(res *importer.Result) {
	for _, it := range res.Items() {
		if !it.LiveEligible() {
			continue
		}
		if m.blocks == nil {
			m.blocks = map[string]string{}
		}
		m.blocks[it.Name] = res.BlockTOML(it.Name)
	}
}

// parseCronSource reads and parses one crontab, refusing a file that isn't
// trustworthy to take daemon-privileged shell from.
func parseCronSource(path string, owned importer.Owned) (*importer.Result, error) {
	if err := assertCronFileTrusted(path); err != nil {
		return nil, fmt.Errorf("daemon.include_cron: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read cron source %s: %w", path, err)
	}
	return importer.ParseCrontab(strings.NewReader(string(data)), cronOptionsFor(path, owned))
}

// cronOptionsFor decides how to read one crontab.
//
// System-ness comes from the path and nothing else: /etc/crontab and anything
// under a cron.d directory carry a user column, which is *privilege-reducing* —
// the column names who a job drops to. See importer.IsSystemCrontabPath.
//
// Detect is deliberately off, unlike `runwisp import cron` on the same file. Its
// per-row ambiguity sniff asks whether a line's sixth field looks like a username,
// and for a live source a suspect row is dropped rather than annotated — so with
// Detect on, an ordinary per-user line like `* * * * * echo ticked` ("echo" reads
// as a plausible username) would silently not run. That trade is the wrong way
// round: the operator naming the file is the declaration of its format, and if the
// guess is wrong anyway the job fails loudly with the error in its captured output,
// where a dropped job has no run to fail in.
//
// Note what is deliberately not here: guessing a run-as identity from a path. A
// file under /var/spool/cron/crontabs is conventionally that user's, but making a
// filename decide a uid is a privilege *escalation* dressed as a convenience.
// Naming such a file in include_cron works and runs its jobs as the daemon's own
// account, which is documented rather than inferred.
func cronOptionsFor(path string, owned importer.Owned) importer.CronOptions {
	return importer.CronOptions{
		System:   importer.IsSystemCrontabPath(path),
		Existing: owned,
		// The collision tie-breaker is the file's own name, so a derived name is a
		// function of where the job came from rather than of how many files the glob
		// happened to match first. Dropping a lexically-earlier file into
		// /etc/cron.d must not renumber the tasks already running.
		NameSuffix: cronNameSuffix(path),
	}
}

// cronNameSuffix derives the stable collision suffix from a source path: the
// basename with its extension dropped, sanitized to a task-name-safe form.
func cronNameSuffix(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return model.SanitizeTaskName(base)
}

// mergeCronResult renders the live-eligible half of a parse, decodes it through
// the ordinary wire path, and folds the entries into root.
func mergeCronResult(root *tomlConfig, res *importer.Result, path string, byName map[string]string) error {
	live := res.LiveTOML()
	wire, err := parseWire([]byte(live), filepath.Dir(path))
	if err != nil {
		// Not an operator error in any actionable sense — the crontab parsed, and
		// then this package failed to read back text it generated itself. Say so
		// plainly rather than blaming the crontab.
		return fmt.Errorf("cron source %s: RunWisp generated a config it could not read back (this is a bug): %w", path, err)
	}
	if err := recordEntryOrigins(wire, path, byName); err != nil {
		return err
	}
	mergeEntryTables(root, wire)
	return nil
}

// ownedFromWire snapshots the entry names a merged wire already claims, so the
// cron parse renames around them instead of emitting a duplicate that would fail
// the merged load.
//
// The command comes from the wire's `run`, which is what makes the dedup
// identity-aware rather than name-only: a native task with the same derived name
// but a different command is a different job, and it gets renamed and kept
// running. Dropping it on a name match alone would retire a live job with nothing
// but a warning — see importer.sameEntry.
func ownedFromWire(w *tomlConfig) importer.Owned {
	owned := make(importer.Owned, len(w.Tasks)+len(w.Services)+len(w.Compose))
	for name, t := range w.Tasks {
		owned[name] = importer.OwnedEntry{Kind: model.KindTask, Run: t.Run}
	}
	for name, s := range w.Services {
		owned[name] = importer.OwnedEntry{Kind: model.KindService, Run: s.Run}
	}
	for name := range w.Compose {
		// A compose alias has no comparable one-shot command, so an empty Run makes
		// it never match and always force a rename.
		owned[name] = importer.OwnedEntry{Kind: model.KindTask}
	}
	return owned
}

// claimOwned folds one source's emitted names into the shared Owned map so the
// next source in the glob renames around them.
func claimOwned(owned importer.Owned, res *importer.Result) {
	for _, it := range res.Items() {
		if !it.LiveEligible() {
			continue
		}
		owned[it.Name] = importer.OwnedEntry{Kind: it.Kind, Run: it.Run}
	}
}

// findingsFrom turns one parse's report rows into the findings an operator needs:
// every job that won't run, plus every job that will run under a name or with a
// caveat they wouldn't guess from the crontab.
//
// The renames matter as much as the skips. A cron task's name is derived from its
// command, so two crontabs can derive the same name and one of them gets renamed.
// That is the right outcome — better than retiring a live job — but an operator
// who wrote `backup` and finds `backup-db` in the UI has to be told why, or the
// task looks like something RunWisp invented.
func findingsFrom(res *importer.Result, path string) []CronFinding {
	var out []CronFinding
	for _, it := range res.Items() {
		live := it.LiveEligible()
		for _, n := range it.Notes {
			if !n.Blocking() && !isRenameNote(n) {
				continue
			}
			// A dropped job's unsafe-live note is the reason it dropped; anything else
			// on that row is downstream of a job that isn't running, so one finding per
			// row is the whole story.
			if !live && !n.UnsafeLive() {
				continue
			}
			f := CronFinding{File: path, Line: it.Line, Source: it.Source, Reason: n.Message, Skipped: !live}
			if live {
				f.Task = it.Name
			}
			out = append(out, f)
		}
	}
	return out
}

// isRenameNote reports whether a note explains that a job is running under a name
// the crontab doesn't mention. Non-blocking — the job runs fine — but the operator
// cannot find the task without being told.
func isRenameNote(n importer.Note) bool {
	return n.Kind == importer.NoteRenamedOwned || n.Kind == importer.NoteRenamedCollision
}

// resolveCronIncludes expands each include_cron pattern against the root config
// dir and returns the resolved patterns plus the deduplicated, lexically sorted
// matches. Zero matches is not an error: an empty /etc/cron.d is a normal machine,
// and a glob that matches nothing today may match tomorrow.
func resolveCronIncludes(patterns []string, rootDir, rootPath string) (globs, matched []string, err error) {
	rootAbs, _ := filepath.Abs(rootPath)
	seen := map[string]struct{}{}
	for _, pat := range patterns {
		abs, err := resolvePath(rootDir, pat)
		if err != nil {
			return nil, nil, fmt.Errorf("daemon.include_cron %q: %w", pat, err)
		}
		globs = append(globs, abs)
		hits, err := filepath.Glob(abs)
		if err != nil {
			return nil, nil, fmt.Errorf("daemon.include_cron %q: %w", pat, err)
		}
		matched = appendGlobHits(matched, hits, rootAbs, seen)
	}
	sort.Strings(matched)
	return globs, matched, nil
}

// assertNoIncludeOverlap rejects a file claimed by both include and include_cron.
// The two readings of one file produce different task sets, and silently picking
// one would make the config mean something nobody wrote.
func assertNoIncludeOverlap(cronMatched, tomlMatched []string) error {
	if len(tomlMatched) == 0 {
		return nil
	}
	claimed := make(map[string]struct{}, len(tomlMatched))
	for _, f := range tomlMatched {
		claimed[f] = struct{}{}
	}
	for _, f := range cronMatched {
		if _, dup := claimed[f]; dup {
			return fmt.Errorf("%s is matched by both daemon.include and daemon.include_cron; "+
				"a file is either RunWisp TOML or a crontab, not both", f)
		}
	}
	return nil
}

// cronSourceWarnings renders every cron finding into config.Warnings, which both
// daemon boot and `runwisp validate` already print.
func cronSourceWarnings(cfg *Config) []string {
	var out []string
	skipped := 0
	for _, f := range cfg.CronFindings {
		if f.Skipped {
			skipped++
			out = append(out, "cron source: skipped "+f.String())
			continue
		}
		out = append(out, "cron source: "+f.String())
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf(
			"cron source: %d job(s) in your crontabs are not running \u2014 fix the lines above, "+
				"or convert them with `runwisp import cron`", skipped))
	}
	return append(out, crondStillRunningWarning(cfg.cronFiles, crondPidFiles)...)
}

// crondPidFiles are where the common cron implementations write their pid. Passed
// in rather than read directly so the check is testable without a real cron daemon.
var crondPidFiles = []string{"/run/crond.pid", "/run/cron.pid", "/var/run/crond.pid", "/var/run/cron.pid"}

// crondStillRunningWarning fires when RunWisp is reading a system crontab that a
// live crond is presumably also reading. Both schedulers firing the same jobs is
// the single most likely way an include_cron setup goes wrong, and it goes wrong
// silently: every job simply runs twice, which looks like nothing at all until a
// non-idempotent one does.
func crondStillRunningWarning(cronFiles, pidCandidates []string) []string {
	var system []string
	for _, f := range cronFiles {
		if importer.IsSystemCrontabPath(f) {
			system = append(system, f)
		}
	}
	if len(system) == 0 {
		return nil
	}
	for _, pid := range pidCandidates {
		if _, err := os.Stat(pid); err != nil {
			continue
		}
		return []string{fmt.Sprintf(
			"cron source: %s looks like a live cron daemon (%s exists) and RunWisp is also "+
				"running the jobs in %s — every one of them will fire twice. Stop and disable cron first.",
			pid, pid, strings.Join(system, ", "))}
	}
	return nil
}
