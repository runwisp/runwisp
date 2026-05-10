// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"regexp"
	"strings"
	"time"
)

var taskNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// SanitizeTaskName replaces unsafe characters with underscores, making the
// name safe for use in file paths and other identifiers.
func SanitizeTaskName(name string) string {
	return taskNameSanitizer.ReplaceAllString(name, "_")
}

// Task describes a runnable task loaded from configuration.
//
// Duration and size fields are parsed from their human-readable TOML form
// (e.g. "30m", "100mb") at config load time and stored as native Go types.
// JSON output therefore renders them as integer nanoseconds / bytes.
type Task struct {
	Name        string   `toml:"-"                     json:"name"`
	Kind        TaskKind `toml:"-"                     json:"kind,omitempty" enum:"task,service" doc:"Whether this is a scheduled task or an always-on service"`
	Group       string   `toml:"group,omitempty"       json:"group,omitempty"`
	Description string   `toml:"description,omitempty" json:"description,omitempty"`

	Cron           string          `toml:"cron,omitempty"               json:"cron,omitempty"`
	Timezone       string          `toml:"timezone,omitempty"           json:"timezone,omitempty" doc:"IANA timezone for cron evaluation; required (per-task or via scheduler.timezone) when cron is set"`
	APITrigger     bool            `toml:"api_trigger,omitempty"        json:"api_trigger"`
	CatchUp        MissedRunPolicy `toml:"catch_up,omitempty"           json:"catch_up,omitempty" enum:"latest,all,skip" doc:"What to do when cron ticks are missed during downtime"`
	MaxCatchUpRuns int             `toml:"max_catch_up_runs,omitempty"  json:"max_catch_up_runs,omitempty" doc:"Cap on catch-up runs triggered when catch_up = all (0 inherits default, -1 means unlimited, positive caps)"`

	Timeout     time.Duration     `toml:"-"                     json:"timeout,omitempty" doc:"Per-run timeout in nanoseconds"`
	Restart     RestartPolicy     `toml:"restart,omitempty"     json:"restart,omitempty" enum:"never,always,on_failure" doc:"Whether and when a task is restarted after completion"`
	Parallelism int               `toml:"parallelism,omitempty" json:"parallelism,omitempty"`
	OnOverlap   ConcurrencyPolicy `toml:"on_overlap,omitempty"  json:"on_overlap,omitempty" enum:"queue,skip,terminate" doc:"How overlapping runs are handled"`

	Instances      int           `toml:"instances,omitempty"      json:"instances,omitempty" doc:"For services: number of always-running replicas"`
	RestartDelay   time.Duration `toml:"-"                        json:"restart_delay,omitempty" doc:"Base delay before each restart, in nanoseconds"`
	RestartBackoff string        `toml:"restart_backoff,omitempty" json:"restart_backoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive restarts"`

	RetryAttempts int           `toml:"retry_attempts,omitempty" json:"retry_attempts,omitempty"`
	RetryDelay    time.Duration `toml:"-"                        json:"retry_delay,omitempty" doc:"Base delay before each retry, in nanoseconds"`
	RetryBackoff  string        `toml:"retry_backoff,omitempty"  json:"retry_backoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive retries"`

	LogMaxSize int64  `toml:"-"                     json:"log_max_size,omitempty" doc:"Per-task log size cap, in bytes"`
	LogOnFull  string `toml:"log_on_full,omitempty" json:"log_on_full,omitempty" enum:"drop_new,drop_old,kill_task" doc:"What to do when log output exceeds log_max_size"`

	KeepRuns int           `toml:"keep_runs,omitempty" json:"keep_runs,omitempty"`
	KeepFor  time.Duration `toml:"-"                   json:"keep_for,omitempty" doc:"Retention window, in nanoseconds"`

	Run          string       `toml:"run,omitempty" json:"-"`
	ExecutionDef ExecutionDef `toml:"-"             json:"-"`
}

// ResolvedExecutionDef returns the runtime execution definition for the task.
func (t *Task) ResolvedExecutionDef() ExecutionDef {
	if t.ExecutionDef != nil {
		return t.ExecutionDef
	}
	if strings.TrimSpace(t.Run) == "" {
		return nil
	}
	return &ShellExecution{Script: t.Run}
}

