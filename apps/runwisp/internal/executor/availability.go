// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

// BackendStatus describes whether a particular execution backend is available.
type BackendStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Availability tracks which execution backends the daemon can use.
// This is determined at startup and transmitted to the control plane; only
// Config can change later, since a reload can add or remove every TOML task
// (see ConfigStatus).
type Availability struct {
	Shell     BackendStatus `json:"shell"`
	Container BackendStatus `json:"container"`
	HTTP      BackendStatus `json:"http"`
	Config    BackendStatus `json:"config"`
	Compose   BackendStatus `json:"compose"`
}

// ForType returns the BackendStatus for the given execution type string.
func (a Availability) ForType(execType string) BackendStatus {
	switch execType {
	case "shell":
		return a.Shell
	case "container":
		return a.Container
	case "http":
		return a.HTTP
	case "config":
		return a.Config
	case "compose":
		return a.Compose
	default:
		return BackendStatus{Available: false, Reason: "unknown execution type"}
	}
}

// ConfigStatus is the status of config-backed dispatch (triggering a TOML
// task), which is available exactly when the daemon has local tasks to resolve.
func ConfigStatus(hasLocalTasks bool) BackendStatus {
	if hasLocalTasks {
		return BackendStatus{Available: true}
	}
	return BackendStatus{Available: false, Reason: "no local tasks configured"}
}
