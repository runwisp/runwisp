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
	Jitter time.Duration `toml:"-" json:"jitter,omitempty" doc:"Cap how far a cron task's start may slip so tasks sharing a fire time take turns through a daemon-wide one-at-a-time gate instead of stampeding; a run starts as soon as the gate frees and slips up to this window only under contention, in nanoseconds"`
	// ManualTrigger means different things by Kind: on a task, whether it can
	// be run outside its cron schedule (see Triggerable). On a service,
	// whether it can be stopped/restarted/started outside its restart policy
	// (see ManuallyControllable): false locks it to the supervisor, changeable
	// only by editing runwisp.toml and reloading. Defaults true either way.
	ManualTrigger bool `toml:"manual_trigger,omitempty"        json:"manualTrigger"`
	// CatchUp is the maximum number of missed cron ticks to re-run at startup
	// after downtime: 0 skips (re-run none), 1 re-runs only the most recent, N
	// re-runs up to the N most recent (older ticks are recorded as missed but not
	// re-fired). A pointer so an explicit `catch_up = 0` (skip) is distinguishable
	// from an omitted key (nil, inherits [defaults] then the built-in default of
	// 1). Cron-task concept; resolved to non-nil by the config loader. Read via
	// CatchUpValue, which falls back to the default of 1 when nil.
	CatchUp *int `toml:"catch_up,omitempty" json:"catchUp,omitempty" doc:"Max missed cron ticks to re-run at startup after downtime: 0 skips, 1 re-runs only the most recent, N re-runs up to the N most recent"`
	// RunOnStart fires the task once at daemon boot, independent of cron and
	// catch-up. The @reboot equivalent. Task-only — services already start every
	// instance at boot.
	RunOnStart bool `toml:"-" json:"runOnStart" doc:"For tasks: fire once at daemon startup, in addition to any cron schedule"`

	Timeout time.Duration `toml:"-"                       json:"timeout,omitempty" doc:"Per-run timeout in nanoseconds"`
	// GracefulStop is a pointer so an explicit `graceful_stop = "0s"` (kill
	// immediately, no grace window) is distinguishable from an omitted key (nil,
	// inherits [defaults] then the built-in default). Applies to tasks and
	// services. Config-loaded tasks always have it resolved to non-nil; nil only
	// occurs for tasks built outside config.Load (cloud dispatch, tests) and is
	// read as 0 (immediate SIGKILL) via GracefulStopValue.
	GracefulStop  *time.Duration    `toml:"-"                       json:"gracefulStop,omitempty" doc:"Window between the stop signal and SIGKILL when a run is stopped, in nanoseconds; 0 means kill immediately"`
	StopSignal    string            `toml:"-"                       json:"stopSignal,omitempty" enum:"SIGTERM,SIGINT,SIGQUIT,SIGHUP,SIGKILL,SIGUSR1,SIGUSR2" doc:"Signal sent to stop a run before SIGKILL; defaults to SIGTERM"`
	Restart       RestartPolicy     `toml:"restart,omitempty"       json:"restart,omitempty" enum:"never,always,on_failure" doc:"For services: whether and when an instance is restarted (defaults to always). Tasks re-run a failed run via retry_* instead."`
	MaxConcurrent int               `toml:"max_concurrent,omitempty" json:"maxConcurrent,omitempty" doc:"Maximum overlapping runs allowed for this task"`
	MaxQueued     int               `toml:"max_queued,omitempty"    json:"maxQueued,omitempty" doc:"Maximum runs that can wait when on_overlap = queue"`
	OnOverlap     ConcurrencyPolicy `toml:"on_overlap,omitempty"    json:"onOverlap,omitempty" enum:"queue,skip,kill" doc:"How overlapping runs are handled"`

	Instances int `toml:"instances,omitempty"      json:"instances,omitempty" doc:"For services: number of always-running instances"`
	// RestartDelay is a pointer so an explicit `restart_delay = "0s"` (restart
	// instantly, no delay) is distinguishable from an omitted key (nil, inherits
	// the built-in default). Service-only; always resolved to non-nil by the
	// config loader's defaulting pass.
	RestartDelay   *time.Duration `toml:"-"                        json:"restartDelay,omitempty" doc:"For services: base delay before each restart, in nanoseconds; 0 means restart instantly"`
	RestartBackoff BackoffCurve   `toml:"restart_backoff,omitempty" json:"restartBackoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive restarts"`
	// HealthyAfter is the uptime an instance must reach to count as healthy.
	// Reaching it both resets the restart-backoff counter and clears the
	// failed-start streak; fast failures below it accrue toward RestartAttempts.
	// Service-only. A pointer so an explicit `healthy_after = "0s"` (healthy the
	// instant it starts) is distinguishable from an omitted key (nil, inherits
	// [defaults] then the built-in default). Always resolved to non-nil by the
	// config loader's defaulting pass.
	HealthyAfter *time.Duration `toml:"-" json:"healthyAfter,omitempty" doc:"For services: an instance that runs at least this long counts as healthy — resets the restart counter and clears the failed-start streak; fast exits below it count toward restart_attempts, in nanoseconds; 0 means healthy immediately on start"`
	// RestartAttempts is the number of consecutive fast failures a service
	// instance is allowed before the supervisor marks it FATAL. Service-only
	// (tasks re-run via retry_*). A pointer so an explicit `restart_attempts = 0`
	// (give up on the very first failure) is distinguishable from an omitted key
	// (nil, inherits [defaults] then the built-in default). Resolved to non-nil
	// for services by the config loader's defaulting pass.
	RestartAttempts *int `toml:"-" json:"restartAttempts,omitempty" doc:"For services: consecutive fast failures tolerated before the instance is marked FATAL; 0 means give up after the very first failure"`
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
	DependsOn []string `toml:"-" json:"dependsOn,omitempty" doc:"For services: service names that must be healthy before this one starts at boot — boot ordering only, not a workflow DAG"`

	RetryAttempts int `toml:"retry_attempts,omitempty" json:"retryAttempts,omitempty"`
	// RetryDelay is a pointer so an explicit `retry_delay = "0s"` (retry with no
	// delay) is distinguishable from an omitted key (nil, falls back to
	// config.DefaultRetryDelay in ComputeRetryDelay). Task-only.
	RetryDelay   *time.Duration `toml:"-"                        json:"retryDelay,omitempty" doc:"Base delay before each retry, in nanoseconds; 0 retries with no delay"`
	RetryBackoff BackoffCurve   `toml:"retry_backoff,omitempty"  json:"retryBackoff,omitempty" enum:"constant,linear,exponential" doc:"Backoff curve between consecutive retries"`

	LogMaxSize int64  `toml:"-"                     json:"logMaxSize,omitempty" doc:"Per-run log size cap in bytes"`
	LogOnFull  string `toml:"log_on_full,omitempty" json:"logOnFull,omitempty" enum:"drop_new,drop_old,kill" doc:"What to do when log output exceeds log_max_size"`

	KeepRuns *int          `toml:"keep_runs,omitempty" json:"keepRuns,omitempty" doc:"Row-count retention cap; 0 keeps no completed runs, omitted inherits the [defaults] value (or no cap)"`
	KeepFor  time.Duration `toml:"-"                   json:"keepFor,omitempty" doc:"Retention window in nanoseconds; 0 means no cap was configured"`

	Env     map[string]string `toml:"env,omitempty"      json:"env,omitempty"      doc:"Environment variables overlaid on the task's process env. Values are visible to authenticated operators in the API/UI; env_file values merge in beneath the inline entries."`
	EnvFile string            `toml:"env_file,omitempty" json:"envFile,omitempty" doc:"Path to a dotenv file whose KEY=VALUE pairs merge into env (inline entries win). Values are visible in the API/UI like inline env."`
	// Secrets holds [tasks.*.secrets] plus secrets_file-derived pairs. Hidden
	// from JSON/TOML so values never leak to API/UI/cloud serialization.
	Secrets     map[string]string `toml:"-" json:"-"`
	SecretsFile string            `toml:"secrets_file,omitempty" json:"secretsFile,omitempty" doc:"Path to a dotenv file whose KEY=VALUE pairs are injected into the task's process env. The path is visible in the API/UI; keys and values are not."`

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
	SourceFile string `toml:"-" json:"sourceFile,omitempty" doc:"Absolute path of the crontab or staging file this task's definition was read from; empty for hand-authored TOML"`
	// HeldBy records that something other than RunWisp owns this task's schedule,
	// so the scheduler must not register it. Derived at config load from the
	// machine's state — currently only "a live cron daemon reads this crontab
	// itself" — and re-derived every load, so retiring cron and reloading clears
	// it. Never a TOML key: a hold is not something the operator configures.
	//
	// Unlike Source/SourceFile, this is NOT masked by config.sameDefinition: it
	// changes what fires, so a flip has to reach the reconciler as a schedule
	// change and get the cron entry registered (or dropped). Masking it would turn
	// "held, and visibly so" into "silently never runs".
	HeldBy HoldReason `toml:"-" json:"heldBy,omitempty" enum:"cron" doc:"Why this task is loaded but not on the scheduler: 'cron' means a live system cron daemon still reads the crontab it came from and is running it, so RunWisp stands down. Manual triggers still work. Empty means RunWisp owns the schedule."`

	// WorkingDir is resolved to an absolute path at config load (relative to
	// the runwisp.toml directory). Empty inherits the daemon's working dir.
	//
	// One case stays literal: a `~` on a task that also sets RunUser means that
	// user's home, which the executor resolves per run from the credential it
	// looked up. See config.homeIsTheRunUsers and executor.resolveWorkingDir.
	WorkingDir string `toml:"-" json:"workingDir,omitempty" doc:"Resolved working directory for the task's process; empty inherits the daemon's working directory. A literal \"~\" means the run-as user's home, resolved at run time"`
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
	EnvBase EnvBase `toml:"-" json:"envBase,omitempty" doc:"What the run's environment starts from: 'inherit' (the daemon's, the default) or 'clean' (PATH, SHELL, HOME, USER/LOGNAME only, as crond gives a job)"`
	// RunUser drops the run's process to another OS user (and optionally group)
	// in `user` or `user:group` form; names or numeric ids are accepted on either
	// side. Empty runs as the daemon's own uid/gid. Switching users needs the
	// daemon running as root. Resolved at run time, not config load — the target
	// account may not exist when the config is validated. Rejected on
	// compose-backed tasks (the container runtime owns the container's user).
	RunUser string `toml:"-" json:"user,omitempty" doc:"Run the process as this OS user, in 'user' or 'user:group' form (name or numeric id). Empty runs as the daemon's user; switching users needs the daemon running as root."`
	// FailureReasons and FailureExitRanges are the task's resolved failure
	// classification, parsed from the `failures` TOML tokens by config's parse
	// step (ParseFailures). Together they answer "does a terminal run count as a
	// failure?" for stats, UI attention, and notifications — never for retry (see
	// runtime/retry.IsFailedExecution). A nil FailureReasons map means "not configured"
	// (a Task built outside the config loader); IsFailureReason then falls back to
	// the built-in default set. Config-internal — never serialized to API/UI/cloud.
	FailureReasons    map[EndReason]struct{} `toml:"-" json:"-"`
	FailureExitRanges [][2]int               `toml:"-" json:"-"`

	// FailureSpec is the task's parsed but not-yet-resolved `failures` list,
	// carried from config's parse step to ApplyDefaults, which resolves it
	// against the inherited [defaults] matcher into FailureReasons/FailureExitRanges
	// and clears this back to nil. Config-load-only: nil on any task the loader
	// has finished with (and on Tasks built outside the loader).
	FailureSpec *FailureSpec `toml:"-" json:"-"`

	// Ephemeral marks a task the daemon registered at runtime for a single
	// cloud-dispatched inline execution (never from TOML, never in the task
	// registry). The run manager reaps such a task — and its queue-drain
	// goroutine — once its last run retires with nothing queued, since reconcile
	// (which only ever sees registry/TOML tasks) has no path to remove it.
	// Runtime-only: never serialized to API/UI/cloud/TOML.
	Ephemeral bool `toml:"-" json:"-"`

	// CloudDeclared marks a service the control plane created at runtime via
	// service:apply (never from TOML, never in the config registry). Only such
	// a service may be torn down by service:remove: a TOML-defined
	// [services.*] entry is owned by disk. Deliberately distinct from
	// Ephemeral: that flag hooks the run-manager's one-shot reaper, which would
	// wrongly delete a cloud service the moment it's idle (stopped) rather than
	// only on an explicit remove. Runtime-only: never serialized to
	// API/UI/cloud/TOML.
	CloudDeclared bool `toml:"-" json:"-"`
}

