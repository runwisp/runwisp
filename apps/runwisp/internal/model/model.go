// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
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

// TaskNameMaxLength caps task names so they fit comfortably in filenames,
// log paths, and URL segments without further truncation.
const TaskNameMaxLength = 100

// TaskNamePatternString is the canonical regular expression that defines a
// valid task name. Kept in sync with the huma `pattern:` tags on REST
// request inputs so an operator who passes TOML validation can also call
// the API for the same name.
const TaskNamePatternString = `^[a-zA-Z0-9._-]+$`

var TaskNamePattern = regexp.MustCompile(TaskNamePatternString)

// ValidateTaskName checks that a name is non-empty, within the length cap,
// and matches TaskNamePattern. It is the single source of truth for task
// name validation across the daemon (TOML loader, REST handlers, etc.).
func ValidateTaskName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("task name is required")
	}
	if len(trimmed) > TaskNameMaxLength {
		return fmt.Errorf("task name %q exceeds the %d-character limit", trimmed, TaskNameMaxLength)
	}
	if !TaskNamePattern.MatchString(trimmed) {
		return fmt.Errorf("invalid task name %q: must match %s", trimmed, TaskNamePatternString)
	}
	return nil
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
	Timezone       string          `toml:"timezone,omitempty"           json:"timezone,omitempty" doc:"IANA timezone for cron evaluation; falls back to scheduler.timezone, then the daemon's resolved system timezone"`
	APITrigger     bool            `toml:"api_trigger,omitempty"        json:"api_trigger"`
	CatchUp        MissedRunPolicy `toml:"catch_up,omitempty"           json:"catch_up,omitempty" enum:"latest,all,skip" doc:"What to do when cron ticks are missed during downtime"`
	MaxCatchUpRuns int             `toml:"max_catch_up_runs,omitempty"  json:"max_catch_up_runs,omitempty" doc:"Cap on catch-up runs triggered when catch_up = all"`

	Timeout       time.Duration     `toml:"-"                       json:"timeout,omitempty" doc:"Per-run timeout in nanoseconds"`
	GracefulStop  time.Duration     `toml:"-"                       json:"graceful_stop,omitempty" doc:"Window between SIGTERM and SIGKILL when a run is stopped, in nanoseconds"`
	Restart       RestartPolicy     `toml:"restart,omitempty"       json:"restart,omitempty" enum:"never,always,on_failure" doc:"Whether and when a task is restarted after completion"`
	MaxConcurrent int               `toml:"max_concurrent,omitempty" json:"max_concurrent,omitempty" doc:"Maximum overlapping runs allowed for this task"`
	QueueMax      int               `toml:"queue_max,omitempty"     json:"queue_max,omitempty" doc:"Maximum runs that can wait when on_overlap = queue"`
	OnOverlap     ConcurrencyPolicy `toml:"on_overlap,omitempty"    json:"on_overlap,omitempty" enum:"queue,skip,terminate" doc:"How overlapping runs are handled"`

	Instances         int           `toml:"instances,omitempty"      json:"instances,omitempty" doc:"For services: number of always-running instances"`
	RestartDelay      time.Duration `toml:"-"                        json:"restart_delay,omitempty" doc:"Base delay before each restart, in nanoseconds"`
	RestartBackoff    string        `toml:"restart_backoff,omitempty" json:"restart_backoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive restarts"`
	BackoffResetAfter time.Duration `toml:"-"                        json:"backoff_reset_after,omitempty" doc:"For services: an instance that runs at least this long resets the restart counter, in nanoseconds"`

	RetryAttempts int           `toml:"retry_attempts,omitempty" json:"retry_attempts,omitempty"`
	RetryDelay    time.Duration `toml:"-"                        json:"retry_delay,omitempty" doc:"Base delay before each retry, in nanoseconds"`
	RetryBackoff  string        `toml:"retry_backoff,omitempty"  json:"retry_backoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive retries"`

	LogMaxSize int64  `toml:"-"                     json:"log_max_size,omitempty" doc:"Per-run log size cap in bytes"`
	LogOnFull  string `toml:"log_on_full,omitempty" json:"log_on_full,omitempty" enum:"drop_new,drop_old,kill_task" doc:"What to do when log output exceeds log_max_size"`

	KeepRuns int           `toml:"keep_runs,omitempty" json:"keep_runs,omitempty" doc:"Row-count retention cap; 0 means no cap was configured"`
	KeepFor  time.Duration `toml:"-"                   json:"keep_for,omitempty" doc:"Retention window in nanoseconds; 0 means no cap was configured"`

	Env     map[string]string `toml:"env,omitempty"      json:"env,omitempty"      doc:"Environment variables overlaid on the task's process env. Values are visible to authenticated operators in the API/UI; env_file values merge in beneath the inline entries."`
	EnvFile string            `toml:"env_file,omitempty" json:"env_file,omitempty" doc:"Path to a dotenv file whose KEY=VALUE pairs merge into env (inline entries win). Values are visible in the API/UI like inline env."`
	// Secrets holds [tasks.*.secrets] plus secrets_file-derived pairs. Hidden
	// from JSON/TOML so values never leak to API/UI/cloud serialization.
	Secrets     map[string]string `toml:"-" json:"-"`
	SecretsFile string            `toml:"secrets_file,omitempty" json:"secrets_file,omitempty" doc:"Path to a dotenv file whose KEY=VALUE pairs are injected into the task's process env. The path is visible in the API/UI; keys and values never leave the daemon."`

	Run          string          `toml:"run,omitempty" json:"-"`
	ExecutionDef ExecutionDef    `toml:"-"             json:"-"`
	Compose      *TaskComposeRef `toml:"-"             json:"compose,omitempty" doc:"Provenance metadata for tasks imported from a docker compose file"`
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

