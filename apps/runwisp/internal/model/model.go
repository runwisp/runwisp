// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var taskNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// SanitizeTaskName replaces unsafe characters with underscores, making the
// name safe for use in file paths and other identifiers. Deliberately
// stricter than TaskNamePattern: characters like `:` and `.` are valid in
// task names but get flattened here so log directories stay portable
// (e.g. NTFS mounts under WSL reject colons).
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
const TaskNamePatternString = `^[a-zA-Z0-9._:-]+$`

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

	Cron     string `toml:"cron,omitempty"               json:"cron,omitempty"`
	Timezone string `toml:"timezone,omitempty"           json:"timezone,omitempty" doc:"IANA timezone for cron evaluation; falls back to scheduler.timezone, then the daemon's resolved system timezone"`
	// Jitter caps how far a cron task's start may slip so tasks sharing a fire
	// time take turns instead of all stampeding the machine at once. Jittered
	// runs pass through a daemon-wide work-conserving gate that targets one run
	// in flight at a time: a task runs as soon as the gate frees (right at its
	// tick when nothing contends) and slips up to this window only under
	// contention, when each task is released on its own staggered slot. The
	// slots are computed once at startup by leveling overlapping windows on a
	// 24-hour time-of-day dial (reload is restart-only) — same TOML + clock
	// yields the same slots — but actual start times depend on run durations,
	// like the queue policy. Task-only (services start every instance at boot)
	// and a no-op without a cron.
	Jitter         time.Duration   `toml:"-" json:"jitter,omitempty" doc:"Cap how far a cron task's start may slip so tasks sharing a fire time take turns through a daemon-wide one-at-a-time gate instead of stampeding; a run starts as soon as the gate frees and slips up to this window only under contention, in nanoseconds"`
	APITrigger     bool            `toml:"api_trigger,omitempty"        json:"api_trigger"`
	CatchUp        MissedRunPolicy `toml:"catch_up,omitempty"           json:"catch_up,omitempty" enum:"latest,all,skip" doc:"What to do when cron ticks are missed during downtime"`
	MaxCatchUpRuns int             `toml:"max_catch_up_runs,omitempty"  json:"max_catch_up_runs,omitempty" doc:"Cap on catch-up runs triggered when catch_up = all"`
	// RunOnStart fires the task once at daemon boot, independent of cron and
	// catch-up. The @reboot equivalent. Task-only — services already start every
	// instance at boot.
	RunOnStart bool `toml:"-" json:"run_on_start,omitempty" doc:"For tasks: fire once at daemon startup, in addition to any cron schedule"`

	Timeout       time.Duration     `toml:"-"                       json:"timeout,omitempty" doc:"Per-run timeout in nanoseconds"`
	GracefulStop  time.Duration     `toml:"-"                       json:"graceful_stop,omitempty" doc:"Window between the stop signal and SIGKILL when a run is stopped, in nanoseconds"`
	StopSignal    string            `toml:"-"                       json:"stop_signal,omitempty" enum:"SIGTERM,SIGINT,SIGQUIT,SIGHUP,SIGKILL,SIGUSR1,SIGUSR2" doc:"Signal sent to stop a run before SIGKILL; defaults to SIGTERM"`
	Restart       RestartPolicy     `toml:"restart,omitempty"       json:"restart,omitempty" enum:"never,always,on_failure" doc:"Whether and when a task is restarted after completion"`
	MaxConcurrent int               `toml:"max_concurrent,omitempty" json:"max_concurrent,omitempty" doc:"Maximum overlapping runs allowed for this task"`
	QueueMax      int               `toml:"queue_max,omitempty"     json:"queue_max,omitempty" doc:"Maximum runs that can wait when on_overlap = queue"`
	OnOverlap     ConcurrencyPolicy `toml:"on_overlap,omitempty"    json:"on_overlap,omitempty" enum:"queue,skip,terminate" doc:"How overlapping runs are handled"`

	Instances      int           `toml:"instances,omitempty"      json:"instances,omitempty" doc:"For services: number of always-running instances"`
	RestartDelay   time.Duration `toml:"-"                        json:"restart_delay,omitempty" doc:"Base delay before each restart, in nanoseconds"`
	RestartBackoff string        `toml:"restart_backoff,omitempty" json:"restart_backoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive restarts"`
	// HealthyAfter is the uptime an instance must reach to count as healthy.
	// Reaching it both resets the restart-backoff counter and clears the
	// failed-start streak; fast failures below it accrue toward StartRetries.
	// Service-only.
	HealthyAfter time.Duration `toml:"-" json:"healthy_after,omitempty" doc:"For services: an instance that runs at least this long counts as healthy — resets the restart counter and clears the failed-start streak; fast exits below it count toward start_retries, in nanoseconds"`
	// StartRetries is the number of consecutive fast failures the supervisor
	// tolerates before marking an instance FATAL and giving up. Service-only.
	StartRetries int `toml:"-" json:"start_retries,omitempty" doc:"For services: consecutive fast failures tolerated before an instance is marked FATAL and stops restarting"`
	// Priority orders service start at boot only (lower starts first; ties break
	// on name). It is not a dependency or readiness gate. Service-only.
	Priority int `toml:"-" json:"priority,omitempty" doc:"For services: boot start order, lowest first (name breaks ties). Start order only — not a dependency."`
	// Autostart controls whether a service comes up at boot. When false the
	// service boots in the stopped state and must be started via the API/UI.
	// Desired state is not persisted — it is re-derived from TOML each boot.
	Autostart bool `toml:"-" json:"autostart" doc:"For services: whether instances start at boot. False boots in the stopped state until started via API/UI."`
	// DependsOn names other services that must become healthy before this one
	// starts at boot. Boot ordering only — not a workflow DAG: no cascade
	// restarts, no run-to-completion edges. Service-only. A dependent that
	// never sees its dep go healthy starts anyway after a bounded window.
	DependsOn []string `toml:"-" json:"depends_on,omitempty" doc:"For services: service names that must be healthy before this one starts at boot — boot ordering only, not a workflow DAG"`

	RetryAttempts int           `toml:"retry_attempts,omitempty" json:"retry_attempts,omitempty"`
	RetryDelay    time.Duration `toml:"-"                        json:"retry_delay,omitempty" doc:"Base delay before each retry, in nanoseconds"`
	RetryBackoff  string        `toml:"retry_backoff,omitempty"  json:"retry_backoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive retries"`

	// ExitCodes lists the process exit codes treated as success. Defaults to
	// [0]. Any code not in the list ends the run as failed (which then drives
	// restart=on_failure, retry, and notifications).
	ExitCodes []int `toml:"-" json:"exit_codes,omitempty" doc:"Process exit codes treated as success; defaults to [0]"`

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

	// Parameters declares per-execution inputs an operator may supply at manual
	// trigger time (env vars, positional args, options, flags). Scheduled
	// firings use the declared defaults. Declarations come from TOML only — the
	// API/UI supply values, never definitions. Mapped from [tasks.*.params].
	Parameters []TaskParam `toml:"-" json:"parameters,omitempty" doc:"Per-execution parameters an operator may supply at manual trigger time; scheduled runs use the declared defaults"`

	Run          string          `toml:"run,omitempty" json:"-"`
	ExecutionDef ExecutionDef    `toml:"-"             json:"-"`
	Compose      *TaskComposeRef `toml:"-"             json:"compose,omitempty" doc:"Provenance metadata for tasks imported from a docker compose file"`

	// Source is where this task's definition came from, which is what the API/UI
	// "staged"/"cron" badge and the display-only Promote affordance are built on.
	// Derived from the entry's origin file at config load — re-derived every load,
	// so promoting a task into the root flips it to native automatically. Never a
	// TOML key.
	Source TaskSource `toml:"-" json:"source,omitempty" enum:"staged,cron" doc:"Where this task's definition came from: native (hand-authored TOML), staged (imported, not yet promoted), or cron (read live from a crontab via daemon.include_cron)"`
	// SourceFile is the absolute path of the file the definition came from, for
	// the sources where naming it is the useful part: which crontab a cron-sourced
	// task lives in, or which staging file to promote out of. Empty for native.
	SourceFile string `toml:"-" json:"source_file,omitempty" doc:"Absolute path of the crontab or staging file this task's definition was read from; empty for hand-authored TOML"`

	// WorkingDir is resolved to an absolute path at config load (relative to
	// the runwisp.toml directory). Empty inherits the daemon's working dir.
	//
	// One case stays literal: a `~` on a task that also sets RunUser means that
	// user's home, which the executor resolves per run from the credential it
	// looked up. See config.homeIsTheRunUsers and executor.resolveWorkingDir.
	WorkingDir string `toml:"-" json:"working_dir,omitempty" doc:"Resolved working directory for the task's process; empty inherits the daemon's working directory. A literal \"~\" means the run-as user's home, resolved at run time"`
	// Shell is the interpreter for `run` scripts, defaulting to /bin/sh. Must
	// be an absolute path. The invocation is `<shell> -e -c <script>` when the
	// interpreter is a recognised POSIX shell (ShellSupportsErrexit), so a
	// multi-line script stops at its first failing command, and
	// `<shell> -c <script>` otherwise.
	Shell string `toml:"-" json:"shell,omitempty" doc:"Absolute path to the shell interpreter for run scripts; defaults to /bin/sh"`
	// Umask is the canonical 4-digit octal file-creation mask applied in the
	// child before the run script executes. Empty inherits the daemon's umask.
	Umask string `toml:"-" json:"umask,omitempty" doc:"Octal file-creation mask applied to the run's process; empty inherits the daemon's umask"`
	// EnvBase selects what the run's environment starts from — the daemon's own
	// ("inherit", the default) or crond's minimal set ("clean"). Host shell runs
	// only; the container backends already build env from task.Env/Secrets alone.
	EnvBase EnvBase `toml:"-" json:"env_base,omitempty" doc:"What the run's environment starts from: 'inherit' (the daemon's, the default) or 'clean' (PATH, SHELL, HOME, USER/LOGNAME only, as crond gives a job)"`
	// RunUser drops the run's process to another OS user (and optionally group)
	// in `user` or `user:group` form; names or numeric ids are accepted on either
	// side. Empty runs as the daemon's own uid/gid. Switching users needs the
	// daemon running as root. Resolved at run time, not config load — the target
	// account may not exist when the config is validated. Rejected on
	// compose-backed tasks (the container runtime owns the container's user).
	RunUser string `toml:"-" json:"user,omitempty" doc:"Run the process as this OS user, in 'user' or 'user:group' form (name or numeric id). Empty runs as the daemon's user; switching users needs the daemon running as root."`
	// NotifyOnMissed gates whether a missed-run alert (run.missed) reaches the
	// failure subscribers for this task. A nil pointer means "not configured"
	// during loading; ApplyDefaults resolves it to a concrete value (default
	// true, or the [defaults] value). Config-internal like notify_on_failure —
	// never serialized to API/UI/cloud. Read it via NotifiesOnMissed.
	NotifyOnMissed *bool `toml:"-" json:"-"`

	// Ephemeral marks a task the daemon registered at runtime for a single
	// cloud-dispatched inline execution (never from TOML, never in the task
	// registry). The run manager reaps such a task — and its queue-drain
	// goroutine — once its last run retires with nothing queued, since reconcile
	// (which only ever sees registry/TOML tasks) has no path to remove it.
	// Runtime-only: never serialized to API/UI/cloud/TOML.
	Ephemeral bool `toml:"-" json:"-"`
}