// Held reports whether something other than RunWisp owns this task's firing.
// Checked directly (rather than via Schedulable) by the boot paths that fire a
// task without consulting a clock at all: a crontab's `@reboot` line becomes
// run_on_start with no cron, and cron fires it too.
func (t *Task) Held() bool { return t.HeldBy != HeldByNothing }

// GracefulStopValue returns the configured stop-signal-to-SIGKILL window, or 0
// (kill immediately) when unset. Config-loaded tasks always have it resolved by
// ApplyDefaults; nil only occurs for tasks built outside config.Load.
func (t *Task) GracefulStopValue() time.Duration {
	if t.GracefulStop == nil {
		return 0
	}
	return *t.GracefulStop
}

// DefaultCatchUp is the built-in catch_up value applied when the key is omitted:
// re-run only the most recent missed cron tick after downtime.
const DefaultCatchUp = 1

// CatchUpValue returns the maximum number of missed cron ticks to re-run, falling
// back to DefaultCatchUp when unset. Config-loaded tasks always have it resolved
// by ApplyDefaults; nil only occurs for tasks built outside config.Load.
func (t *Task) CatchUpValue() int {
	if t.CatchUp == nil {
		return DefaultCatchUp
	}
	return *t.CatchUp
}

// RetryDelayValue returns the configured base retry delay, or 0 when unset. Note
// the 5s builtin fallback lives in retry.ComputeRetryDelay, which reads the
// pointer directly so an explicit "0s" stays 0.
func (t *Task) RetryDelayValue() time.Duration {
	if t.RetryDelay == nil {
		return 0
	}
	return *t.RetryDelay
}

