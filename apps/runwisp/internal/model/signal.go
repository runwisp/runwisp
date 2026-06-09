// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import "strings"

// StopSignals is the canonical allowlist of signal names accepted by the
// stop_signal config key, in "SIGxxx" form. These are the only signals
// meaningful as the first step of the stop ladder (the daemon always follows
// with SIGKILL after graceful_stop). The name→syscall.Signal resolution lives
// in the executor package so this stays free of a syscall import and portable.
var StopSignals = []string{"SIGTERM", "SIGINT", "SIGQUIT", "SIGHUP", "SIGKILL", "SIGUSR1", "SIGUSR2"}

// NormalizeSignalName canonicalizes a signal name to "SIGxxx" form and reports
// whether it is one of the accepted stop signals. Input is case-insensitive
// and the "SIG" prefix is optional (both "TERM" and "SIGTERM" are accepted).
func NormalizeSignalName(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", false
	}
	up := strings.ToUpper(trimmed)
	if !strings.HasPrefix(up, "SIG") {
		up = "SIG" + up
	}
	for _, s := range StopSignals {
		if s == up {
			return up, true
		}
	}
	return up, false
}