// NotifiesOnMissed reports whether missed-run alerts are enabled for this task.
// Defaults to true when unset so a Task literal built outside the config loader
// (tests, ad-hoc dispatch) alerts by default.
func (t *Task) NotifiesOnMissed() bool {
	return t.NotifyOnMissed == nil || *t.NotifyOnMissed
}

// ResolvedExecutionDef returns the runtime execution definition for the task.
func (t *Task) ResolvedExecutionDef() ExecutionDef {
	if t.ExecutionDef != nil {
		return t.ExecutionDef
	}
	if strings.TrimSpace(t.Run) == "" {
		return nil
	}
	return &ShellExecution{Script: t.Run, Shell: t.Shell, WorkingDir: t.WorkingDir, Umask: t.Umask, EnvBase: t.EnvBase}
}

// ConcurrencyPolicy controls how overlapping runs are handled.
type ConcurrencyPolicy string

const (
	PolicyQueue     ConcurrencyPolicy = "queue"
	PolicySkip      ConcurrencyPolicy = "skip"
	PolicyTerminate ConcurrencyPolicy = "terminate"
)

// EnvBase selects what a host shell run's environment starts from, before the
// task's own env, secrets, and parameters are layered on top.
//
// It exists because the two schedulers RunWisp replaces disagree: crond hands a
// job a near-empty environment, while a supervisord program — and RunWisp
// itself, until a task says otherwise — inherits the supervisor's. Inheriting
// is the friendlier default (a task sees the PATH you tested it with), but it
// also means a job's behaviour depends on how the daemon happened to be
// started, which is exactly the surprise an operator migrating off cron does
// not want.
type EnvBase string

