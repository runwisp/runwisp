// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

// ScopeDetection answers "which scope is this host actually installed
// under?" so the read/control commands (status, uninstall, stop, restart)
// can find the unit instead of making the operator remember how the install
// was run.
type ScopeDetection struct {
	// SystemPath / UserPath are where each scope's unit would live. An
	// empty string means the scope does not exist on this host at all:
	// macOS has no system-wide install yet, and the user scope needs a
	// fingerprint to name its unit.
	SystemPath string
	UserPath   string

	// SystemFound / UserFound report whether a file is actually there.
	SystemFound bool
	UserFound   bool
}

// DetectScope stats both candidate unit paths. It deliberately does not
// decide anything — "both installed" is ambiguous and only the caller knows
// whether to refuse or pick a side.
func DetectScope(deps Deps) ScopeDetection {
	d := ScopeDetection{}
	d.SystemPath, d.UserPath = ScopeCandidates(deps)
	if d.SystemPath != "" {
		if _, err := deps.FS.Stat(d.SystemPath); err == nil {
			d.SystemFound = true
		}
	}
	if d.UserPath != "" {
		if _, err := deps.FS.Stat(d.UserPath); err == nil {
			d.UserFound = true
		}
	}
	return d
}
