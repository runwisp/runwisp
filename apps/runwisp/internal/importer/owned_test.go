// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
)

func TestOwnedFrom_SnapshotsKindAndCommand(t *testing.T) {
	owned := OwnedFrom([]model.Task{
		{Name: "backup", Kind: model.KindTask, Run: "/usr/bin/backup.sh"},
		{Name: "web", Kind: model.KindService, Run: "/usr/bin/web"},
	})

	if got := len(owned); got != 2 {
		t.Fatalf("expected 2 owned entries, got %d", got)
	}
	if e := owned["backup"]; e.Kind != model.KindTask || e.Run != "/usr/bin/backup.sh" {
		t.Errorf("backup entry: %+v", e)
	}
	if e := owned["web"]; e.Kind != model.KindService {
		t.Errorf("web entry: %+v", e)
	}
}

// TestOwnedFrom_SkipsStaged is the reason Owned exists at all: the staging file
// is rewritten wholesale by every import, so what it currently holds reserves
// nothing. Counting it would make a re-import rename every one of its own tasks
// to name-2.
func TestOwnedFrom_SkipsStaged(t *testing.T) {
	owned := OwnedFrom([]model.Task{
		{Name: "native", Kind: model.KindTask, Run: "echo native"},
		{Name: "imported", Kind: model.KindTask, Run: "echo imported", Source: model.SourceStaged},
	})

	if _, ok := owned["native"]; !ok {
		t.Error("a hand-authored task must be owned")
	}
	if _, ok := owned["imported"]; ok {
		t.Error("a staged task must not be owned")
	}
}

func TestSameEntry(t *testing.T) {
	tests := []struct {
		name     string
		existing OwnedEntry
		kind     model.TaskKind
		command  string
		want     bool
	}{
		{
			name:     "same kind and command",
			existing: OwnedEntry{Kind: model.KindTask, Run: "/bin/job"},
			kind:     model.KindTask, command: "/bin/job", want: true,
		},
		{
			name:     "whitespace differences don't matter",
			existing: OwnedEntry{Kind: model.KindTask, Run: "  /bin/job "},
			kind:     model.KindTask, command: "/bin/job", want: true,
		},
		{
			name:     "different command",
			existing: OwnedEntry{Kind: model.KindTask, Run: "/bin/other"},
			kind:     model.KindTask, command: "/bin/job", want: false,
		},
		{
			name:     "different kind",
			existing: OwnedEntry{Kind: model.KindService, Run: "/bin/job"},
			kind:     model.KindTask, command: "/bin/job", want: false,
		},
		{
			name:     "both commandless is not a match",
			existing: OwnedEntry{Kind: model.KindTask, Run: ""},
			kind:     model.KindTask, command: "", want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameEntry(tc.existing, tc.kind, tc.command); got != tc.want {
				t.Errorf("sameEntry = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNamer_ReservesOwnedNamesForFreshImports covers the plain dedup path: a
// name the live config owns is claimed up front, so an unrelated import of the
// same name lands on name-2 instead of colliding on the merged load.
func TestNamer_ReservesOwnedNamesForFreshImports(t *testing.T) {
	res := &Result{}
	n := newNamer(res, Owned{"job": {Kind: model.KindTask, Run: "/bin/original"}}, "")

	if got := n.unique("job"); got != "job-2" {
		t.Errorf("unique(job) = %q, want job-2", got)
	}
}
