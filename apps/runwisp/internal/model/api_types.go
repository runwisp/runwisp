// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "time"

// TaskResponse extends a Task with the optional next scheduled run time.
type TaskResponse struct {
	Task
	NextRunAt *time.Time `json:"nextRunAt,omitempty"`
}

// ReloadResult is the diff produced by an explicit config reload: which tasks
// were added, removed, or changed relative to the previously-live set. It is
// the wire shape returned by POST /api/reload and by `runwisp reload`.
type ReloadResult struct {
	Added   []string           `json:"added" doc:"Names of tasks added by the reload"`
	Removed []string           `json:"removed" doc:"Names of tasks removed by the reload"`
	Changed []ReloadTaskChange `json:"changed" doc:"Tasks whose definition changed, with the reasons"`
	// Warnings carries the newly-live config's non-fatal findings — chiefly the
	// crontab jobs include_cron declined to schedule. Without it a `crontab -e`
	// followed by `runwisp reload` would report nothing about the job that didn't
	// come back, and the reload is exactly the moment the operator is watching.
	Warnings []string `json:"warnings,omitempty" doc:"Non-fatal findings in the newly-live config, e.g. crontab jobs that could not be scheduled"`
}

// ReloadTaskChange names a changed task and the human-readable reasons its
// definition differed (e.g. "schedule", "command", "env").
type ReloadTaskChange struct {
	Name    string   `json:"name" doc:"Task name"`
	Reasons []string `json:"reasons" doc:"Why the task is considered changed"`
}

// IsEmpty reports whether the reload changed nothing.
func (r ReloadResult) IsEmpty() bool {
	return len(r.Added) == 0 && len(r.Removed) == 0 && len(r.Changed) == 0
}

// SystemStats holds live system resource and identity information.
type SystemStats struct {
	CPUUsage float64 `json:"cpuUsage" doc:"CPU usage percentage (0-100)"`
	MemUsage float64 `json:"memUsage" doc:"Memory usage percentage (0-100)"`
	MemTotal uint64  `json:"memTotal" doc:"Total memory in bytes"`
	MemUsed  uint64  `json:"memUsed" doc:"Used memory in bytes"`
	Uptime   string  `json:"uptime" doc:"Human-readable uptime"`
	Version  string  `json:"version" doc:"RunWisp version"`
	Name     string  `json:"name" doc:"Application name"`
	CPUCores int     `json:"cpuCores" doc:"Number of CPU cores"`
	Host     string  `json:"host" doc:"Hostname"`
	OS       string  `json:"os" doc:"Operating system (e.g. linux, darwin, windows)"`
	Arch     string  `json:"arch" doc:"CPU architecture (e.g. amd64, arm64)"`
	WorkDir  string  `json:"workDir" doc:"Working directory of the daemon process"`
}

// MetricsSample is a single timestamped snapshot of system resource usage.
// Field names mirror SystemStats (cpuUsage/memUsage) so the two resource shapes
// agree. Timestamp is Unix seconds (the log-line `ts` field is milliseconds —
// deliberately a different, distinctly-named field).
type MetricsSample struct {
	Timestamp int64   `json:"timestamp" doc:"Unix timestamp (seconds)"`
	CPUUsage  float64 `json:"cpuUsage" doc:"CPU usage percentage (0-100)"`
	MemUsage  float64 `json:"memUsage" doc:"Memory usage percentage (0-100)"`
	MemUsed   uint64  `json:"memUsed" doc:"Used memory in bytes"`
	MemTotal  uint64  `json:"memTotal" doc:"Total memory in bytes"`
}
