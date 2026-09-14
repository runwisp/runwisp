// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/proxycidr"
)

// tomlConfig is the over-the-wire config shape used only during TOML decoding.
//
// Compose is decoded as a free-form map: each [compose.<alias>] block mixes
// reserved scalar keys (file, mode, include, …) with per-service override
// sub-tables, so we destructure the alias map in internal/config/compose.go
// rather than via direct struct binding. See parseComposeBlock there.
type tomlConfig struct {
	Daemon    daemonWire                `toml:"daemon,omitempty"`
	Storage   storageWire               `toml:"storage,omitempty"`
	Defaults  defaultsWire              `toml:"defaults,omitempty"`
	Scheduler schedulerWire             `toml:"scheduler,omitempty"`
	Tasks     map[string]*taskWire      `toml:"tasks,omitempty"`
	Services  map[string]*serviceWire   `toml:"services,omitempty"`
	Compose   map[string]map[string]any `toml:"compose,omitempty"`
	Notify    notifyWire                `toml:"notify,omitempty"`

	Notifiers map[string]*notifierWire `toml:"notifiers,omitempty"`
	Routes    []routeWire              `toml:"route,omitempty"`
}

// unitOverrideWire holds the "identity and lifecycle" TOML keys accepted on
// every executable unit: [tasks.*], [services.*], and per-service compose
// overrides. Embedded anonymously so go-toml decodes its fields as if they
// were declared on the outer struct.
type unitOverrideWire struct {
	Group       string `toml:"group,omitempty"`
	Description string `toml:"description,omitempty"`

	// ManualTrigger is valid on every unit: it gates manual run-triggering on
	// a task (model.Task.Triggerable) and manual stop/restart/start on a
	// service (model.Task.ManuallyControllable). OnOverlap decodes here too,
	// but purely so a service (or compose override) can reject it with a
	// pointed message instead of an opaque undecoded-key error: it's a
	// task-only concept (a service's copy count is `instances`, and it never
	// runs a second overlapping instance).
	ManualTrigger *bool                   `toml:"manual_trigger,omitempty"`
	OnOverlap     model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`

	Timeout      string `toml:"timeout,omitempty"`
	GracefulStop string `toml:"graceful_stop,omitempty"`
	StopSignal   string `toml:"stop_signal,omitempty"`

	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`

	KeepRuns *int   `toml:"keep_runs,omitempty"`
	KeepFor  string `toml:"keep_for,omitempty"`

	Env         map[string]string `toml:"env,omitempty"`
	EnvFile     string            `toml:"env_file,omitempty"`
	Secrets     map[string]string `toml:"secrets,omitempty"`
	SecretsFile string            `toml:"secrets_file,omitempty"`

	// Notify lists the notifier IDs to page when this unit's `failures` policy
	// classifies a run as a failure. Each entry is a notifier id, optionally with
	// an inline target override ("slack:#ops"). Non-failure outcomes (a success
	// ping, a timeout-only escalation) are routed with an explicit [[route]].
	Notify []string `toml:"notify,omitempty"`
}

