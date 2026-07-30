// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "path/filepath"

// posixShells are the interpreter basenames known to accept `-e` (errexit) as
// an argv flag with POSIX semantics. The executor consults this set before
// arming fail-fast, because `shell` is only validated as an absolute path — an
// operator may legitimately point it at a non-shell interpreter, and handing
// `-e` to one of those ranges from a loud startup error (python) to a silent
// no-op that reports success (`perl -e -c <script>` treats `-c` as the program
// and exits 0 having run nothing). The latter is exactly the invisible failure
// fail-fast exists to prevent, so the flag is gated rather than unconditional.
var posixShells = map[string]struct{}{
	"sh": {}, "bash": {}, "dash": {}, "ash": {}, "busybox": {},
	"ksh": {}, "ksh93": {}, "mksh": {}, "lksh": {}, "pdksh": {},
	"oksh": {}, "loksh": {}, "zsh": {}, "yash": {},
}

// ShellSupportsErrexit reports whether the interpreter at path takes `-e` to
// mean "stop at the first failing command". Matching is on the basename only,
// so absolute paths from any prefix resolve — /bin/bash, /usr/local/bin/dash,
// /opt/homebrew/bin/zsh, /nix/store/<hash>-bash-5.2/bin/bash. Matching is
// exact: no prefix or version-suffix guessing, because a wrong guess here
// reintroduces the silent-success case the set exists to avoid.
func ShellSupportsErrexit(path string) bool {
	if path == "" {
		// Empty means "fall back to /bin/sh" (see ShellExecution.Shell).
		return true
	}
	_, ok := posixShells[filepath.Base(path)]
	return ok
}
