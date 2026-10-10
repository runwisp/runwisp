// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "time"

// TaskResponse extends a Task with its live state: the next scheduled run
// time, when an operator paused the cron schedule (if they did), whether a
// service is stopped and how its instances are doing, and the newest run.
// RunCommand is the task's run command, which Task itself keeps out of JSON.
type TaskResponse struct {
	Task
	NextRunAt      *time.Time     `json:"nextRunAt,omitempty"`
	PausedAt       *time.Time     `json:"pausedAt,omitempty" doc:"When an operator paused this task's cron schedule (POST /api/tasks/{taskName}/pause); absent when not paused. A paused task has no nextRunAt."`
	ServiceStopped bool           `json:"serviceStopped,omitempty" doc:"For services: true while the service is stopped (by an operator, or never started because autostart = false) and the daemon will not respawn it until it is started. Absent for running or restarting services and for tasks."`
	Usage          *ResourceUsage `json:"usage,omitempty" doc:"Live CPU and memory use of the task's running shell runs; absent when none is running or measured."`
	LastRun        *Run           `json:"lastRun,omitempty" doc:"The newest run of this task that started (pending, skipped and missed runs don't count); absent when none has."`
	Service        *ServiceStatus `json:"service,omitempty" doc:"For services: the supervisor's view of each instance. Absent for tasks."`
	RunCommand     string         `json:"run,omitempty" doc:"The run command as written in runwisp.toml; absent for tasks without one (container and compose tasks)."`
}

// ServiceStatus is the REST shape of a service's supervisor snapshot.
type ServiceStatus struct {
	State            string                  `json:"state" enum:"running,degraded,stopped,fatal" doc:"stopped: an operator stopped it, or autostart = false and it was never started; fatal: every instance gave up; degraded: some instances are down"`
	DesiredInstances int                     `json:"desiredInstances"`
	RunningInstances int                     `json:"runningInstances"`
	Instances        []ServiceInstanceStatus `json:"instances"`
}

// ServiceInstanceStatus is one instance slot's reported state. Pid/StartedAt/
// LastExitCode are best-effort: populated when the daemon has a live or just-
// exited run for the slot, zero/nil otherwise.
type ServiceInstanceStatus struct {
	Index        int        `json:"index" doc:"0-based slot, as RUNWISP_INSTANCE_INDEX"`
	State        string     `json:"state" enum:"running,restarting,stopped,fatal" doc:"restarting: between an exit and the next respawn; fatal: gave up after too many failed starts and won't respawn until started"`
	Pid          int        `json:"-"`
	StartedAt    *time.Time `json:"startedAt,omitempty" doc:"When the instance's current run started; absent unless running"`
	RestartCount int        `json:"restartCount" doc:"Exits since the instance last ran healthy"`
	// StartFails is the slot's consecutive failed starts since its last
	// healthy run; the slot goes FATAL once it passes restart_attempts.
	StartFails   int  `json:"startFails" doc:"Failed starts in a row since the instance last ran healthy; it gives up once this passes restartAttempts"`
	LastExitCode *int `json:"lastExitCode,omitempty" doc:"Exit code of the instance's latest run; absent until one has exited since the daemon started"`
}

// ResourceUsage is the live CPU and memory use of a task's running processes,
// summed over its active runs.
type ResourceUsage struct {
	CPUPercent  float64 `json:"cpuPercent" doc:"CPU use in percent of one core (200 = two full cores)"`
	MemoryBytes int64   `json:"memoryBytes" doc:"Resident memory in bytes"`
}

// ReloadResult is the diff produced by an explicit config reload: which tasks
// were added, removed, or changed relative to the previously-live set. It is
// the wire shape returned by POST /api/daemon/reload and by `runwisp reload`.
type ReloadResult struct {
	Added   []string           `json:"added" doc:"Names of tasks added by the reload"`
	Removed []string           `json:"removed" doc:"Names of tasks removed by the reload"`
	Changed []ReloadTaskChange `json:"changed" doc:"Tasks whose definition changed, with the reasons"`
	// Settings names the daemon-wide settings the reload applied, as TOML keys
	// (e.g. "daemon.timezone", "storage.max_size"); "notifications" covers
	// [notify], [notifiers.*] and [[route]].
	Settings []string `json:"settings,omitempty" doc:"Daemon-wide settings the reload applied, as TOML keys; notifications covers [notify], [notifiers.*] and [[route]]"`
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
	return len(r.Added) == 0 && len(r.Removed) == 0 && len(r.Changed) == 0 && len(r.Settings) == 0
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
