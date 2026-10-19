// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/runwisp/runwisp/apps/runwisp/internal/update"
)

// sudoSecurePath is the PATH sudo gives the command on mainstream distros
// (Debian/Ubuntu/Fedora secure_path). A binary outside it (~/.local/bin is the
// common case) is "command not found" under sudo, so a hint has to name it by
// its full path.
var sudoSecurePath = []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin", "/snap/bin"}

// sudoRunwisp renders `sudo runwisp <args>` the way it actually works on this
// box: the bare name when sudo's PATH finds this binary, its absolute path when
// it does not.
func sudoRunwisp(args ...string) string {
	exe, err := update.Executable()
	if err != nil {
		exe = ""
	}
	return sudoRunwispFor(exe, args...)
}

// sudoRunwispFor is sudoRunwisp with the executable injected.
func sudoRunwispFor(exe string, args ...string) string {
	name := "runwisp"
	if exe != "" && !slices.Contains(sudoSecurePath, filepath.Dir(exe)) {
		name = exe
	}
	return strings.Join(append([]string{"sudo", name}, args...), " ")
}

// sudoForwardedEnv are the settings a re-run under sudo keeps. sudo resets the
// environment, so without them `RUNWISP_PORT=8080 runwisp takeover` would bake
// the default port into the unit. Secrets (RUNWISP_PASSWORD, the station token)
// are left out on purpose: they would travel on sudo's command line, which every
// user on the box can read in ps.
var sudoForwardedEnv = []string{
	"RUNWISP_CONFIG", "RUNWISP_DATA", "RUNWISP_HOST", "RUNWISP_PORT", "RUNWISP_TLS",
	"RUNWISP_LOG_LEVEL", "RUNWISP_LOG_FORMAT", "RUNWISP_LOG_OUTPUT",
}

// sudoArgv is the argv that re-runs this invocation as root: the same
// arguments, through sudo, by absolute path (sudo's PATH may not find it), with
// sudoForwardedEnv passed as VAR=value assignments sudo applies to the command.
func sudoArgv(sudo, exe string, args, environ []string) []string {
	argv := []string{sudo}
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); slices.Contains(sudoForwardedEnv, name) {
			argv = append(argv, kv)
		}
	}
	argv = append(argv, exe)
	return append(argv, args...)
}

// reexecAsRoot replaces this process with `sudo <this binary> <same args>`.
// It returns only if that cannot happen: no sudo on PATH (a root-only image,
// where the caller's own needs-root answer is the better message), or exec
// itself failed.
func reexecAsRoot(stderr io.Writer, why string) error {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return nil
	}
	exe, err := update.Executable()
	if err != nil {
		return nil
	}
	fmt.Fprintf(stderr, "%s Re-running with sudo…\n", why)
	err = syscall.Exec(sudo, sudoArgv(sudo, exe, os.Args[1:], os.Environ()), os.Environ())
	return fmt.Errorf("re-run with sudo: %w", err)
}
