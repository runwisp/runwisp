// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import "testing"

// An `@reboot user cmd` line gets the same user-column warning as its five-field
// sibling (TestCronDetectAmbiguousUserColumnWarns) instead of importing "root"
// as the command.
func TestCronDetectAmbiguousUserColumnOnRebootLine(t *testing.T) {
	res := parseCron(t, "@reboot root /usr/bin/warmup\n", CronOptions{Detect: true})
	out := res.TOML()

	mustContain(t, out, "TODO")
	mustContain(t, out, "--system")
	if !hasBlockingNote(res, "system crontab") {
		t.Fatalf("expected a system-crontab ambiguity note for an @reboot line with a "+
			"user column, got none: %+v", allNotes(res))
	}
}