// taskServiceWireCore holds the TOML keys shared by [tasks.*] and [services.*]
// entries specifically — the process-execution surface a compose override
// never gets, since its backend is fixed by the parent [compose.*] block. It
// is embedded anonymously in taskWire and serviceWire so go-toml decodes its
// fields as if they were declared on the outer struct.
type taskServiceWireCore struct {
	unitOverrideWire

	WorkingDir string `toml:"working_dir,omitempty"`
	Shell      string `toml:"shell,omitempty"`
	Umask      string `toml:"umask,omitempty"`
	EnvBase    string `toml:"env_base,omitempty"`
	User       string `toml:"user,omitempty"`

	// Run is exempt from ${...} substitution (expand:"-"): the shell expands
	// $VAR / ${VAR} at runtime with the full process env, secrets included.
	Run string `toml:"run,omitempty" expand:"-"`

	// ComposeFile / ComposeService route the task through ComposeBackend
	// instead of ShellBackend. ComposeMode picks what that means: "exec" runs
	// Run inside the service's already-running container, "run" starts a fresh
	// one. Empty resolves per Run's presence — see resolveComposeMode.
	ComposeFile    string `toml:"compose_file,omitempty"`
	ComposeService string `toml:"compose_service,omitempty"`
	ComposeMode    string `toml:"compose_mode,omitempty"`

	// Failures declares which outcomes count as a failure for this task: a list of
	// EndReason names and/or exit-code tokens ("42", "1-23"). nil means "unset"
	// (inherit [defaults], then the built-in default) — distinct from an explicit
	// empty list, which means "nothing is a failure". Parsed and validated by
	// ApplyDefaults into model.Task.FailureReasons / FailureExitRanges.
	Failures []string `toml:"failures,omitempty"`

	// Params declares per-execution inputs. Carried on the shared core so the
	// key decodes on [services.*] into a friendly rejection (services are never
	// manually triggered) rather than an undecoded-key error.
	Params []paramWire `toml:"params,omitempty"`

	// DependsOn decodes here (not separately on taskWire and serviceWire) so
	// [tasks.*] can reject it with a friendly message alongside
	// manual_trigger/on_overlap instead of duplicating an identical field on
	// both structs. Only [services.*] gives it meaning; a slice needs no
	// pointer trick since nil already means "unset".
	DependsOn []string `toml:"depends_on,omitempty"`
}

// serviceSupervisionWire holds the restart/instance-supervision TOML keys
// shared by [services.*] entries and their per-service compose overrides.
// Embedded anonymously so go-toml decodes its fields as if declared on the
// outer struct.
type serviceSupervisionWire struct {
	// Restart defaults to "always" (a service that just exits should come back);
	// an operator can narrow it to "on_failure"/"never", matching what a
	// compose-imported service's per-service override already allows.
	Restart        model.RestartPolicy `toml:"restart,omitempty"`
	RestartDelay   string              `toml:"restart_delay,omitempty"`
	RestartBackoff model.BackoffCurve  `toml:"restart_backoff,omitempty"`
	HealthyAfter   string              `toml:"healthy_after,omitempty"`

	// RestartAttempts is a pointer so an explicit `restart_attempts = 0` (give
	// up on the very first failure) is distinguishable from an omitted key.
	RestartAttempts *int `toml:"restart_attempts,omitempty"`

	Priority int `toml:"priority,omitempty"`
	// Autostart is a pointer so an omitted key (nil → default true) is
	// distinguishable from an explicit `autostart = false`.
	Autostart *bool `toml:"autostart,omitempty"`
}

// paramWire is one inline table in [tasks.*.params]. Exactly one identity
// keyword (env/arg/option/flag) names the kind and canonical key. Default is
// `any` so a TOML scalar (string / integer / float / bool) decodes; it is
// canonicalised to a string when mapped to model.TaskParam.
type paramWire struct {
	Env    string `toml:"env,omitempty"`
	Arg    string `toml:"arg,omitempty"`
	Option string `toml:"option,omitempty"`
	Flag   string `toml:"flag,omitempty"`

	// expand:"-" — the variable expander can't write back through an `any`
	// (a string held in an interface isn't settable via reflect). Param
	// defaults are literals; ${VAR} substitution does not apply to them.
	Default     any      `toml:"default,omitempty" expand:"-"`
	Required    bool     `toml:"required,omitempty"`
	Type        string   `toml:"type,omitempty"`
	Choices     []string `toml:"choices,omitempty"`
	AllowCustom bool     `toml:"allow_custom,omitempty"`
	Description string   `toml:"description,omitempty"`
}

