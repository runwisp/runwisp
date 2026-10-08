// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// Owned describes the entries a live config already defines outside the
// machine-owned staging file, keyed by name. It powers identity-aware dedup on a
// re-import: without it, importing the same crontab twice after a `promote`
// would emit a task the merged config already defines and fail the whole load.
//
// It is deliberately a snapshot of facts, not a config handle — building one
// needs a loaded config, but using one doesn't, which keeps this package free of
// I/O as its doc comment promises.
type Owned map[string]OwnedEntry

// OwnedEntry is what the live config knows about one name.
type OwnedEntry struct {
	Kind model.TaskKind
	// Run is the entry's command. Empty for entries with no comparable one-shot
	// command (a compose-backed task), which therefore can never match an
	// imported command and always force a rename rather than a skip.
	Run string
	// User is who the entry runs as, empty for the daemon's own account. The same
	// command in Alice's and Bob's crontabs is two jobs, not one.
	User string
	// Schedule is set only for an entry read live from a crontab: its cron
	// expression, or "@reboot". That entry is one exact crontab line, so the same
	// command on another schedule is another crond job. An operator-authored entry
	// leaves it empty and matches on any schedule, because a job promoted out of a
	// crontab and retimed since is still that job.
	Schedule string
}

// OwnedFrom snapshots the tasks and services a config defines, skipping staged
// ones: the staging file is about to be rewritten wholesale, so what it
// currently holds reserves nothing.
//
// Cron-sourced tasks are *kept*. An import doesn't rewrite a crontab, so the
// name is genuinely taken — and when the incoming job is the same job (which it
// usually is, since include_cron is reading the very file being imported)
// sameEntry turns it into a skip, which is the right answer rather than a
// duplicate.
func OwnedFrom(tasks []model.Task) Owned {
	owned := make(Owned, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		if t.Source == model.SourceStaged {
			continue
		}
		e := OwnedEntry{Kind: t.Kind, Run: t.Run, User: t.RunUser}
		if t.Source == model.SourceCron {
			e.Schedule = cronSchedule(t.Cron, t.RunOnStart)
		}
		owned[t.Name] = e
	}
	return owned
}

// cronSchedule is the schedule a crontab line imports to: its cron expression,
// or "@reboot" for a run-on-start job. The same string Item.Schedule carries.
func cronSchedule(cron string, runOnStart bool) string {
	if runOnStart && cron == "" {
		return "@reboot"
	}
	return cron
}

// namer assigns each imported entry its final RunWisp name: unique within this
// import, and reconciled against what the live config already owns.
type namer struct {
	res   *Result
	dd    *deduper
	owned Owned
	// suffix is the stable collision tie-breaker for the file being parsed — see
	// deduper.uniqueIn. Empty for a single-source import, where positional
	// suffixes are stable because there is only one order.
	suffix string
}

// newNamer seeds the deduper with every owned name, so a clash renames to
// name-2 instead of emitting a duplicate that would fail the merged load.
func newNamer(res *Result, owned Owned, suffix string) *namer {
	n := &namer{res: res, dd: newDeduper(), owned: owned, suffix: suffix}
	for name := range owned {
		n.dd.reserve(name)
	}
	return n
}

// unique returns a name unique within this import, ignoring the live config.
func (n *namer) unique(base string) string { return n.dd.uniqueIn(base, n.suffix) }

// resolve opens this job's report row and picks its RunWisp name. source is what
// the source file called the job; base is that name sanitized to RunWisp's
// rules; id is the incoming entry as sameEntry compares it. It returns
// skip=true when the live config already owns exactly this entry (see
// sameEntry), i.e. a job that was already promoted and is still sitting in the
// source file, and otherwise a unique name, renamed when something else already
// claims the base.
//
// The row is opened here, before the skip return, because this is the one place
// both parsers pass through on their way to a name and the one place that
// decides a job won't be imported. A row opened later would be a row a skipped
// job never gets, which is the silent drop this design exists to prevent.
//
// line is the 1-based source line, or 0 for a source that isn't line-oriented.
func (n *namer) resolve(source, base string, id OwnedEntry, line int) (ref itemRef, name string, skip bool) {
	ref = n.res.addItemAt(source, line)
	existing, reserved := n.owned[base]
	if reserved && sameEntry(existing, id) {
		ref.note(NoteAlreadyDefined, "already defined in runwisp.toml with the same command")
		return ref, "", true
	}
	name = n.unique(base)
	switch {
	case name == base:
	case reserved:
		ref.note(NoteRenamedOwned,
			"runwisp.toml already defines \""+base+"\" as a different job — imported this one as \""+name+"\".")
	default:
		ref.note(NoteRenamedCollision,
			"another job in this import already took the name \""+base+"\" — imported this one as \""+name+"\".")
	}
	return ref, name, false
}

// sameEntry reports whether an imported entry is the same job the live config
// already owns. `promote` preserves the command and user verbatim, so trimmed
// equality of both plus a matching kind identifies "this one, again", the
// signal for skipping a re-import rather than renaming it. The schedule counts
// only when the owned entry carries one (see OwnedEntry.Schedule). An empty
// command never matches: two entries that merely both lack a command are not
// the same job.
//
// Anything short of a match renames, which keeps both jobs running. A false
// match drops a job without a finding, so every doubt resolves to a rename.
func sameEntry(existing, incoming OwnedEntry) bool {
	command := strings.TrimSpace(incoming.Run)
	return command != "" &&
		existing.Kind == incoming.Kind &&
		strings.TrimSpace(existing.Run) == command &&
		strings.TrimSpace(existing.User) == strings.TrimSpace(incoming.User) &&
		(existing.Schedule == "" || existing.Schedule == incoming.Schedule)
}
