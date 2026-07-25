// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package importer

import "github.com/runwisp/runwisp/internal/model"

// This file owns the *report*: one row per job the source described, whatever
// became of it. It is deliberately separate from the emitted TOML (blocks), and
// that separation is the point — when "row in the report" and "table in the
// TOML" were the same object, a job that produced no TOML produced no row, so
// the jobs most in need of the operator's attention were the ones that
// disappeared. Directive #1 says nothing silently fails; the report is where an
// import keeps that promise.

// NoteKind identifies what a note is about. Severity is a property of the kind
// (see info), not of the call site, so "this program has no command" cannot be
// filed as a passing remark. The kinds also give tests a stable identity to
// assert on — note prose stays as free to reword as it ever was.
type NoteKind int

const (
	// Crontab-level.
	NoteShellNotAbsolute NoteKind = iota
	NoteMailto
	NoteSystemAmbiguous
	NoteLineUnparseable
	NoteCronUnparseable
	NoteTimezoneInvalid
	NotePercentInCommand

	// Naming and identity, shared by both parsers.
	NoteAlreadyDefined
	NoteRenamedOwned
	NoteRenamedCollision

	// supervisord-level.
	NoteIncludeUnresolved
	NoteIncludeNoMatch
	NoteIncludeUnreadable
	NoteGroup
	NoteSectionUnsupported
	NoteSectionDaemon
	NoteSectionUnrecognized
	NoteAutorestartUnexpected
	NoteNoCommand
	NoteCommandExpansion
	NoteRunOnce
	NoteLogsDropped
	NoteSignalScope
	NoteRelativeDirectory
	NoteServiceKeyDropped
	NoteInstances
	NoteKeysUnsupported
	NoteKeyUnreadable

	// noteKindCount bounds the enum so TestNoteKindsAreTotal can walk it.
	noteKindCount
)

// info returns the kind's stable slug and whether it blocks — i.e. whether the
// job it belongs to needs a human before anyone should rely on it. A blocking
// note is one an operator has to act on: the TOML carries a `# TODO`, the
// command is wrong, or the job didn't import at all. Everything else is a
// difference worth knowing about.
//
// An unlisted kind returns an empty slug on purpose: that is what makes
// "adding a kind without deciding its severity" a red test rather than a
// silently non-blocking note.
func (k NoteKind) info() (slug string, blocking bool) {
	switch k {
	case NoteShellNotAbsolute:
		return "shell-not-absolute", true
	case NoteMailto:
		return "mailto", true
	case NoteSystemAmbiguous:
		return "system-crontab-ambiguous", true
	case NoteLineUnparseable:
		return "line-unparseable", true
	case NoteCronUnparseable:
		return "cron-unparseable", true
	case NoteTimezoneInvalid:
		return "timezone-invalid", true
	case NotePercentInCommand:
		return "percent-in-command", false
	case NoteAlreadyDefined:
		return "already-defined", false
	case NoteRenamedOwned:
		return "renamed-owned", false
	case NoteRenamedCollision:
		return "renamed-collision", false
	case NoteIncludeUnresolved:
		return "include-unresolved", true
	case NoteIncludeNoMatch:
		return "include-no-match", true
	case NoteIncludeUnreadable:
		return "include-unreadable", true
	case NoteGroup:
		return "group", false
	case NoteSectionUnsupported:
		return "section-unsupported", true
	case NoteSectionDaemon:
		return "section-daemon", false
	case NoteSectionUnrecognized:
		return "section-unrecognized", false
	case NoteAutorestartUnexpected:
		return "autorestart-unexpected", false
	case NoteNoCommand:
		return "no-command", true
	case NoteCommandExpansion:
		return "command-expansion", true
	case NoteRunOnce:
		return "run-once", false
	case NoteLogsDropped:
		return "logs-dropped", false
	case NoteSignalScope:
		return "signal-scope", false
	case NoteRelativeDirectory:
		return "relative-directory", false
	case NoteServiceKeyDropped:
		return "service-key-dropped", false
	case NoteInstances:
		return "instances", false
	case NoteKeysUnsupported:
		return "keys-unsupported", false
	case NoteKeyUnreadable:
		return "key-unreadable", false
	default:
		return "", false
	}
}

// Slug is the kind's stable identifier, for tests and structured dumps.
func (k NoteKind) Slug() string { slug, _ := k.info(); return slug }

// String makes a NoteKind readable in test failures.
func (k NoteKind) String() string {
	if slug := k.Slug(); slug != "" {
		return slug
	}
	return "note-kind-without-severity"
}

// Note is a single human-readable observation about the conversion. Scope is
// structural rather than a field: a note about one job's own mapping lives on
// that job's Item, and a note about the file's structure lives on the Result.
// That makes a note naming something the report never lists unrepresentable.
type Note struct {
	Kind    NoteKind
	Message string
}

// Blocking reports whether this note needs a human before the job is
// trustworthy. See NoteKind.info.
func (n Note) Blocking() bool { _, blocking := n.Kind.info(); return blocking }

// ItemStatus is the mark one source job earned. It is always derived from what
// the parser recorded — never assigned — so a row's mark and the notes under it
// cannot disagree.
type ItemStatus int

const (
	StatusClean   ItemStatus = iota // mapped with nothing lost
	StatusChanged                   // imported, but not identically
	StatusBlocked                   // carries an unresolved # TODO, or didn't import
	StatusSkipped                   // recognized and deliberately not imported
)