// toTaskParams maps the wire param list to model.TaskParam, deriving Kind/Key
// from the single identity keyword and canonicalising the default scalar. It
// rejects entries that do not set exactly one identity keyword; the remaining
// semantic rules live in validateTaskParams.
func toTaskParams(params []paramWire, taskName string) ([]model.TaskParam, error) {
	if len(params) == 0 {
		return nil, nil
	}
	out := make([]model.TaskParam, 0, len(params))
	for i, w := range params {
		kind, key, err := w.identity()
		if err != nil {
			return nil, fmt.Errorf("invalid params[%d] for task %q: %w", i, taskName, err)
		}
		def, err := canonicalizeParamDefault(w.Default)
		if err != nil {
			return nil, fmt.Errorf("invalid params[%d] (%s) for task %q: %w", i, key, taskName, err)
		}
		// A flag default canonicalises to exactly "true"/"false" so the single
		// equality check every consumer makes (resolveFlagValue, the TUI form,
		// the web form) agrees. Without this a default of "1"/"TRUE"/1 would
		// read as "off" and persist a non-boolean string.
		if kind == model.ParamFlag && def != nil {
			b, perr := strconv.ParseBool(*def)
			if perr != nil {
				return nil, fmt.Errorf("invalid params[%d] (%s) for task %q: flag default %q must be a boolean", i, key, taskName, *def)
			}
			canon := strconv.FormatBool(b)
			def = &canon
		}
		out = append(out, model.TaskParam{
			Kind:        kind,
			Key:         key,
			Type:        w.Type,
			Default:     def,
			Required:    w.Required,
			Choices:     w.Choices,
			AllowCustom: w.AllowCustom,
			Description: w.Description,
		})
	}
	return out, nil
}

// identity returns the param's kind and canonical key, requiring exactly one of
// env/arg/option/flag to be set.
func (w *paramWire) identity() (model.ParamKind, string, error) {
	type identity struct {
		kind model.ParamKind
		key  string
	}
	candidates := []identity{
		{model.ParamEnv, w.Env},
		{model.ParamArg, w.Arg},
		{model.ParamOption, w.Option},
		{model.ParamFlag, w.Flag},
	}
	var set identity
	n := 0
	for _, c := range candidates {
		if c.key != "" {
			set = c
			n++
		}
	}
	if n != 1 {
		return "", "", fmt.Errorf("each param must set exactly one of env/arg/option/flag")
	}
	return set.kind, set.key, nil
}

// canonicalizeParamDefault renders a TOML default scalar as the canonical
// string stored on model.TaskParam. nil means "no default declared".
func canonicalizeParamDefault(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case bool:
		s = strconv.FormatBool(t)
	case int64:
		s = strconv.FormatInt(t, 10)
	case float64:
		// 'f' (not 'g') so integer-valued and large floats render without
		// exponent notation — a number default reaches the program as the plain
		// digits the operator wrote (1e21 → 1000…000, 1.0 → "1"), not "1e+21".
		s = strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return nil, fmt.Errorf("default must be a string, number, or bool")
	}
	return &s, nil
}

