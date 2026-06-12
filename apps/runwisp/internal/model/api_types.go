// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

// TaskResponse extends a Task with the optional next scheduled run time.
type TaskResponse struct {
	Task
	NextRunAt *string `json:"next_run_at,omitempty"`
}

// ReloadResult is the diff produced by an explicit config reload: which tasks
// were added, removed, or changed relative to the previously-live set. It is
// the wire shape returned by POST /api/reload and by `runwisp reload`.
type ReloadResult struct {
	Added   []string           `json:"added" doc:"Names of tasks added by the reload"`
	Removed []string           `json:"removed" doc:"Names of tasks removed by the reload"`
	Changed []ReloadTaskChange `json:"changed" doc:"Tasks whose definition changed, with the reasons"`
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
	CPUUsage float64 `json:"cpu_usage" doc:"CPU usage percentage (0-100)"`
	MemUsage float64 `json:"mem_usage" doc:"Memory usage percentage (0-100)"`
	MemTotal uint64  `json:"mem_total" doc:"Total memory in bytes"`
	MemUsed  uint64  `json:"mem_used" doc:"Used memory in bytes"`
	Uptime   string  `json:"uptime" doc:"Human-readable uptime"`
	Version  string  `json:"version" doc:"RunWisp version"`
	Name     string  `json:"name" doc:"Application name"`
	CPUCores int     `json:"cpu_cores" doc:"Number of CPU cores"`
	Host     string  `json:"host" doc:"Hostname"`
	OS       string  `json:"os" doc:"Operating system (e.g. linux, darwin, windows)"`
	Arch     string  `json:"arch" doc:"CPU architecture (e.g. amd64, arm64)"`
	WorkDir  string  `json:"work_dir" doc:"Working directory of the daemon process"`
}

// MetricsSample is a single timestamped snapshot of system resource usage.
type MetricsSample struct {
	Timestamp int64   `json:"ts" doc:"Unix timestamp (seconds)"`
	CPUUsage  float64 `json:"cpu" doc:"CPU usage percentage (0-100)"`
	MemUsage  float64 `json:"mem" doc:"Memory usage percentage (0-100)"`
	MemUsed   uint64  `json:"mem_used" doc:"Used memory in bytes"`
	MemTotal  uint64  `json:"mem_total" doc:"Total memory in bytes"`
}
