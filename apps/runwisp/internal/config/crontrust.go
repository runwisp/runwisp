// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// assertCronFileTrusted refuses to take task definitions from a file anyone but
// root or the daemon's own account could have written.
//
// This closes a hole include_cron opens by existing. Every other file that can
// define a task is one the operator wrote or one `runwisp import` wrote into the
// data dir; include_cron makes an arbitrary glob an author of shell that runs with
// the daemon's privilege. A group-writable /etc/cron.d/backup is then a privilege
// escalation for every member of that group — which is why crond applies
// structurally the same check to its own spool.
//
// Both the file and its directory are checked: a writable directory lets an
// attacker replace the file wholesale, which the file's own mode says nothing
// about.
func assertCronFileTrusted(path string) error {
	if err := assertPathTrusted(path, "cron source"); err != nil {
		return err
	}
	return assertPathTrusted(filepath.Dir(path), "the directory holding cron source")
}

// assertPathTrusted is the per-path half of assertCronFileTrusted.
func assertPathTrusted(path, what string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot stat %s: %w", path, err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s %s is writable by group or others (mode %04o); "+
			"anyone who can write it can run commands as this daemon — chmod it to at most 0755",
			what, path, info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// No uid available for this filesystem; the mode check above still stands.
		return nil
	}
	if owner := int(stat.Uid); owner != 0 && owner != os.Geteuid() {
		return fmt.Errorf("%s %s is owned by uid %d, which is neither root nor this daemon (uid %d); "+
			"RunWisp will not take commands from it", what, path, owner, os.Geteuid())
	}
	return nil
}
