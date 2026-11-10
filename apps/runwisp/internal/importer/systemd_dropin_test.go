// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUnits writes files (relative path → content) under a fresh temp dir and
// returns it.
func writeUnits(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func parseFiles(t *testing.T, opts SystemdOptions, paths ...string) *Result {
	t.Helper()
	res, err := ParseSystemdFiles(paths, opts)
	if err != nil {
		t.Fatalf("ParseSystemdFiles: %v", err)
	}
	return res
}

// TestSystemdDropInsOverrideTheUnit: `systemctl edit` writes an override under
// /etc even for a unit a package ships in /usr/lib. An `OnCalendar=` reset plus
// a new time, and an extra Environment=, must land in the import; reading only
// the base file imported the schedule the operator had changed.
func TestSystemdDropInsOverrideTheUnit(t *testing.T) {
	lib := writeUnits(t, map[string]string{
		"backup.timer":   "[Timer]\nOnCalendar=*-*-* 03:30:00\n",
		"backup.service": "[Service]\nType=oneshot\nExecStart=/bin/backup\nEnvironment=A=1\n",
		// Masked by the /etc drop-in of the same name.
		"backup.timer.d/override.conf": "[Timer]\nOnCalendar=\nOnCalendar=*-*-* 05:00:00\n",
	})
	etc := writeUnits(t, map[string]string{
		"backup.timer.d/override.conf": "[Timer]\nOnCalendar=\nOnCalendar=*-*-* 01:15:00\n",
		"backup.service.d/env.conf":    "[Service]\nEnvironment=EXTRA=1\n",
	})
	res := parseFiles(t, SystemdOptions{UnitDirs: []string{etc, lib}}, filepath.Join(lib, "backup.timer"))

	out := res.TOML()
	mustContain(t, out, `cron = "15 1 * * *"`)
	mustContain(t, out, `A = "1"`)
	mustContain(t, out, `EXTRA = "1"`)
	if n := findNote(t, res, NoteSystemdDropIns); !strings.Contains(n.Message, filepath.Join(etc, "backup.timer.d/override.conf")) ||
		strings.Contains(n.Message, filepath.Join(lib, "backup.timer.d")) {
		t.Fatalf("drop-ins note should name the /etc override only: %s", n.Message)
	}
}

// TestSystemdDropInResetsExecStart: a drop-in replaces a command with an empty
// ExecStart= first, which must not leave the old command as the run line.
func TestSystemdDropInResetsExecStart(t *testing.T) {
	dir := writeUnits(t, map[string]string{
		"app.service":            "[Service]\nExecStart=/bin/old\n",
		"app.service.d/new.conf": "[Service]\nExecStart=\nExecStart=/bin/new\n",
	})
	res := parseFiles(t, SystemdOptions{}, filepath.Join(dir, "app.service"))
	out := res.TOML()
	mustContain(t, out, `run = "/bin/new"`)
	mustNotContain(t, out, "/bin/old")
	if hasNoteKind(res, NoteSystemdMultiExec) {
		t.Fatal("a reset ExecStart is one command, not two")
	}
}

// TestSystemdRepeatedSectionsMerge: `systemctl cat` piped in prints the unit
// and its drop-ins one after another, so [Service] appears twice. The second
// one adds to the first rather than replacing it.
func TestSystemdRepeatedSectionsMerge(t *testing.T) {
	res := parseUnit(t, "# /etc/systemd/system/app.service\n[Service]\nExecStart=/bin/app\nUser=deploy\n\n"+
		"# /etc/systemd/system/app.service.d/env.conf\n[Service]\nEnvironment=K=v\n")
	out := res.TOML()
	mustContain(t, out, `run = "/bin/app"`)
	mustContain(t, out, `user = "deploy"`)
	mustContain(t, out, `K = "v"`)
}

// TestSystemdServiceOfATimerPair: importing backup.service with backup.timer
// beside it made a run-once-per-boot task, never on the timer's schedule, with
// nothing pointing at the timer.
func TestSystemdServiceOfATimerPair(t *testing.T) {
	dir := writeUnits(t, map[string]string{
		"backup.timer":   "[Timer]\nOnCalendar=daily\n",
		"backup.service": "[Service]\nType=oneshot\nExecStart=/bin/backup\n",
	})
	res := parseFiles(t, SystemdOptions{}, filepath.Join(dir, "backup.service"))
	n := findNote(t, res, NoteSystemdStartedByTimer)
	if !strings.Contains(n.Message, "runwisp import systemd backup.timer") || !n.Blocking() {
		t.Fatalf("want a blocking note pointing at the timer: %+v", n)
	}

	res = parseFiles(t, SystemdOptions{}, filepath.Join(dir, "backup.service"), filepath.Join(dir, "backup.timer"))
	if hasNoteKind(res, NoteSystemdStartedByTimer) {
		t.Fatal("with the timer passed too, the service is imported through it")
	}
}