// RunPhase captures the high-level lifecycle phase of a run.
type RunPhase string

const (
	PhasePending RunPhase = "pending"
	PhaseRunning RunPhase = "running"
	PhaseEnded   RunPhase = "ended"
)

// EndReason describes why a run ended.
type EndReason string

const (
	ReasonSuccess EndReason = "success"
	ReasonFailed  EndReason = "failed"
	ReasonStopped EndReason = "stopped"
	ReasonTimeout EndReason = "timeout"
	ReasonCrashed EndReason = "crashed"
	// ReasonSkipped marks runs that the concurrency policy rejected before
	// they ever executed (PolicySkip). Distinct from ReasonFailed so chronic
	// overlap on a `* * * * *` health probe doesn't pose as a real failure
	// to retries, notifications, or dashboards. Exit code is conventionally
	// recorded as -1.
	ReasonSkipped EndReason = "skipped"
	// ReasonLogOverflow marks runs that log_on_full = "kill_task" cancelled
	// because output exceeded log_max_size. Treated as a failure for retry,
	// notification, and UI purposes — the run failed to stay inside its log
	// budget — but recorded as a distinct reason so operators can see the
	// specific cause without inspecting logs.
	ReasonLogOverflow EndReason = "log_overflow"
)

// IsTerminal reports whether the phase represents a completed run.
func (p RunPhase) IsTerminal() bool {
	return p == PhaseEnded
}

// TriggeredBy identifies how a run started.
type TriggeredBy string

const (
	TriggeredByCron  TriggeredBy = "cron"
	TriggeredByAPI   TriggeredBy = "api"
	TriggeredByCloud TriggeredBy = "cloud"
)

// ConcurrencyPolicy controls how overlapping runs are handled.
type ConcurrencyPolicy string

const (
	PolicyQueue     ConcurrencyPolicy = "queue"
	PolicySkip      ConcurrencyPolicy = "skip"
	PolicyTerminate ConcurrencyPolicy = "terminate"
)

// RestartPolicy controls whether and when a task is restarted after completion.
type RestartPolicy string

const (
	RestartNever     RestartPolicy = "never"
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on_failure"
)

// TaskKind distinguishes scheduled/manual tasks from always-on services.
type TaskKind string

const (
	KindTask    TaskKind = ""
	KindService TaskKind = "service"
)

// IsService reports whether the task is an always-on service.
func (k TaskKind) IsService() bool { return k == KindService }

// Backoff curves shared by retry_backoff (tasks) and restart_backoff (services).
const (
	BackoffConstant    = "constant"
	BackoffLinear      = "linear"
	BackoffExponential = "exponential"
)

// MissedRunPolicy controls what happens when cron ticks are missed (e.g. daemon downtime).
type MissedRunPolicy string

const (
	MissedRunLatest MissedRunPolicy = "latest" // one catch-up run for the most recent missed tick
	MissedRunAll    MissedRunPolicy = "all"    // one run per missed tick
	MissedRunSkip   MissedRunPolicy = "skip"   // drop missed ticks
)

// Log overflow behavior when task output exceeds log_max_size.
const (
	LogOverflowDropNew  = "drop_new"  // stop writing; keep older output (task keeps running)
	LogOverflowDropOld  = "drop_old"  // rotate: keep recent output (task keeps running)
	LogOverflowKillTask = "kill_task" // terminate the task
)

// Run represents a persisted execution.
type Run struct {
	ID                  string      `json:"id"`
	ExternalExecutionID *string     `json:"external_execution_id,omitempty"`
	TaskName            string      `json:"task_name"`
	Status              RunPhase    `json:"status" enum:"pending,running,ended" doc:"Run lifecycle phase"`
	EndReason           *EndReason  `json:"end_reason,omitempty" enum:"success,failed,stopped,timeout,crashed,skipped,log_overflow" doc:"Why the run ended (set when status=ended)"`
	ExitCode            int         `json:"exit_code"`
	LogPath             string      `json:"-"`
	StartAt             *time.Time  `json:"start_at,omitempty"`
	EndAt               *time.Time  `json:"end_at,omitempty"`
	TriggeredBy         TriggeredBy `json:"triggered_by" enum:"cron,api,cloud" doc:"How the run was triggered"`
	CreatedAt           time.Time   `json:"created_at"`
	RetryAttempt        int         `json:"retry_attempt"`
	RetryOfRunID        *string     `json:"retry_of_run_id,omitempty"`
	ReplicaIndex        int         `json:"replica_index"`
}