// Schedulable reports whether the scheduler should fire this task on a clock.
// The one predicate every clock-driven site consults, so "has no schedule" and
// "something else owns the schedule" can never drift apart: a task that is not
// schedulable gets no cron entry, no jitter plan, and no missed-tick accounting —
// while staying fully visible and manually triggerable.
func (t *Task) Schedulable() bool { return t.Cron != "" && !t.Held() }

// Triggerable reports whether this task can be started via the API/UI/CLI
// trigger path. A service is never ad-hoc runnable this way regardless of
// ManualTrigger; see ManuallyControllable for what ManualTrigger gates on a
// service.
func (t *Task) Triggerable() bool { return !t.Kind.IsService() && t.ManualTrigger }

// ManuallyControllable reports whether a service can be stopped, restarted,
// or started outside its restart policy, via the Web UI, TUI, CLI, REST API,
// or the cloud control plane. false locks it to hands-off supervision: only a
// runwisp.toml edit + reload can change its running state. Meaningless on a
// task; use Triggerable there instead.
func (t *Task) ManuallyControllable() bool { return t.ManualTrigger }

// IsFailureReason reports whether a terminal run ending with the given reason
// and exit code counts as a failure under this task's `failures` policy. It is
// the single source of truth for failure classification: stats, UI attention,
// and notifications all resolve through it (and the persisted run.IsFailure bit
// it produces). It never gates retry — that is runtime/retry.IsFailedExecution.
//
// A nil FailureReasons map (a Task literal built outside the config loader —
// tests, ad-hoc dispatch) falls back to the built-in default set. Exit ranges
// only apply to the `failed` reason (a run that actually exited); a non-`failed`
// reason with an incidental exit code (a SIGTERM'd `stopped` run) is classified
// purely by its reason.
func (t *Task) IsFailureReason(reason EndReason, exitCode int) bool {
	if reason == ReasonSuccess {
		return false
	}
	reasons := t.FailureReasons
	if reasons == nil {
		reasons = defaultFailureReasons
	}
	if _, ok := reasons[reason]; ok {
		return true
	}
	if reason == ReasonFailed {
		for _, r := range t.FailureExitRanges {
			if exitCode >= r[0] && exitCode <= r[1] {
				return true
			}
		}
	}
	return false
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
	PolicyQueue ConcurrencyPolicy = "queue"
	PolicySkip  ConcurrencyPolicy = "skip"
	PolicyKill  ConcurrencyPolicy = "kill"
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

// HoldReason names why a task is loaded and visible but deliberately not on the
// scheduler. Derived at config load like TaskSource, never a TOML key: the
// operator does not ask for a hold, the machine's state produces one.
//
// A hold withholds *automatic firing only*. A held task still appears in the task
// list with its schedule, and `runwisp run` / the API trigger still run it — the
// point of a migration is to be able to check a job works under RunWisp before
// handing the schedule over.
type HoldReason string

const (
	// HeldByNothing is the zero value: this task is the scheduler's to fire.
	HeldByNothing HoldReason = ""
	// HeldByCron is a task read from a crontab that a live system cron daemon is
	// still reading itself. Both schedulers firing the same job is invisible
	// until a non-idempotent one runs twice, so RunWisp stands down and says so
	// rather than racing cron for it. Cleared by retiring cron — `runwisp
	// takeover`, or stopping cron and reloading.
	HeldByCron HoldReason = "cron"
)

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

// BackoffCurve is the shape of delay growth between consecutive restarts
// (restart_backoff) or retries (retry_backoff).
type BackoffCurve string

const (
	BackoffConstant    BackoffCurve = "constant"
	BackoffLinear      BackoffCurve = "linear"
	BackoffExponential BackoffCurve = "exponential"
)

// Log overflow behavior when task output exceeds log_max_size.
const (
	LogOverflowDropNew = "drop_new" // stop writing; keep older output (task keeps running)
	LogOverflowDropOld = "drop_old" // rotate: keep recent output (task keeps running)
	LogOverflowKill    = "kill"     // kill the task
)

// DaemonInfo holds static identity/config data exposed via /api/daemon.
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
	Version          string    `json:"version"`
	Fingerprint      string    `json:"fingerprint"`
	Port             int       `json:"port"`
	ExternalURL      string    `json:"externalUrl"`
	CloudEnabled     bool      `json:"cloudEnabled"`
	SchedulingActive bool      `json:"schedulingActive"`
	ServiceManaged   bool      `json:"serviceManaged"`
	AuthDisabled     bool      `json:"authDisabled"`
	ConfigLoadedAt   time.Time `json:"configLoadedAt"`
	ConfigStale      bool      `json:"configStale"`
	ConfigWarnings   []string  `json:"configWarnings,omitempty" doc:"Non-fatal findings in the live config, e.g. crontab jobs include_cron could not schedule. Re-derived per request, so it tracks reloads."`
	ResolvedTimezone string    `json:"resolvedTimezone"`
	TimezoneSource   string    `json:"timezoneSource" enum:"config,system"`
	Tasks            []Task    `json:"tasks"`
	Capabilities     []CapInfo `json:"capabilities"`
	// UpdateAvailable is true when the background update check found a newer
	// published release than this build. Re-derived per request from the live
	// checker; false when the check is disabled, offline, or up to date.
	UpdateAvailable bool `json:"updateAvailable"`
	// LatestVersion is the newest release the update check has seen (e.g.
	// "v0.3.0"), or empty before the first successful check, when the check is
	// disabled, or when already up to date. Always present so the shape is stable.
	LatestVersion string `json:"latestVersion"`
}

// InstanceInfo is the local-only identity of a running daemon, returned by
// GET /api/daemon/identity. It lets a second `runwisp` launching against an
// already-occupied port discover which daemon holds it — and where that
// daemon's datadir/config/socket live — so it can offer to connect or stop it.
// The endpoint is reachable only over the Unix socket or a loopback TCP peer,
// so these filesystem paths never reach the network.
type InstanceInfo struct {
	App         string `json:"app" doc:"Always \"runwisp\"; lets a caller confirm the port-holder is a RunWisp daemon."`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Pid         int    `json:"pid"`
	DataDir     string `json:"dataDir"`
	ConfigPath  string `json:"configPath"`
	SocketPath  string `json:"socketPath"`
}

// TaskComposeRef identifies the compose file and service backing a task.
// Used by the UI to render the "compose" provenance badge.
type TaskComposeRef struct {
	File        string `json:"file"`
	Service     string `json:"service,omitempty"`
	ProjectName string `json:"projectName"`
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