// toTaskCore parses the shared wire fields into a model.Task skeleton with
// name/kind stamped and the compose backend resolved. Callers layer their
// task-only / service-only fields on top. label ("task" or "service") names
// the entry kind in error messages.
func (w *taskServiceWireCore) toTaskCore(name, label string, kind model.TaskKind) (model.Task, error) {
	manualTrigger := true
	if w.ManualTrigger != nil {
		manualTrigger = *w.ManualTrigger
	}
	timeout, err := parseDuration(w.Timeout)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid timeout for task %q: %w", name, err)
	}
	gracefulStop, err := parseDuration(w.GracefulStop)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid graceful_stop for task %q: %w", name, err)
	}
	keepFor, err := parseKeepFor(w.KeepFor)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid keep_for for task %q: %w", name, err)
	}
	keepRuns, err := parseKeepRuns(w.KeepRuns)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid keep_runs for task %q: %w", name, err)
	}
	logMaxSize, err := parseLogMaxSize(w.LogMaxSize)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid log_max_size for task %q: %w", name, err)
	}
	umask, err := parseUmask(w.Umask)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid umask for %s %q: %w", label, name, err)
	}
	envBase, err := parseEnvBase(w.EnvBase)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid env_base for %s %q: %w", label, name, err)
	}
	// A nil Failures leaves the spec unset so ApplyDefaults inherits [defaults];
	// a present list is parsed now and resolved against the inherited matcher at
	// ApplyDefaults time (a delta needs that base; an absolute list replaces it).
	var failureSpec *model.FailureSpec
	if w.Failures != nil {
		failureSpec, err = model.ParseFailures(w.Failures)
		if err != nil {
			return model.Task{}, fmt.Errorf("invalid %s %q: %w", label, name, err)
		}
	}
	task := model.Task{
		Name:          name,
		Kind:          kind,
		Group:         w.Group,
		Description:   w.Description,
		ManualTrigger: manualTrigger,
		OnOverlap:     w.OnOverlap,
		Timeout:       timeout,
		GracefulStop:  gracefulStop,
		StopSignal:    w.StopSignal,
		LogMaxSize:    logMaxSize,
		LogOnFull:     w.LogOnFull,
		KeepRuns:      keepRuns,
		KeepFor:       keepFor,
		WorkingDir:    w.WorkingDir,
		Shell:         w.Shell,
		Umask:         umask,
		EnvBase:       envBase,
		RunUser:       w.User,
		FailureSpec:   failureSpec,
		Run:           w.Run,
		Env:           w.Env,
		EnvFile:       w.EnvFile,
		Secrets:       w.Secrets,
		SecretsFile:   w.SecretsFile,
	}
	params, err := toTaskParams(w.Params, name)
	if err != nil {
		return model.Task{}, err
	}
	task.Parameters = params
	if err := w.applyComposeBackend(&task, name, label); err != nil {
		return model.Task{}, err
	}
	return task, nil
}

// applyComposeBackend routes the task through the compose backend when
// compose_file is set, rejecting host-process-only keys (shell/umask/user) that
// have no meaning for a `docker compose` run. compose_service without
// compose_file is rejected. Both checks run here, where the raw explicit values
// are still visible (applyInheritedDefaults later erases the explicit-vs-default
// signal by filling in shell = /bin/sh).
func (w *taskServiceWireCore) applyComposeBackend(task *model.Task, name, label string) error {
	if w.ComposeFile == "" {
		if w.ComposeService != "" {
			return fmt.Errorf("%s %q sets compose_service without compose_file", label, name)
		}
		return nil
	}
	if w.Shell != "" {
		return fmt.Errorf("shell is not supported on compose-backed %s %q; it applies only to host shell runs", label, name)
	}
	if w.Umask != "" {
		return fmt.Errorf("umask is not supported on compose-backed %s %q; it applies only to host shell runs", label, name)
	}
	if w.User != "" {
		return fmt.Errorf("user is not supported on compose-backed %s %q; the container runtime owns the container's user", label, name)
	}
	if w.EnvBase != "" {
		return fmt.Errorf("env_base is not supported on compose-backed %s %q; a container never inherits the daemon's environment, so there is no base to choose", label, name)
	}
	svc := w.ComposeService
	if svc == "" {
		svc = name
	}
	mode, err := w.resolveComposeMode(name, label)
	if err != nil {
		return err
	}
	command := ""
	// Exec mode targets a container someone else created, so the project name
	// has to be the one that container actually runs under — and RunWisp cannot
	// know it. Using the task name (fine for `run`, which creates the container
	// in its own namespace) would make `compose exec` search a project that does
	// not exist and report the service as not running. Leaving it empty lets
	// compose resolve the project exactly as a hand-typed `docker compose -f …
	// exec` would: from the file's directory, its top-level `name:`, or
	// COMPOSE_PROJECT_NAME.
	projectName := name
	if mode == model.ComposeModeExec {
		command = w.Run
		projectName = ""
	}
	task.ExecutionDef = &model.ComposeExecution{
		File:        w.ComposeFile,
		ProjectName: projectName,
		Service:     svc,
		Mode:        mode,
		Command:     command,
	}
	task.Compose = &model.TaskComposeRef{
		File:        w.ComposeFile,
		Service:     svc,
		ProjectName: name,
	}
	return nil
}