// String makes an ItemStatus readable in test failures and structured dumps.
func (s ItemStatus) String() string {
	switch s {
	case StatusClean:
		return "clean"
	case StatusChanged:
		return "changed"
	case StatusBlocked:
		return "blocked"
	case StatusSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// Item is one row of the report: one job in the source, exactly one row, and a
// mark that says what happened to it. Every job the parser saw gets an Item —
// including the ones it declined to import and the lines it couldn't read —
// because a job that vanishes from the report is a job that was silently
// dropped.
type Item struct {
	// Source is what the source file called this job: the [program:NAME] name,
	// the name derived from a cron command, or the raw line when it wouldn't
	// parse at all.
	Source string
	// Name is the RunWisp name after sanitizing and dedup. Empty when nothing
	// was emitted for this job.
	Name string
	// Kind is empty when nothing was emitted.
	Kind model.TaskKind
	// Schedule is a cron expression, "@reboot", or "service". Empty when nothing
	// was emitted.
	Schedule string
	// Run is the command that will run — verbatim, unbounded, and possibly
	// multi-line. Wrapping and truncation are the CLI's business, not this
	// package's.
	Run string
	// Status is derived by Items; a zero value here means nothing yet.
	Status ItemStatus
	// Notes are the differences and blockers belonging to this job.
	Notes []Note
}

// SkipReason is the one-clause "why" for a skipped row, or "" for any other
// status. Total by construction, so a formatter never has to index Notes.
func (i Item) SkipReason() string {
	if i.Status != StatusSkipped {
		return ""
	}
	if len(i.Notes) > 0 {
		return i.Notes[0].Message
	}
	return "not imported"
}

// deriveStatus is the single place a row's mark is decided.
//
// Blocking deliberately outranks skipped. An [eventlistener] emits nothing, so
// it looks like a skip, but the operator's job is "go reimplement this" — not
// "nothing to do". Only a job the live config already owns is genuinely a
// nothing-to-do, and it carries no blocking note.
func deriveStatus(it Item) ItemStatus {
	for _, n := range it.Notes {
		if n.Blocking() {
			return StatusBlocked
		}
	}
	if it.Name == "" {
		return StatusSkipped
	}
	if len(it.Notes) > 0 {
		return StatusChanged
	}
	return StatusClean
}

// Items returns the report rows in source order, each with its status derived.
func (r *Result) Items() []Item {
	items := make([]Item, len(r.items))
	copy(items, r.items)
	for i := range items {
		items[i].Status = deriveStatus(items[i])
	}
	return items
}

// Notes returns the file-level notes: the ones about the source file's
// structure, or about a section that isn't a job. Per-job notes live on their
// Item.
func (r *Result) Notes() []Note { return r.notes }

// Tally counts the report. Tasks and Services count only rows that emitted
// something, so Tasks+Services+Skipped-with-no-emit accounts for every row and
// the arithmetic can be asserted.
type Tally struct {
	Tasks, Services                  int
	Clean, Changed, Blocked, Skipped int
}

// Total is the number of rows, i.e. jobs the source described.
func (t Tally) Total() int { return t.Clean + t.Changed + t.Blocked + t.Skipped }

// Tally summarizes the report.
func (r *Result) Tally() Tally {
	var t Tally
	for _, it := range r.Items() {
		switch it.Status {
		case StatusClean:
			t.Clean++
		case StatusChanged:
			t.Changed++
		case StatusBlocked:
			t.Blocked++
		case StatusSkipped:
			t.Skipped++
		}
		switch it.Kind {
		case model.KindService:
			t.Services++
		case model.KindTask:
			t.Tasks++
		}
	}
	return t
}

// --- recording seam ---
//
// The parsers only ever record through these five methods, which is what keeps
// "every job gets a row" a property of the code rather than a habit.

// itemRef points at a row already opened on a Result. A row is opened before
// anything about the job can go wrong — that ordering is the whole reason a
// skipped or unreadable job still appears in the report.
type itemRef struct {
	res *Result
	i   int
}

// addItem opens a report row for a job the source described.
func (r *Result) addItem(source string) itemRef {
	r.items = append(r.items, Item{Source: source})
	return itemRef{res: r, i: len(r.items) - 1}
}

// emit records what this job actually became. A row that never gets an emit is
// a job that produced no configuration.
func (ir itemRef) emit(name string, kind model.TaskKind, schedule, run string) {
	it := &ir.res.items[ir.i]
	it.Name, it.Kind, it.Schedule, it.Run = name, kind, schedule, run
}

// note records a difference or a blocker belonging to this job.
func (ir itemRef) note(kind NoteKind, msg string) {
	it := &ir.res.items[ir.i]
	it.Notes = append(it.Notes, Note{Kind: kind, Message: msg})
}

// noteOnce records a note unless one of the same kind is already on this job.
// Several supervisord keys collapse onto a single explanation, and repeating it
// per key would drown the row it belongs to.
func (ir itemRef) noteOnce(kind NoteKind, msg string) {
	it := &ir.res.items[ir.i]
	for _, n := range it.Notes {
		if n.Kind == kind {
			return
		}
	}
	it.Notes = append(it.Notes, Note{Kind: kind, Message: msg})
}

// fileNote records a note about the source file itself rather than about one
// job: its structure, or a section that isn't a job.
func (r *Result) fileNote(kind NoteKind, msg string) {
	r.notes = append(r.notes, Note{Kind: kind, Message: msg})
}
