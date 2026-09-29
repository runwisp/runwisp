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
	job := OwnedEntry{Kind: model.KindTask, Run: "/bin/job"}
	tests := []struct {
		name     string
		existing OwnedEntry
		incoming OwnedEntry
		want     bool
	}{
		{name: "same kind and command", existing: job, incoming: job, want: true},
		{
			name:     "whitespace differences don't matter",
			existing: OwnedEntry{Kind: model.KindTask, Run: "  /bin/job "},
			incoming: job, want: true,
		},
		{
			name:     "different command",
			existing: OwnedEntry{Kind: model.KindTask, Run: "/bin/other"},
			incoming: job, want: false,
		},
		{
			name:     "different kind",
			existing: OwnedEntry{Kind: model.KindService, Run: "/bin/job"},
			incoming: job, want: false,
		},
		{
			name:     "both commandless is not a match",
			existing: OwnedEntry{Kind: model.KindTask},
			incoming: OwnedEntry{Kind: model.KindTask}, want: false,
		},
		{
			name:     "different user",
			existing: OwnedEntry{Kind: model.KindTask, Run: "/bin/job", User: "alice"},
			incoming: OwnedEntry{Kind: model.KindTask, Run: "/bin/job", User: "bob"}, want: false,
		},
		{
			name:     "operator-authored entry matches any schedule",
			existing: job,
			incoming: OwnedEntry{Kind: model.KindTask, Run: "/bin/job", Schedule: "0 3 * * *"}, want: true,
		},
		{
			name:     "cron-sourced entry on another schedule",
			existing: OwnedEntry{Kind: model.KindTask, Run: "/bin/job", Schedule: "0 3 * * *"},
			incoming: OwnedEntry{Kind: model.KindTask, Run: "/bin/job", Schedule: "30 4 * * *"}, want: false,
		},
		{
			name:     "cron-sourced entry on its own schedule",
			existing: OwnedEntry{Kind: model.KindTask, Run: "/bin/job", Schedule: "@reboot"},
			incoming: OwnedEntry{Kind: model.KindTask, Run: "/bin/job", Schedule: "@reboot"}, want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameEntry(tc.existing, tc.incoming); got != tc.want {
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
