// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"os/user"
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
	// kind is the note this finding came from, for the few findings whose relevance
	// depends on the rest of the config. Unexported: which importer note produced a
	// finding is this package's business, and a NoteKind on the public type would put
	// an internal/importer enum on the daemon's API surface. See dropAnsweredFindings.
	kind importer.NoteKind
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
	globs, matched, ignored, err := resolveCronIncludes(patterns, rootDir, rootPath)
	if err != nil {
		return cronMerge{}, err
	}
	m.globs = globs
	m.findings = append(m.findings, ignoredFindings(ignored)...)
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
	opts := cronOptionsFor(path, owned)
	if err := assertSpoolRunnable(path, opts.User); err != nil {
		return nil, fmt.Errorf("daemon.include_cron: %w", err)
	}
	if err := assertCronFileTrusted(path, opts.User); err != nil {
		return nil, fmt.Errorf("daemon.include_cron: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read cron source %s: %w", path, err)
	}
	return importer.ParseCrontab(strings.NewReader(string(data)), opts)
}

// assertSpoolRunnable refuses a spool crontab the daemon could read but not honour.
//
// Every job in /var/spool/cron/crontabs/alice runs as alice, and only root can
// become another account. A non-root daemon that loaded the file anyway would run
// her jobs as itself: the same commands, the wrong identity, writing to the wrong
// home, with nothing on screen saying so. Refusing the source names the reason
// while there is still somewhere to print it.
func assertSpoolRunnable(path, runAs string) error {
	if runAs == "" || os.Geteuid() == 0 {
		return nil
	}
	if current, err := user.Current(); err == nil && current.Username == runAs {
		// The daemon already *is* that account — no privilege drop needed. This is the
		// unprivileged single-user case: alice running her own daemon over her own
		// crontab, which is the one this feature should be easiest for.
		return nil
	}
	return fmt.Errorf("cron source %s holds %s's jobs, which only a root daemon can run as them; "+
		"this daemon runs as uid %d — run RunWisp as root to adopt other users' crontabs, "+
		"or point include_cron only at your own", path, runAs, os.Geteuid())
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
// A spool crontab (/var/spool/cron/crontabs/alice) is the third format: no user
// column, and the owner is the filename. Taking the identity from the name is safe
// *because* assertCronFileTrusted then requires the file to be owned by that
// account — an inference nothing has to agree with would be an escalation, and this
// one has to agree with the filesystem. crond pairs the same two checks on the same
// files. A file whose jobs run as someone else is refused unless the daemon can
// actually become them; see assertSpoolRunnable.
func cronOptionsFor(path string, owned importer.Owned) importer.CronOptions {
	spoolOwner, _ := importer.UserSpoolOwner(path)
	return importer.CronOptions{
		System:   importer.IsSystemCrontabPath(path),
		User:     spoolOwner,
		Existing: owned,
		// These are this machine's crontabs, so the sixth field of a system line can be
		// checked against the account database rather than only sniffed for shape — see
		// CronOptions.UserExists. Without it, `* * * * * echo hi` dropped into
		// /etc/cron.d schedules `"hi"` as user `echo`, which is a live task that cannot
		// run and a schedule the operator never wrote.
		UserExists: cronUserExists,
		// The collision tie-breaker is the file's own name, so a derived name is a
		// function of where the job came from rather than of how many files the glob
		// happened to match first. Dropping a lexically-earlier file into
		// /etc/cron.d must not renumber the tasks already running.
		NameSuffix: cronNameSuffix(path),
	}
}

// cronUserExists answers "is this an account on this box" for the user column of a
// system crontab line. A package var for the same reason crondPidFiles is one: the
// real answer comes from the machine, and a test needs to ask the question about a
// machine it can describe. Never assigned outside tests.
var cronUserExists = importer.SystemUserExists

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
	return append(fileFindings(res, path), jobFindings(res, path)...)
}

// fileFindings reports the notes about the crontab itself rather than about any one
// job — a MAILTO nobody is honouring, a SHELL that isn't an absolute path.
//
// These belong to no Item, so the per-job walk could never reach them. Until this
// existed, a crontab that had been mailing its output for years went quiet on the
// switch to include_cron and said nothing at all, which is the exact failure this
// type was introduced to prevent.
func fileFindings(res *importer.Result, path string) []CronFinding {
	var out []CronFinding
	for _, n := range res.Notes() {
		if !n.Blocking() {
			continue
		}
		out = append(out, CronFinding{
			File:   path,
			Source: filepath.Base(path),
			Reason: n.Message,
			kind:   n.Kind,
		})
	}
	return out
}

