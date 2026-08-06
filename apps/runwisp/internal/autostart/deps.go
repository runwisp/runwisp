// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"io"
	"os"
	"os/user"

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/fingerprint"
)

// Deps consolidates the injected seams. Constructed once by the CLI
// (or wired by hand in tests) and passed to New().
type Deps struct {
	FS       FileSystem
	Cmd      Runner
	Prompter Prompter
	Stdout   io.Writer

	// Home is the operator's home directory. Drives unit path,
	// linger username, the "binary under ~/.cache" warning, etc.
	Home string
	// User is the login name (drives `loginctl enable-linger`).
	User string
	// XDGDataHome / XDGConfHome are the operator's XDG dirs.
	XDGDataHome string
	XDGConfHome string

	// WSL marks this as a Windows Subsystem for Linux session.
	WSL bool

	// Fingerprint is the deterministic per-instance suffix appended to
	// the systemd unit name and launchd label so multiple RunWisp
	// daemons (different data dirs / cwd) on the same host install
	// non-conflicting units. Required.
	Fingerprint string

	// StdinIsTTY reflects whether stdin is attached to a terminal.
	StdinIsTTY bool

	// Euid is os.Geteuid(), injected rather than read inline so a test can
	// describe a root-image machine (no `sudo` binary) without actually
	// running as root. It decides whether a systemWide systemctl call gets
	// a "sudo" prefix: root already has the privilege, and a minimal
	// Debian/Alpine root image may not have sudo installed at all.
	Euid int
}

// DefaultDeps returns a Deps with production seams, sniffing the
// usual environment signals for home / user / XDG / WSL / TTY.
func DefaultDeps(stdout io.Writer, stdin *os.File, autoOK bool) (Deps, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Deps{}, err
	}
	u, err := user.Current()
	if err != nil {
		return Deps{}, err
	}
	isTTY := stdin != nil && isatty.IsTerminal(stdin.Fd())
	d := Deps{
		FS:          NewOSFileSystem(),
		Cmd:         NewRunner(),
		Stdout:      stdout,
		Home:        home,
		User:        u.Username,
		XDGDataHome: os.Getenv("XDG_DATA_HOME"),
		XDGConfHome: os.Getenv("XDG_CONFIG_HOME"),
		WSL:         DetectWSLFromEnv(),
		Fingerprint: fingerprint.Generate(),
		StdinIsTTY:  isTTY,
		Euid:        os.Geteuid(),
	}
	var stdinReader io.Reader = os.Stdin
	if stdin != nil {
		stdinReader = stdin
	}
	d.Prompter = NewStdioPrompter(stdinReader, stdout, isTTY, autoOK)
	return d, nil
}
