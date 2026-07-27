// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	// The two '%' kinds are split because their severity differs: collapsing
	// `\%` is a faithful translation, while input crond piped on stdin is
	// something RunWisp cannot express at all.
	NotePercentTranslated
	NotePercentStdin
	NoteUserColumnSuspect

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

// info returns the kind's stable slug and its two severity answers.
//
// blocking is whether the job it belongs to needs a human before anyone should
// rely on it: the TOML carries a `# TODO`, the command is wrong, or the job
// didn't import at all. Everything else is a difference worth knowing about.
//
// unsafeLive is whether the job may be *run* as-imported. The two are separate
// axes and collapsing them would be wrong in both directions. A MAILTO is
// blocking — nobody gets the mail until a notifier exists — but the job itself
// runs exactly as crond ran it, so refusing to schedule it would be a worse
// answer than scheduling it. A `%`-stdin command is the reverse shape: the TODO
// is not advice, it is the input the command needed and didn't get, so running
// it means running something the crontab never asked for. Only the second kind
// disqualifies a job from `include_cron`.
//
// An unlisted kind returns an empty slug on purpose: that is what makes
// "adding a kind without deciding its severity" a red test rather than a
// silently harmless note.
func (k NoteKind) info() (slug string, blocking, unsafeLive bool) {
	switch k {
	case NoteShellNotAbsolute:
		// The crontab's bash isn't invoked, so a bash-ism fails — loudly, with the
		// error in the run's captured output. A visible failure is still a run.
		return "shell-not-absolute", true, false
	case NoteMailto:
		// Nobody gets the mail until a notifier exists, but the job runs exactly as
		// crond ran it. Refusing to schedule it would be the worse answer.
		return "mailto", true, false
	case NoteSystemAmbiguous:
		// The format was guessed, so the command may still have a username glued to
		// its front — running it would run the wrong thing as the wrong user.
		return "system-crontab-ambiguous", true, true
	case NoteLineUnparseable:
		return "line-unparseable", true, true
	case NoteCronUnparseable:
		return "cron-unparseable", true, true
	case NoteTimezoneInvalid:
		// The zone decides when it fires, so the wrong zone is the wrong schedule.
		return "timezone-invalid", true, true
	case NotePercentTranslated:
		return "percent-translated", false, false
	case NotePercentStdin:
		// The TODO is not advice, it is the input the command needed and did not
		// get. Running it runs something the crontab never asked for.
		return "percent-stdin", true, true
	case NoteUserColumnSuspect:
		// Both halves of the split are wrong: neither the identity nor the command
		// is one the operator wrote.
		return "user-column-suspect", true, true
	case NoteAlreadyDefined:
		return "already-defined", false, false
	case NoteRenamedOwned:
		return "renamed-owned", false, false
	case NoteRenamedCollision:
		return "renamed-collision", false, false
	case NoteIncludeUnresolved:
		return "include-unresolved", true, false
	case NoteIncludeNoMatch:
		return "include-no-match", true, false
	case NoteIncludeUnreadable:
		return "include-unreadable", true, false
	case NoteGroup:
		return "group", false, false
	case NoteSectionUnsupported:
		return "section-unsupported", true, true
	case NoteSectionDaemon:
		return "section-daemon", false, false
	case NoteSectionUnrecognized:
		return "section-unrecognized", false, false
	case NoteAutorestartUnexpected:
		return "autorestart-unexpected", false, false
	case NoteNoCommand:
		return "no-command", true, true
	case NoteCommandExpansion:
		// A %(...)s that didn't expand leaves a command that isn't the real one.
		return "command-expansion", true, true
	case NoteRunOnce:
		return "run-once", false, false
	case NoteLogsDropped:
		return "logs-dropped", false, false
	case NoteSignalScope:
		return "signal-scope", false, false
	case NoteRelativeDirectory:
		return "relative-directory", false, false
	case NoteServiceKeyDropped:
		return "service-key-dropped", false, false
	case NoteInstances:
		return "instances", false, false
	case NoteKeysUnsupported:
		return "keys-unsupported", false, false
	case NoteKeyUnreadable:
		return "key-unreadable", false, false
	default:
		return "", false, false
	}
}

