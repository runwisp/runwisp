// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/fang"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/textutil"
)

// handleCLIError is the fang error handler, and it owns *every* CLI error path
// so the whole surface shares one branded look. RunWisp's hand-authored errors
// carry multi-line, bulleted guidance (CHAP, port conflicts, cert pins, unknown
// tasks, …); cobra contributes flat usage errors (unknown command / unknown
// flag). Both route through renderError — fang's styled default is never called,
// so its capitalize-first-word / force-trailing-period quirk never applies and
// our lowercase title convention holds. The fang.Styles argument is ignored;
// renderError draws from the shared brand hex constants instead.
func handleCLIError(w io.Writer, _ fang.Styles, err error) {
	if ufe, ok := isUserFacing(err); ok {
		renderError(w, ufe.title, ufe.details, "")
		return
	}
	// A --help hint only makes sense for cobra usage errors; appending it to
	// e.g. a config parse failure would send the operator down the wrong path.
	hint := ""
	if isUsageError(err) {
		hint = "Run 'runwisp --help' for usage."
	}
	renderError(w, err.Error(), "", hint)
}

// usageErrorMarkers are the stable substrings cobra uses to phrase the errors
// that mean "you invoked the command wrong" — the class for which a --help hint
// is genuinely useful. Matching on the message is the only seam cobra gives us:
// these errors are plain *errors.errorString values with no distinguishing type.
var usageErrorMarkers = []string{
	"unknown command",
	"unknown flag",
	"unknown shorthand flag",
	"flag needs an argument",
	"invalid argument",
	"required flag",
	"accepts ",
}

// isUsageError reports whether err is a cobra usage error, so handleCLIError can
// append the "run --help" hint only where it points somewhere useful.
func isUsageError(err error) bool {
	msg := err.Error()
	for _, marker := range usageErrorMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

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

// renderError writes a branded error block to w: an "ERROR" badge (matching
// fang's badge aesthetic — bold, dark text on the brand red) followed by the
// title, then the optional bulleted details, then an optional dim hint line.
// It is the single renderer behind every CLI error path (cobra/generic,
// userFacingError, and cmd_password). Kept in lipgloss v1 so the badge colors
// come from the same shared brand hex constants as the rest of the CLI;
// The profile comes from w via rendererFor, not from lipgloss's package-level
// renderer: errors go to stderr, and styling them by what os.Stdout happens to
// be writes escape sequences into a redirected error log.
func renderError(w io.Writer, title, details, hint string) {
	r := rendererFor(w)
	badge := r.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(brandErrorFgHex)).
		Background(lipgloss.Color(brandErrorHex)).
		Padding(0, 1).
		Render("ERROR")
	fmt.Fprintln(w, badge+" "+title)

	if details != "" {
		fmt.Fprintln(w)
		bullet := r.NewStyle().Foreground(lipgloss.Color(brandErrorHex)).Render("•")
		for _, line := range strings.Split(strings.TrimRight(details, "\n"), "\n") {
			if strings.HasPrefix(line, "  - ") {
				fmt.Fprintln(w, "  "+bullet+" "+strings.TrimPrefix(line, "  - "))
				continue
			}
			fmt.Fprintln(w, line)
		}
	}

	if hint != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, r.NewStyle().Faint(true).Render(hint))
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

// runwispPortConflictError is shown (non-interactively) when the port is held
// by *another RunWisp daemon* started from a different data directory. Unlike
// portConflictError, we know exactly what is there — so we name its datadir and
// config and point at the commands to connect to or stop it.
func runwispPortConflictError(host string, port int, info *model.InstanceInfo) error {
	displayHost := host
	if displayHost == "" {
		displayHost = "127.0.0.1"
	}
	var b strings.Builder
	b.WriteString("It was started from a different data directory, so it has its own socket and config:\n")
	for _, line := range instanceSummaryLines(info) {
		b.WriteString(line + "\n")
	}
	b.WriteString("\nYou can:\n")
	fmt.Fprintf(&b, "  - Connect to it:        runwisp tui --socket %s\n", info.SocketPath)
	fmt.Fprintf(&b, "  - Stop it:              runwisp stop --data %s\n", info.DataDir)
	b.WriteString("  - Or run on a different port:  runwisp --port <PORT>")
	return &userFacingError{
		title:   fmt.Sprintf("another RunWisp daemon (v%s, pid %d) is already running on %s:%d", info.Version, info.Pid, displayHost, port),
		details: b.String(),
	}
}