const (
	// EnvBaseInherit starts from the daemon's own environment, minus its
	// RUNWISP_* internals. The default.
	EnvBaseInherit EnvBase = "inherit"
	// EnvBaseClean starts from the minimal set crond guarantees a job — PATH,
	// SHELL, HOME, USER/LOGNAME — and nothing the daemon was started with.
	EnvBaseClean EnvBase = "clean"
)

// Valid reports whether b is a value the executor knows how to honor. The empty
// string is not valid: the config loader resolves it to EnvBaseInherit, so a
// zero value reaching this check means it bypassed the loader.
func (b EnvBase) Valid() bool { return b == EnvBaseInherit || b == EnvBaseClean }

// RestartPolicy controls whether and when a task is restarted after completion.
type RestartPolicy string

const (
	RestartNever     RestartPolicy = "never"
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on_failure"
)

// TaskKind distinguishes scheduled/manual tasks from always-on services. The
// value is always explicit ("task" or "service"): it is emitted verbatim on the
// sync wire and stored verbatim by cloud, so neither side infers a missing kind.
type TaskKind string

const (
	KindTask    TaskKind = "task"
	KindService TaskKind = "service"
)

// IsService reports whether the task is an always-on service.
func (k TaskKind) IsService() bool { return k == KindService }

// TaskSource is where a task's definition came from. It is derived provenance,
// not part of the definition: the same task reads as SourceStaged before
// `runwisp promote` and SourceNative after, with nothing about what runs having
// changed. config.sameDefinition masks it for exactly that reason.
//
// A string enum rather than a pair of bools so the three cases stay mutually
// exclusive by construction — "staged and cron" is not a state that exists, and a
// bool pair would let it be represented.
type TaskSource string

