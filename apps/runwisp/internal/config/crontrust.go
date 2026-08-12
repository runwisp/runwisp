// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// AssertFileTrusted refuses a path that someone other than root or this daemon
// could have written — the general form of the check assertCronFileTrusted
// applies to a cron source, with no run-as account to widen the acceptable
// owners by. Callers that bake a path into a privileged context without
// asking the operator first (a --system unit's config path, say) use this so
// that trust decision isn't silently skipped just because the path came from
// a shell default rather than an explicit flag.
//
// The file itself and every directory on the path to it are checked: a writable
// ancestor lets an attacker swap a directory component (or the file) after the
// check, so validating only the leaf is a TOCTOU hole.
func AssertFileTrusted(path, what string) error {
	if err := assertPathTrusted(path, what, -1); err != nil {
		return err
	}
	return assertAncestorsTrusted(path, -1)
}

// AssertPrivilegedConfigTrust re-runs the trust check on the root config and
// every included TOML file, but only when the daemon is running privileged
// (euid 0). A root daemon executes whatever the config says, so a config (or an
// included file) reachable through a user-writable directory or a repointable
// symlink is a root-RCE path. Running it on every Load — boot and reload alike —
// closes the gap where the install-time check on the baked path is not repeated
// when the file is actually read. Cron sources pulled via include_cron are
// already re-checked by assertCronFileTrusted inside every Load, so they are not
// repeated here.
//
// Non-privileged daemons are unaffected: a user-run daemon can only ever execute
// what that user could already run.
func AssertPrivilegedConfigTrust(cfg *Config, rootPath string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := AssertFileTrusted(rootPath, "the config file"); err != nil {
		return err
	}
	for _, inc := range cfg.includeFiles {
		if err := AssertFileTrusted(inc, "the included config file"); err != nil {
			return err
		}
	}
	return nil
}

// assertCronFileTrusted refuses to take task definitions from a file that someone
// other than the job's own identity could have written.
//
// This closes a hole include_cron opens by existing. Every other file that can
// define a task is one the operator wrote or one `runwisp import` wrote into the
// data dir; include_cron makes an arbitrary glob an author of shell that runs with
// the daemon's privilege. A group-writable /etc/cron.d/backup is then a privilege
// escalation for every member of that group — which is why crond applies
// structurally the same check to its own spool.
//
// runAs is the account the file's jobs will run as, empty for a file whose jobs run
// as the daemon. It widens the set of acceptable owners by exactly one: a spool
// crontab belonging to alice is trustworthy *for running alice's jobs*, because
// anything she could put in it she could already run herself. That is not a hole —
// it is the corroboration that makes deriving her name from the filename safe in
// the first place, and it is the same pairing crond makes.
//
// Both the file and its directory are checked: a writable directory lets an
// attacker replace the file wholesale, which the file's own mode says nothing
// about.
func assertCronFileTrusted(path, runAs string) error {
	extra, err := runAsUID(runAs)
	if err != nil {
		return err
	}
	if err := assertPathTrusted(path, "cron source", extra); err != nil {
		return err
	}
	return assertAncestorsTrusted(path, extra)
}

// runAsUID resolves the run-as account to a uid that may also own the file. It
// returns -1 for "no additional owner", which no real uid equals.
//
// An unresolvable account is refused here rather than left to the run: a spool file
// for a user who doesn't exist is a file whose ownership nothing can corroborate,
// so the one check standing between it and daemon-privileged execution can't be
// made. Failing the load names the file; failing at run time would strand a task
// nobody can explain.
func runAsUID(runAs string) (int, error) {
	if runAs == "" {
		return -1, nil
	}
	u, err := user.Lookup(runAs)
	if err != nil {
		return -1, fmt.Errorf("cron source for user %q: cannot resolve that account on this machine "+
			"(%w) — RunWisp cannot confirm the file belongs to whoever its jobs would run as", runAs, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return -1, fmt.Errorf("cron source for user %q: account has a non-numeric uid %q", runAs, u.Uid)
	}
	return uid, nil
}

// assertPathTrusted is the per-file half of assertCronFileTrusted. extraOwner is
// an additional acceptable uid, or -1. It uses Lstat (not Stat) and rejects a
// symlink: a symlinked config/cron source can be repointed at attacker content
// after the check, and following it would hide the real target's ownership.
func assertPathTrusted(path, what string, extraOwner int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("cannot stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s %s is a symlink; RunWisp will not take commands through a symlink "+
			"(its target can be repointed after the check) — point it at the real file", what, path)
	}
	return assertInfoTrusted(info, path, what, extraOwner)
}

// assertInfoTrusted applies the writability and ownership checks to an
// already-stat'd entry, shared by the file check and the ancestor walk.
func assertInfoTrusted(info os.FileInfo, path, what string, extraOwner int) error {
	if err := assertNotGroupOrWorldWritable(info, path, what); err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// No uid available for this filesystem; the mode check above still stands.
		return nil
	}
	owner := int(stat.Uid)
	if owner == 0 || owner == os.Geteuid() || (extraOwner >= 0 && owner == extraOwner) {
		return nil
	}
	return fmt.Errorf("%s %s is owned by uid %d, which is neither root, this daemon (uid %d), "+
		"nor the account its jobs would run as; RunWisp will not take commands from it",
		what, path, owner, os.Geteuid())
}

// assertAncestorsTrusted walks every directory component from the file's parent
// up to the filesystem root, refusing if any is writable by an untrusted party
// or owned by one — a writable ancestor lets an attacker rename or replace a
// component and redirect the path to their own file. Directory components are
// Lstat'd but symlinks among them are *not* rejected: system layouts legitimately
// symlink directories (macOS /etc -> /private/etc), and the ownership/writability
// check on the lstat'd component is what actually gates the swap.
func assertAncestorsTrusted(path string, extraOwner int) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	dir := filepath.Dir(abs)
	for {
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("cannot stat %s: %w", dir, err)
		}
		if err := assertInfoTrusted(info, dir, "a directory on the path to a trusted file", extraOwner); err != nil {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// assertNotGroupOrWorldWritable rejects a path others can write, with one carve-out
// crond itself relies on.
//
// A cron spool directory is group-writable *and sticky* by design — 1730
// root:crontab is what lets the setgid crontab(1) binary drop a user's file in
// there. Sticky is precisely what makes that safe: a group member can add their own
// entry but cannot rename or delete anyone else's, so it buys no ability to replace
// another user's crontab. Refusing it outright is why per-user crontabs were
// unreadable rather than merely unmapped, and it refused the exact configuration
// every Debian box ships.
//
// A sticky *directory* is the carve-out — for group- and world-writable alike:
// sticky means only a file's owner (or root) can rename or delete it, so a
// group/world member can add their own entry but cannot replace someone else's.
// That is exactly what makes /tmp (1777), /var/spool/cron (1730), and the like
// safe as directory components. A writable regular *file* gets no such pass.
func assertNotGroupOrWorldWritable(info os.FileInfo, path, what string) error {
	perm := info.Mode().Perm()
	if perm&0o022 == 0 {
		return nil
	}
	if info.IsDir() && info.Mode()&os.ModeSticky != 0 {
		return nil
	}
	if perm&0o002 != 0 {
		return fmt.Errorf("%s %s is world-writable (mode %04o); anyone on this machine "+
			"could run commands as this daemon — chmod it to at most 0755", what, path, perm)
	}
	return fmt.Errorf("%s %s is writable by its group (mode %04o); anyone in that group "+
		"can run commands as this daemon — chmod it to at most 0755", what, path, perm)
}