// resolveComposeMode decides between exec-into-the-running-container and
// start-a-fresh-one for a compose-backed unit.
//
// The default is conditional, and deliberately so. `run` is what disambiguates:
// supply a command and exec is what you almost always meant — the container is
// already up, and starting a second copy of the image to run one command in it
// is the surprising reading. Supply no command and exec is impossible (there is
// nothing to execute), so the only coherent behaviour is a fresh container
// running the service's own compose-declared command.
//
// Both are reachable explicitly, so "fresh container, my command" stays
// expressible via compose_mode = "run" — that combination used to be a hard
// error, which is why relaxing it needed a deliberate decision rather than a
// silent precedence rule.
func (w *taskServiceWireCore) resolveComposeMode(name, label string) (string, error) {
	hasRun := strings.TrimSpace(w.Run) != ""

	switch w.ComposeMode {
	case "":
		if hasRun {
			return model.ComposeModeExec, nil
		}
		return model.ComposeModeRun, nil
	case model.ComposeModeExec:
		if !hasRun {
			return "", fmt.Errorf(
				"%s %q sets compose_mode = %q but no `run` command; exec needs a command to run inside the container",
				label, name, model.ComposeModeExec)
		}
		return model.ComposeModeExec, nil
	case model.ComposeModeRun:
		return model.ComposeModeRun, nil
	default:
		return "", fmt.Errorf(
			"%s %q has invalid compose_mode %q; valid values are %q and %q",
			label, name, w.ComposeMode, model.ComposeModeExec, model.ComposeModeRun)
	}
}

// taskWire is the over-the-wire task shape used only during TOML decoding.
// It exists so manual_trigger can be distinguished between "absent" (nil, default true)
// and "explicitly false" (&false).
type taskWire struct {
	taskServiceWireCore

	Cron           string                `toml:"cron,omitempty"`
	Timezone       string                `toml:"timezone,omitempty"`
	Jitter         string                `toml:"jitter,omitempty"`
	CatchUp        model.MissedRunPolicy `toml:"catch_up,omitempty"`
	MaxCatchUpRuns int                   `toml:"max_catch_up_runs,omitempty"`
	RunOnStart     bool                  `toml:"run_on_start,omitempty"`

	MaxConcurrent int `toml:"max_concurrent,omitempty"`
	MaxQueued     int `toml:"max_queued,omitempty"`

	// Restart and RestartAttempts are rejected on [tasks.*] (services-only:
	// restart is ongoing supervision, a task re-runs via retry_*). They decode
	// here only so collectTaskNames can reject them with a pointed message
	// instead of an opaque undecoded-key error.
	Restart         model.RestartPolicy `toml:"restart,omitempty"`
	RestartAttempts *int                `toml:"restart_attempts,omitempty"`

	// Instances is rejected on [tasks.*]; carried as a pointer so the validator
	// can distinguish "unset" from "explicitly zero".
	Instances *int `toml:"instances,omitempty"`

	RetryAttempts int                `toml:"retry_attempts,omitempty"`
	RetryDelay    string             `toml:"retry_delay,omitempty"`
	RetryBackoff  model.BackoffCurve `toml:"retry_backoff,omitempty"`
}

func (w *taskWire) toTask(name string) (model.Task, error) {
	task, err := w.toTaskCore(name, "task", model.KindTask)
	if err != nil {
		return model.Task{}, err
	}
	retryDelay, err := parseDuration(w.RetryDelay)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid retry_delay for task %q: %w", name, err)
	}
	jitter, err := parseDuration(w.Jitter)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid jitter for task %q: %w", name, err)
	}
	task.Cron = w.Cron
	task.Timezone = w.Timezone
	task.Jitter = jitter
	task.CatchUp = w.CatchUp
	task.MaxCatchUpRuns = w.MaxCatchUpRuns
	task.RunOnStart = w.RunOnStart
	task.MaxConcurrent = w.MaxConcurrent
	task.MaxQueued = w.MaxQueued
	task.RetryAttempts = w.RetryAttempts
	task.RetryDelay = retryDelay
	task.RetryBackoff = w.RetryBackoff
	return task, nil
}