// Copy creates a deep copy of the Run to prevent data races.
func (r *Run) Copy() *Run {
	if r == nil {
		return nil
	}
	cpy := *r
	if r.ExternalExecutionID != nil {
		eid := *r.ExternalExecutionID
		cpy.ExternalExecutionID = &eid
	}
	if r.EndReason != nil {
		er := *r.EndReason
		cpy.EndReason = &er
	}
	if r.StartAt != nil {
		sa := *r.StartAt
		cpy.StartAt = &sa
	}
	if r.EndAt != nil {
		ea := *r.EndAt
		cpy.EndAt = &ea
	}
	if r.RetryOfRunID != nil {
		rid := *r.RetryOfRunID
		cpy.RetryOfRunID = &rid
	}
	return &cpy
}

// End transitions a run to the ended phase with the given reason.
func (r *Run) End(reason EndReason, exitCode int, endAt time.Time) {
	r.Status = PhaseEnded
	r.EndReason = &reason
	r.ExitCode = exitCode
	r.EndAt = &endAt
}

// IsRetryable reports whether a run ended with a reason that warrants
// re-running it. Skipped runs are excluded — the policy already decided
// the firing was redundant; another retry just races the original again.
func (r *Run) IsRetryable() bool {
	if r.Status != PhaseEnded || r.EndReason == nil {
		return false
	}
	switch *r.EndReason {
	case ReasonSuccess, ReasonSkipped:
		return false
	default:
		return true
	}
}

// EndReasonPtr returns a pointer to the given EndReason value.
func EndReasonPtr(r EndReason) *EndReason { return &r }

// DisplayStatus returns the end reason string when ended, otherwise the phase.
func (r *Run) DisplayStatus() string {
	if r.Status == PhaseEnded {
		if r.EndReason != nil {
			return string(*r.EndReason)
		}
		return string(ReasonStopped)
	}
	return string(r.Status)
}

// DaemonInfo holds static identity/config data exposed via /api/info.
type DaemonInfo struct {
	Version      string      `json:"version"`
	Fingerprint  string      `json:"fingerprint"`
	Port         int         `json:"port"`
	CloudEnabled bool        `json:"cloud_enabled"`
	Tasks        []TaskBrief `json:"tasks"`
	Capabilities []CapInfo   `json:"capabilities"`
}

// TaskBrief is a trimmed task descriptor exposed via the API.
type TaskBrief struct {
	Name        string            `json:"name"`
	Kind        TaskKind          `json:"kind,omitempty" enum:"task,service"`
	Group       string            `json:"group,omitempty"`
	Cron        string            `json:"cron,omitempty"`
	APITrigger  bool              `json:"api_trigger"`
	CatchUp     MissedRunPolicy   `json:"catch_up,omitempty"`
	Restart     RestartPolicy     `json:"restart,omitempty"`
	Parallelism int               `json:"parallelism,omitempty"`
	OnOverlap   ConcurrencyPolicy `json:"on_overlap,omitempty"`
	Instances   int               `json:"instances,omitempty"`
}

// CapInfo describes a daemon capability.
type CapInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// RunSummary holds aggregate run statistics.
type RunSummary struct {
	Total       int64      `json:"total" doc:"Total number of runs"`
	Success     int64      `json:"success" doc:"Number of successful runs"`
	Failed      int64      `json:"failed" doc:"Number of failed runs"`
	LastFailure *time.Time `json:"last_failure,omitempty" doc:"Timestamp of most recent failure"`
}

// TaskRegistration is a one-to-one per-task record for metadata that has no
// natural home in the run log (first-seen timestamp, future per-task flags, etc.).
type TaskRegistration struct {
	TaskName    string
	FirstSeenAt time.Time
}

// ConfigEntry stores a named daemon configuration value persisted in the database.
type ConfigEntry struct {
	Key   string
	Value string
}
