// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/textutil"
)

// daemonNotRunningHint is appended to "daemon not reachable" errors so a
// failed connection never dead-ends — the operator learns the next command
// without a docs lookup.
const daemonNotRunningHint = "start it with 'runwisp' (interactive) or 'runwisp daemon' (headless)"

// userFacingError carries an already-formatted, human-readable message that
// the top-level error renderer should print verbatim instead of through slog.
// Wrap lower-level errors with one of the helpers in this file whenever the
// message is meant to be seen by the end user.
type userFacingError struct {
	title   string
	details string
}

func (e *userFacingError) Error() string {
	if e.details == "" {
		return e.title
	}
	return e.title + "\n\n" + e.details
}

// isUserFacing reports whether err (or any error in its chain) is a
// userFacingError. Used by the CLI entry point to choose pretty rendering.
func isUserFacing(err error) (*userFacingError, bool) {
	for err != nil {
		if u, ok := err.(*userFacingError); ok {
			return u, true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
			continue
		}
		return nil, false
	}
	return nil, false
}

// renderUserFacingError writes a styled multi-line error block to w.
// Falls back to plain ASCII when the destination is not a terminal or when
// styling is disabled by the user's environment.
func renderUserFacingError(w io.Writer, e *userFacingError) {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "1", Dark: "9"})
	labelStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "1", Dark: "9"})

	title := titleStyle.Render("Error: ") + e.title
	fmt.Fprintln(w, title)
	if e.details != "" {
		fmt.Fprintln(w)
		for _, line := range strings.Split(strings.TrimRight(e.details, "\n"), "\n") {
			if strings.HasPrefix(line, "  - ") {
				fmt.Fprintln(w, "  "+labelStyle.Render("•")+" "+strings.TrimPrefix(line, "  - "))
				continue
			}
			fmt.Fprintln(w, line)
		}
	}
}

// unknownTaskError is returned when `runwisp exec` names a task that doesn't
// exist. It suggests the closest match for a likely typo and lists what is
// actually defined, so the operator never has to open runwisp.toml to find
// the right name.
func unknownTaskError(taskName string, available []string) error {
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)

	var b strings.Builder
	if suggestion := textutil.Closest(taskName, sorted); suggestion != "" {
		fmt.Fprintf(&b, "Did you mean %q?\n\n", suggestion)
	}
	if len(sorted) == 0 {
		b.WriteString("No tasks are defined. Add a [tasks.<name>] block to runwisp.toml first.")
	} else {
		b.WriteString("Available tasks:\n")
		for _, name := range sorted {
			b.WriteString("  - " + name + "\n")
		}
	}
	return &userFacingError{
		title:   fmt.Sprintf("task %q not found", taskName),
		details: b.String(),
	}
}

// passwordMismatchError is returned when a different RunWisp daemon is
// already running on the configured port with a different password.
func passwordMismatchError(port int) error {
	return &userFacingError{
		title: fmt.Sprintf("another RunWisp daemon is already running on port %d with a different password", port),
		details: "This usually happens when an instance was started from a different directory.\n" +
			"To resolve this, you can:\n" +
			"  - Stop the other daemon first\n" +
			"  - Use a different port:  runwisp --port <PORT>\n" +
			"  - Set RUNWISP_PASSWORD to the other daemon's password to connect to it",
	}
}

// tuiPasswordMismatchError is the variant shown by the `tui` subcommand.
// explicit indicates the user supplied the password themselves.
func tuiPasswordMismatchError(port int, explicit bool) error {
	if explicit {
		return &userFacingError{
			title: fmt.Sprintf("the password does not match the daemon on port %d", port),
		}
	}
	return &userFacingError{
		title: fmt.Sprintf("password mismatch with the daemon on port %d", port),
		details: "The daemon was likely started from a different directory with its own password.\n" +
			"To fix this, either:\n" +
			"  - Use --password or RUNWISP_PASSWORD with the correct password\n" +
			"  - Stop the other daemon and restart from this directory",
	}
}

// authRateLimitedError is returned when the daemon's auth rate limiter
// rejects our login attempt. The window and limit are exported constants in
// the server package, but we avoid importing them here to keep this file
// self-contained — the numbers match MaxAuthAttempts / AuthRateWindow.
func authRateLimitedError(port int) error {
	return &userFacingError{
		title: fmt.Sprintf("too many authentication attempts against the daemon on port %d", port),
		details: "The daemon temporarily blocks further login attempts from your IP after repeated failures.\n" +
			"To resolve this, you can:\n" +
			"  - Wait a few minutes and try again\n" +
			"  - Set RUNWISP_PASSWORD to the correct password before retrying\n" +
			"  - Stop and restart the daemon if you have lost the password",
	}
}