// serviceWire is the over-the-wire shape for [services.*] entries. Cron and
// catch_up are intentionally omitted — services are not cron-driven. Services
// have no max_concurrent or max_queued: instance count is governed by `instances`
// and overlap behaviour by `on_overlap`.
type serviceWire struct {
	taskServiceWireCore
	serviceSupervisionWire

	Instances int `toml:"instances,omitempty"`
}

func (w *serviceWire) toTask(name string) (model.Task, error) {
	if len(w.Params) > 0 {
		return model.Task{}, fmt.Errorf("service %q sets params; params is only valid on [tasks.*] (services are not manually triggered)", name)
	}
	task, err := w.toTaskCore(name, "service", model.KindService)
	if err != nil {
		return model.Task{}, err
	}
	restartDelay, err := parseDurationPtr(w.RestartDelay)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid restart_delay for task %q: %w", name, err)
	}
	healthyAfter, err := parseDurationPtr(w.HealthyAfter)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid healthy_after for task %q: %w", name, err)
	}
	autostart := true
	if w.Autostart != nil {
		autostart = *w.Autostart
	}
	task.Restart = w.Restart
	if task.Restart == "" {
		task.Restart = model.RestartAlways
	}
	task.Instances = w.Instances
	task.RestartDelay = restartDelay
	task.RestartBackoff = w.RestartBackoff
	task.HealthyAfter = healthyAfter
	task.RestartAttempts = w.RestartAttempts
	task.Priority = w.Priority
	task.Autostart = autostart
	task.DependsOn = w.DependsOn
	return task, nil
}

// defaultsWire mirrors [defaults] before parsing.
type defaultsWire struct {
	Timeout      string `toml:"timeout,omitempty"`
	Jitter       string `toml:"jitter,omitempty"`
	Shell        string `toml:"shell,omitempty"`
	StopSignal   string `toml:"stop_signal,omitempty"`
	LogMaxSize   string `toml:"log_max_size,omitempty"`
	LogOnFull    string `toml:"log_on_full,omitempty"`
	KeepRuns     *int   `toml:"keep_runs,omitempty"`
	KeepFor      string `toml:"keep_for,omitempty"`
	HealthyAfter string `toml:"healthy_after,omitempty"`
	// RestartAttempts is a pointer so an explicit `restart_attempts = 0` in
	// [defaults] is distinguishable from an omitted key.
	RestartAttempts *int `toml:"restart_attempts,omitempty"`

	// Failures is the global default failure classification; a task may override
	// it. nil leaves the built-in default set (see model.DefaultFailureTokens).
	Failures []string `toml:"failures,omitempty"`

	Env         map[string]string `toml:"env,omitempty"`
	EnvFile     string            `toml:"env_file,omitempty"`
	Secrets     map[string]string `toml:"secrets,omitempty"`
	SecretsFile string            `toml:"secrets_file,omitempty"`
}