// jobFindings reports the per-job rows: every job that won't run, plus every job
// running under a name the crontab doesn't mention.
func jobFindings(res *importer.Result, path string) []CronFinding {
	var out []CronFinding
	for _, it := range res.Items() {
		live := it.LiveEligible()
		for _, n := range it.Notes {
			if !worthReporting(n, live) {
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

// worthReporting decides whether one job note earns a finding.
//
// A dropped job's unsafe-live note is the reason it dropped; anything else on that
// row is downstream of a job that isn't running, so a skipped job yields exactly
// one finding rather than a pile explaining consequences of the first.
func worthReporting(n importer.Note, live bool) bool {
	if !n.Blocking() && !isRenameNote(n) {
		return false
	}
	return live || n.UnsafeLive()
}

// dropAnsweredFindings removes findings whose need the rest of the config already
// meets. Called once the whole Config exists, because the answer lives outside the
// crontab that raised the question.
//
// Only MAILTO so far. It is a real gap on a box that hasn't wired up mail — crond
// delivered that output and RunWisp doesn't — but it is a gap the operator closes
// once, daemon-wide, and a warning that keeps firing after it has been dealt with
// is how an operator learns to stop reading warnings. The findings list is
// re-derived every load, so this stays correct if the notifier is later removed.
func dropAnsweredFindings(cfg *Config) {
	if !hasMailNotifier(cfg) {
		return
	}
	kept := make([]CronFinding, 0, len(cfg.CronFindings))
	for _, f := range cfg.CronFindings {
		if f.kind == importer.NoteMailto {
			continue
		}
		kept = append(kept, f)
	}
	cfg.CronFindings = kept
}

// hasMailNotifier reports whether the config can deliver mail at all. Both mail
// types count: `sendmail` is what the importer suggests because it needs no
// configuration on a box that already ran cron, but an operator who reached for
// `smtp` instead has answered the same question.
func hasMailNotifier(cfg *Config) bool {
	for _, n := range cfg.Notify.Notifiers {
		if n.Type == "sendmail" || n.Type == "smtp" {
			return true
		}
	}
	return false
}

// isRenameNote reports whether a note explains that a job is running under a name
// the crontab doesn't mention. Non-blocking — the job runs fine — but the operator
// cannot find the task without being told.
func isRenameNote(n importer.Note) bool {
	return n.Kind == importer.NoteRenamedOwned || n.Kind == importer.NoteRenamedCollision
}

// resolveCronIncludes expands each include_cron pattern against the root config
// dir and returns the resolved patterns, the deduplicated lexically sorted
// matches, and the glob hits deliberately passed over. Zero matches is not an
// error: an empty /etc/cron.d is a normal machine, and a glob that matches nothing
// today may match tomorrow.
func resolveCronIncludes(patterns []string, rootDir, rootPath string) (globs, matched []string, ignored []ignoredSource, err error) {
	rootAbs, _ := filepath.Abs(rootPath)
	seen := map[string]struct{}{}
	for _, pat := range patterns {
		abs, err := resolvePath(rootDir, pat)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("daemon.include_cron %q: %w", pat, err)
		}
		globs = append(globs, abs)
		hits, err := filepath.Glob(abs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("daemon.include_cron %q: %w", pat, err)
		}
		if hasGlobMeta(abs) {
			hits, ignored = partitionCrondEligible(hits, ignored)
		}
		matched = appendGlobHits(matched, hits, rootAbs, seen)
	}
	sort.Strings(matched)
	return globs, matched, ignored, nil
}

// ignoredSource is one glob hit that was not read, and why.
type ignoredSource struct {
	Path   string
	Reason string
}

// hasGlobMeta reports whether a resolved pattern can expand to more than the one
// path it names.
//
// It gates the crond-eligibility filter, because the filter must apply to a
// *glob's* hits and never to a path the operator typed out. `include_cron =
// ["/etc/cron.d/backup.cron"]` names one file deliberately; silently declining to
// read it because crond's own directory scan would have skipped that name is the
// daemon overruling an explicit instruction. A glob is the opposite case: the
// operator asked for "whatever is in this directory", and what crond considers to
// be in it is the only sane reading of that.
func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// partitionCrondEligible splits glob hits into the ones crond would read and the
// ones it would pass over, appending the latter to ignored.
//
// crond's own /etc/cron.d scan accepts only regular files whose names are made of
// letters, digits, hyphens and underscores — no dots. That rule is not cosmetic:
// it is precisely how a package upgrade's `backup.dpkg-old`, a hand-disabled
// `job.disabled`, and a `README` stay out of the schedule. Reading them because
// they matched `*` is the worst kind of divergence, because it runs jobs the
// operator believes are switched off, and a matched subdirectory would fail the
// whole load on a read error.
func partitionCrondEligible(hits []string, ignored []ignoredSource) ([]string, []ignoredSource) {
	kept := make([]string, 0, len(hits))
	for _, h := range hits {
		info, err := os.Lstat(h)
		switch {
		case err != nil:
			// Vanished between glob and stat, or unreadable. Leave it in: the read
			// that follows reports a real error against a real path, which beats
			// this function inventing a reason for a file it couldn't look at.
			kept = append(kept, h)
		case info.IsDir():
			ignored = append(ignored, ignoredSource{Path: h, Reason: "it is a directory"})
		case !info.Mode().IsRegular():
			ignored = append(ignored, ignoredSource{Path: h, Reason: "it is not a regular file"})
		case !isCrondEligibleName(filepath.Base(h)):
			ignored = append(ignored, ignoredSource{Path: h,
				Reason: "crond ignores this name (letters, digits, - and _ only), so it is not part of the schedule"})
		default:
			kept = append(kept, h)
		}
	}
	return kept, ignored
}

// isCrondEligibleName applies crond's run-parts naming rule to one basename.
func isCrondEligibleName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ignoredFindings reports the passed-over hits, one finding per file.
//
// Skipped is false: nothing stopped running because of this. crond wasn't running
// these either, so the machine is doing exactly what it did before — but an
// operator who dropped a file in and can't find its tasks needs to be told the
// name is why, and the alternative (say nothing) is how `job.disabled` becomes a
// twenty-minute mystery.
func ignoredFindings(ignored []ignoredSource) []CronFinding {
	out := make([]CronFinding, 0, len(ignored))
	for _, ig := range ignored {
		out = append(out, CronFinding{
			File:   ig.Path,
			Source: filepath.Base(ig.Path),
			Reason: "not read as a cron source: " + ig.Reason,
		})
	}
	return out
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