const (
	// SourceNative is a task the operator wrote in their own TOML. The zero value,
	// so a Task nobody stamped reads as native — which is the honest answer for a
	// compose-generated task, and the one that offers no Promote affordance.
	SourceNative TaskSource = ""
	// SourceStaged is a task whose definition lives in the machine-owned staging
	// file (runwisp.d/imported.toml, written by `runwisp import` and rewritten by
	// `runwisp promote`): imported, not yet promoted to native TOML.
	SourceStaged TaskSource = "staged"
	// SourceCron is a task read live from a real crontab via [daemon] include_cron.
	// The crontab is the definition — RunWisp never writes to it — so the task
	// changes when the operator runs `crontab -e`, not when they edit TOML.
	SourceCron TaskSource = "cron"
)

// Promotable reports whether this source has a `runwisp promote` path into the
// operator's own TOML.
func (s TaskSource) Promotable() bool { return s == SourceStaged || s == SourceCron }

// Service instance/roll-up state strings reported to cloud. They mirror the
// asyncapi ServiceInstanceState / ServiceState enums so the cloud bridge maps
// them without translation.
const (
	ServiceInstanceRunning    = "running"
	ServiceInstanceRestarting = "restarting"
	ServiceInstanceStopped    = "stopped"
	ServiceInstanceFatal      = "fatal"

	ServiceRunning  = "running"
	ServiceDegraded = "degraded"
	ServiceStopped  = "stopped"
	ServiceFatal    = "fatal"
)