func (w *defaultsWire) toDefaults() (Defaults, error) {
	timeout, err := parseDuration(w.Timeout)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.timeout: %w", err)
	}
	jitter, err := parseDuration(w.Jitter)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.jitter: %w", err)
	}
	keepFor, err := parseKeepFor(w.KeepFor)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.keep_for: %w", err)
	}
	keepRuns, err := parseKeepRuns(w.KeepRuns)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.keep_runs: %w", err)
	}
	logMaxSize, err := parseLogMaxSize(w.LogMaxSize)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.log_max_size: %w", err)
	}
	healthyAfter, err := parseDurationPtr(w.HealthyAfter)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.healthy_after: %w", err)
	}
	// [defaults] always resolves to a concrete failure classification: the
	// built-in default, replaced or delta-adjusted by the operator's `failures`
	// list. Tasks that leave `failures` unset inherit this resolved matcher.
	failureReasons, failureRanges := model.DefaultFailures()
	if w.Failures != nil {
		spec, perr := model.ParseFailures(w.Failures)
		if perr != nil {
			return Defaults{}, fmt.Errorf("invalid defaults.%w", perr)
		}
		failureReasons, failureRanges = spec.Resolve(failureReasons, failureRanges)
	}
	return Defaults{
		Timeout:           timeout,
		Jitter:            jitter,
		Shell:             w.Shell,
		StopSignal:        w.StopSignal,
		LogMaxSize:        logMaxSize,
		LogOnFull:         w.LogOnFull,
		KeepRuns:          keepRuns,
		KeepFor:           keepFor,
		HealthyAfter:      healthyAfter,
		RestartAttempts:   w.RestartAttempts,
		FailureReasons:    failureReasons,
		FailureExitRanges: failureRanges,
		Env:               w.Env,
		EnvFile:           w.EnvFile,
		Secrets:           w.Secrets,
		SecretsFile:       w.SecretsFile,
	}, nil
}

// storageWire mirrors [storage] before parsing.
type storageWire struct {
	MaxSize      string `toml:"max_size,omitempty"`
	MinFreeSpace string `toml:"min_free_space,omitempty"`
}

func (w *storageWire) toStorage() (Storage, error) {
	maxSize, err := parseScopedByteSize("storage.max_size", w.MaxSize)
	if err != nil {
		return Storage{}, err
	}
	minFree, err := parseScopedByteSize("storage.min_free_space", w.MinFreeSpace)
	if err != nil {
		return Storage{}, err
	}
	return Storage{
		MaxSize:      maxSize,
		MinFreeSpace: minFree,
	}, nil
}

// daemonWire mirrors [daemon] before parsing — the duration string for
// shutdown_timeout is parsed at config-load time.
//
// Include and IncludeCron are consumed entirely at load time by loadWithIncludes
// (glob, merge) and are deliberately absent from the Daemon model: they never
// reach the API, UI, or any runtime consumer — the merged task set is the only
// observable result. That absence is what makes editing either one a *reloadable*
// change: checkNonReloadable compares the Daemon structs, so a field there would
// make adding a crontab require a restart. Only the root config may set them;
// either key in an included file is a hard error.
type daemonWire struct {
	AllowCloudDispatch bool     `toml:"allow_cloud_dispatch,omitempty"`
	ShutdownTimeout    string   `toml:"shutdown_timeout,omitempty"`
	ExternalURL        string   `toml:"external_url,omitempty"`
	MetricsEnabled     bool     `toml:"metrics_enabled,omitempty"`
	MetricsListen      string   `toml:"metrics_listen,omitempty"`
	TLS                string   `toml:"tls,omitempty"`
	TLSCert            string   `toml:"tls_cert,omitempty"`
	TLSKey             string   `toml:"tls_key,omitempty"`
	TrustedProxies     []string `toml:"trusted_proxies,omitempty"`
	Include            []string `toml:"include,omitempty"`
	IncludeCron        []string `toml:"include_cron,omitempty"`
}

func (w *daemonWire) toDaemon() (Daemon, error) {
	shutdown, err := parseDuration(w.ShutdownTimeout)
	if err != nil {
		return Daemon{}, fmt.Errorf("invalid daemon.shutdown_timeout: %w", err)
	}
	externalURL, err := parseExternalURL(w.ExternalURL)
	if err != nil {
		return Daemon{}, err
	}
	metricsListen, err := parseMetricsListen(w.MetricsListen)
	if err != nil {
		return Daemon{}, err
	}
	if metricsListen != "" && !w.MetricsEnabled {
		return Daemon{}, fmt.Errorf("invalid daemon.metrics_listen: set without daemon.metrics_enabled = true")
	}
	tlsMode, err := parseTLSMode(w.TLS)
	if err != nil {
		return Daemon{}, err
	}
	trustedProxies, err := parseTrustedProxies(w.TrustedProxies)
	if err != nil {
		return Daemon{}, err
	}
	return Daemon{
		AllowCloudDispatch: w.AllowCloudDispatch,
		ShutdownTimeout:    shutdown,
		ExternalURL:        externalURL,
		MetricsEnabled:     w.MetricsEnabled,
		MetricsListen:      metricsListen,
		TLS:                tlsMode,
		TLSCert:            strings.TrimSpace(w.TLSCert),
		TLSKey:             strings.TrimSpace(w.TLSKey),
		TrustedProxies:     trustedProxies,
	}, nil
}