// DaemonInfo holds static identity/config data exposed via /api/info.
//
// ResolvedTimezone is the IANA zone the scheduler is actually using; it equals
// either the operator's explicit [scheduler] timezone or — when omitted — the
// system zone the daemon detected at boot. TimezoneSource ("config" or
// "system") tells the UI which path produced the value, so the Web UI header
// can label the chip without re-implementing the resolver.
// ServiceManaged is true when the daemon process was started by an init
// system (systemd / launchd) rather than by hand. UIs use it to steer the
// operator toward `runwisp stop` / `runwisp restart` instead of raw signals
// that would desync the service manager.
//
// ConfigLoadedAt is when the daemon read runwisp.toml; ConfigStale flips to
// true when the file (or a referenced env_file) has changed on disk since —
// config reload is restart-only, so UIs surface a "restart to apply" hint.
// ConfigStale is recomputed per request, not cached.
type DaemonInfo struct {
	Version          string      `json:"version"`
	Fingerprint      string      `json:"fingerprint"`
	Port             int         `json:"port"`
	CloudEnabled     bool        `json:"cloud_enabled"`
	ServiceManaged   bool        `json:"service_managed"`
	ConfigLoadedAt   time.Time   `json:"config_loaded_at"`
	ConfigStale      bool        `json:"config_stale"`
	ResolvedTimezone string      `json:"resolved_timezone"`
	TimezoneSource   string      `json:"timezone_source" enum:"config,system"`
	Tasks            []TaskBrief `json:"tasks"`
	Capabilities     []CapInfo   `json:"capabilities"`
}

// TaskBrief is a trimmed task descriptor exposed via the API.
type TaskBrief struct {
	Name          string            `json:"name"`
	Kind          TaskKind          `json:"kind,omitempty" enum:"task,service"`
	Group         string            `json:"group,omitempty"`
	Cron          string            `json:"cron,omitempty"`
	APITrigger    bool              `json:"api_trigger"`
	CatchUp       MissedRunPolicy   `json:"catch_up,omitempty"`
	Restart       RestartPolicy     `json:"restart,omitempty"`
	MaxConcurrent int               `json:"max_concurrent,omitempty"`
	OnOverlap     ConcurrencyPolicy `json:"on_overlap,omitempty"`
	Instances     int               `json:"instances,omitempty"`
	Compose       *TaskComposeRef   `json:"compose,omitempty"`
}

// TaskComposeRef identifies the compose file and service backing a task.
// Used by the UI to render the "compose" provenance badge.
type TaskComposeRef struct {
	File        string `json:"file"`
	Service     string `json:"service,omitempty"`
	ProjectName string `json:"project_name"`
}

// CapInfo describes a daemon capability.
type CapInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
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