// instanceSummaryLines renders a discovered daemon's identity as aligned
// bullet lines, shared by the non-interactive error and the interactive prompt
// so both describe the running instance the same way.
func instanceSummaryLines(info *model.InstanceInfo) []string {
	return []string{
		"  - data dir:  " + info.DataDir,
		"  - config:    " + info.ConfigPath,
	}
}

// remoteAuthRequiredError is returned when `runwisp exec --url` needs to run
// the CHAP handshake (no cached session) but no password was supplied.
func remoteAuthRequiredError(baseURL string) error {
	return &userFacingError{
		title: fmt.Sprintf("a password is required to authenticate with %s", baseURL),
		details: "Provide the daemon's password so RunWisp can log in:\n" +
			"  - Pass --password <PASSWORD>\n" +
			"  - Or set RUNWISP_PASSWORD in the environment",
	}
}

// remoteAuthFailedError is returned when the remote daemon rejects our login
// (a 401), i.e. the password is wrong.
func remoteAuthFailedError(baseURL string) error {
	return &userFacingError{
		title: fmt.Sprintf("authentication with %s failed", baseURL),
		details: "The daemon rejected the password.\n" +
			"  - Check --password / RUNWISP_PASSWORD matches the daemon's password",
	}
}

// remoteRateLimitedError is returned when the remote daemon's auth rate
// limiter rejects our login (a 429).
func remoteRateLimitedError(baseURL string) error {
	return &userFacingError{
		title: fmt.Sprintf("too many authentication attempts against %s", baseURL),
		details: "The daemon temporarily blocks further logins from your IP after repeated attempts.\n" +
			"RunWisp caches the session token between calls, so this usually means the password is wrong.\n" +
			"  - Wait a few minutes and try again\n" +
			"  - Confirm --password / RUNWISP_PASSWORD is correct",
	}
}

// remoteUnreachableError is returned when the remote daemon can't be dialed.
func remoteUnreachableError(baseURL string, cause error) error {
	return &userFacingError{
		title: fmt.Sprintf("daemon at %s is not reachable (%v)", baseURL, cause),
		details: "Check that:\n" +
			"  - The URL is correct (scheme, host, and port)\n" +
			"  - The daemon is bound beyond loopback (runwisp daemon --host 0.0.0.0) or reachable through your reverse proxy\n" +
			"  - No firewall is blocking the connection",
	}
}

// certPinMismatchError is returned when a remote daemon's TLS certificate no
// longer matches the fingerprint pinned on first connect. Like ssh's
// host-key-changed warning it refuses to proceed: the operator either
// regenerated the cert (and should clear the stale pin) or the connection is
// being intercepted.
func certPinMismatchError(baseURL string, mismatch *apiclient.CertPinMismatchError) error {
	path, err := pinStorePath()
	if err != nil {
		path = "~/.cache/runwisp/pinned_certs.json"
	}
	return &userFacingError{
		title: fmt.Sprintf("TLS certificate for %s changed since first connect", baseURL),
		details: fmt.Sprintf("Pinned:   sha256:%s\n", mismatch.Pinned) +
			fmt.Sprintf("Presented: sha256:%s\n", mismatch.Got) +
			"This means the daemon's certificate was replaced. If you regenerated it\n" +
			"intentionally (or moved the daemon), verify the new fingerprint against the\n" +
			"daemon's startup banner, then remove the stale pin:\n" +
			fmt.Sprintf("  - Edit %s and delete the entry for %s\n", path, apiclient.NormalizeBaseURL(baseURL)) +
			"Otherwise the connection may be intercepted — do not proceed.",
	}
}

// remoteAPITriggerDisabledError is returned when the task exists but has
// api_trigger = false, so it can't be triggered over the API.
func remoteAPITriggerDisabledError(taskName string) error {
	return &userFacingError{
		title: fmt.Sprintf("task %q cannot be triggered over the API", taskName),
		details: "This task has api_trigger = false in runwisp.toml.\n" +
			"  - Remove that line (api_trigger defaults to true) to allow remote triggering",
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