// parseTrustedProxies validates each [daemon] trusted_proxies entry and returns
// the normalised CIDRs, dropping blanks. Catch-all ranges are rejected here so
// `runwisp validate` catches them before a restart — the same rule the
// RUNWISP_TRUSTED_PROXIES env var enforces at daemon start.
func parseTrustedProxies(entries []string) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		cidr, err := proxycidr.Normalize(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid daemon.trusted_proxies: %w", err)
		}
		if cidr != "" {
			out = append(out, cidr)
		}
	}
	return out, nil
}

// schedulerWire mirrors [scheduler] before parsing.
type schedulerWire struct {
	Timezone string `toml:"timezone,omitempty"`
}

// notifyWire mirrors the [notify] block before parsing. GlobalNotifiers is a
// pointer so we can distinguish "key omitted" (apply built-in default of
// ["inapp"]) from "key set to []" (operator explicitly opted out of the
// in-app safety net).
type notifyWire struct {
	RetryBudget       string    `toml:"retry_budget,omitempty"`
	GlobalNotifiers   *[]string `toml:"global_notifiers,omitempty"`
	KeepNotifications int       `toml:"keep_notifications,omitempty"`
	KeepFor           string    `toml:"keep_for,omitempty"`
	CoalesceWindow    string    `toml:"coalesce_window,omitempty"`
	KeepOccurrences   int       `toml:"keep_occurrences,omitempty"`
	// CoalesceOutbound is *bool so we can distinguish "unset" (default-on)
	// from explicit `coalesce_outbound = false` (the rare opt-out).
	CoalesceOutbound *bool `toml:"coalesce_outbound,omitempty"`
}

// notifierWire is one [notifiers.<id>] block, keyed by its id. Secret-bearing
// values arrive final: operators use ${VAR} / ${file:...} substitution for
// indirection.
type notifierWire struct {
	Type string `toml:"type"`

	WebhookURL string `toml:"webhook_url,omitempty"`
	Channel    string `toml:"channel,omitempty"`

	BotToken  string `toml:"bot_token,omitempty"`
	ChatID    string `toml:"chat_id,omitempty"`
	ParseMode string `toml:"parse_mode,omitempty"`

	Host          string   `toml:"host,omitempty"`
	Port          int      `toml:"port,omitempty"`
	TLSMode       string   `toml:"tls_mode,omitempty"`
	TLSSkipVerify bool     `toml:"tls_skip_verify,omitempty"`
	Username      string   `toml:"username,omitempty"`
	Password      string   `toml:"password,omitempty"`
	From          string   `toml:"from,omitempty"`
	ReplyTo       string   `toml:"reply_to,omitempty"`
	To            []string `toml:"to,omitempty"`
	CC            []string `toml:"cc,omitempty"`
	BCC           []string `toml:"bcc,omitempty"`

	SendmailPath string `toml:"sendmail_path,omitempty"`

	URL     string            `toml:"url,omitempty"`
	Headers map[string]string `toml:"headers,omitempty"`

	TemplatePath string `toml:"template_path,omitempty"`
}

// routeWire is one [[route]] block before validation.
type routeWire struct {
	Match     routeMatchWire `toml:"match"`
	Notifiers []string       `toml:"notifiers"`
}

type routeMatchWire struct {
	Kinds   []string `toml:"kinds,omitempty"`
	Failure bool     `toml:"failure,omitempty"`
	Task    string   `toml:"task,omitempty"`
}
