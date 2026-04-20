// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

// BackendStatus describes whether a particular execution backend is available.
type BackendStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Availability tracks which execution backends the daemon can use.
// This is determined at startup and transmitted to the control plane.
type Availability struct {
	Shell     BackendStatus `json:"shell"`
	Container BackendStatus `json:"container"`
	HTTP      BackendStatus `json:"http"`
	Config    BackendStatus `json:"config"`
}

// ForType returns the BackendStatus for the given execution type string.
func (a *Availability) ForType(execType string) BackendStatus {
	switch execType {
	case "shell":
		return a.Shell
	case "container":
		return a.Container
	case "http":
		return a.HTTP
	case "config":
		return a.Config
	default:
		return BackendStatus{Available: false, Reason: "unknown execution type"}
	}
}