// ServiceInstanceStatus is one instance slot's reported state. Pid/StartedAt/
// LastExitCode are best-effort: populated when the daemon has a live or just-
// exited run for the slot, zero/nil otherwise.
type ServiceInstanceStatus struct {
	Index        int
	State        string
	Pid          int
	StartedAt    *time.Time
	RestartCount int
	LastExitCode *int
}

// ServiceSnapshot is the supervisor + live-run view of one service, built by
// the runtime manager and forwarded to cloud as a service:status message.
type ServiceSnapshot struct {
	TaskName         string
	State            string
	DesiredInstances int
	RunningInstances int
	Instances        []ServiceInstanceStatus
}

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
// ConfigStale is recomputed per request, not cached. So is ConfigWarnings, which
// carries what the daemon would print at boot — a skipped crontab job has no runs,
// so this is one of the few places it can be seen at all.
//
// SchedulingActive is false when the local scheduler is inactive — e.g.
// `runwisp cloud`, where the cloud owns scheduling — so UIs hide next-run
// affordances rather than mislabel scheduled tasks as unscheduled. It is
// distinct from CloudEnabled, which only reports that a cloud connection is
// configured.
type DaemonInfo struct {
	Version          string      `json:"version"`
	Fingerprint      string      `json:"fingerprint"`
	Port             int         `json:"port"`
	ExternalURL      string      `json:"external_url"`
	CloudEnabled     bool        `json:"cloud_enabled"`
	SchedulingActive bool        `json:"scheduling_active"`
	ServiceManaged   bool        `json:"service_managed"`
	AuthDisabled     bool        `json:"auth_disabled"`
	ConfigLoadedAt   time.Time   `json:"config_loaded_at"`
	ConfigStale      bool        `json:"config_stale"`
	ConfigWarnings   []string    `json:"config_warnings,omitempty" doc:"Non-fatal findings in the live config, e.g. crontab jobs include_cron could not schedule. Re-derived per request, so it tracks reloads."`
	ResolvedTimezone string      `json:"resolved_timezone"`
	TimezoneSource   string      `json:"timezone_source" enum:"config,system"`
	Tasks            []TaskBrief `json:"tasks"`
	Capabilities     []CapInfo   `json:"capabilities"`
}

// InstanceInfo is the local-only identity of a running daemon, returned by
// GET /api/instance. It lets a second `runwisp` launching against an
// already-occupied port discover which daemon holds it — and where that
// daemon's datadir/config/socket live — so it can offer to connect or stop it.
// The endpoint is reachable only over the Unix socket or a loopback TCP peer,
// so these filesystem paths never reach the network.
type InstanceInfo struct {
	App         string `json:"app" doc:"Always \"runwisp\"; lets a caller confirm the port-holder is a RunWisp daemon."`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Pid         int    `json:"pid"`
	DataDir     string `json:"data_dir"`
	ConfigPath  string `json:"config_path"`
	SocketPath  string `json:"socket_path"`
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
	DependsOn     []string          `json:"depends_on,omitempty"`
	Compose       *TaskComposeRef   `json:"compose,omitempty"`
	Source        TaskSource        `json:"source,omitempty" enum:"staged,cron"`
	SourceFile    string            `json:"source_file,omitempty"`
	Parameters    []TaskParam       `json:"parameters,omitempty"`
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