// Slug is the kind's stable identifier, for tests and structured dumps.
func (k NoteKind) Slug() string { slug, _, _ := k.info(); return slug }

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
func (n Note) Blocking() bool { _, blocking, _ := n.Kind.info(); return blocking }

// UnsafeLive reports whether this note means the job must not be run as
// imported. See NoteKind.info for why this is a different question from
// Blocking.
func (n Note) UnsafeLive() bool { _, _, unsafe := n.Kind.info(); return unsafe }

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
	// Notes are the differences and blockers belonging to this job.
	Notes []Note
	// Line is the 1-based line in the source file this job came from, or 0 when
	// the source isn't line-oriented (a supervisord section) or the note is about
	// the file. It exists so a skipped job can be named as `file:line` — an
	// operator staring at a crontab needs the line, not a derived task name they
	// have never seen.
	Line int
}

// LiveEligible reports whether this job may be scheduled as-imported, which is
// the question `[daemon] include_cron` asks of every row.
//
// Two things disqualify a job: emitting no TOML at all (nothing to schedule),
// and carrying a note whose kind says the imported form isn't what the source
// would have run. See NoteKind.info for why that is a different question from
// Status() == StatusBlocked — a MAILTO blocks and is perfectly safe to run.
func (i Item) LiveEligible() bool {
	if i.Name == "" {
		return false
	}
	for _, n := range i.Notes {
		if n.UnsafeLive() {
			return false
		}
	}
	return true
}

// Status is the mark this row earned, computed from the row itself every time it
// is asked for.
//
// It is a method rather than a field on purpose. As a field its zero value was
// StatusClean, so any Item that reached a renderer without being stamped printed
// a green ✓ — the safest-looking mark as the default, which is the failure mode
// this whole report exists to remove. A derived status cannot be stale and cannot
// disagree with the notes printed under it.
func (i Item) Status() ItemStatus { return deriveStatus(i) }

// SkipReason is the one-clause "why" for a skipped row, or "" for any other
// status. Total by construction, so a formatter never has to index Notes.
func (i Item) SkipReason() string {
	if i.Status() != StatusSkipped {
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

// Items returns the report rows in source order. The slice is a copy, so a
// caller filtering or sorting it can't reach back into the parse.
func (r *Result) Items() []Item {
	items := make([]Item, len(r.items))
	copy(items, r.items)
	return items
}

// Notes returns the file-level notes: the ones about the source file's
// structure, or about a section that isn't a job. Per-job notes live on their
// Item.
func (r *Result) Notes() []Note { return r.notes }

// Tally counts the report two independent ways. The four statuses partition the
// rows, so Clean+Changed+Blocked+Skipped is always the row count — that is what
// makes "every job gets exactly one row" assertable as arithmetic. Tasks and
// Services count only the rows that emitted something, which is a different
// question ("what landed in the file") and deliberately does not add up to the
// same number.
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
		switch it.Status() {
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
	return r.addItemAt(source, 0)
}

// addItemAt opens a row for a job that came from a known line of the source.
func (r *Result) addItemAt(source string, line int) itemRef {
	r.items = append(r.items, Item{Source: source, Line: line})
	return itemRef{res: r, i: len(r.items) - 1}
}

// emit records what this job became *and* the TOML it produced, in one call.
//
// Blocks reach the generated config only through here. That is the structural
// half of this package's promise: a table cannot appear in the emitted file
// without a row in the report, because there is no other way to add one. A row
// that never gets an emit is a job that produced no configuration.
func (ir itemRef) emit(name string, kind model.TaskKind, schedule, run string, blocks ...block) {
	it := &ir.res.items[ir.i]
	it.Name, it.Kind, it.Schedule, it.Run = name, kind, schedule, run
	// Stamping the owning row onto each block is what lets TOMLFor render a
	// subset without inferring ownership from table names. Inference would have
	// to re-derive the header→row mapping that emit already knows, and would get
	// a `[tasks.x.env]` child wrong.
	for _, b := range blocks {
		b.item = ir.i
		ir.res.blocks = append(ir.res.blocks, b)
	}
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
