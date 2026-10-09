// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/proxycidr"
)

// tomlConfig is the over-the-wire config shape used only during TOML decoding.
//
// Compose is decoded as a free-form map: each [compose.<alias>] block mixes
// reserved scalar keys (file, mode, include, …) with per-service override
// sub-tables, so we destructure the alias map in internal/config/compose.go
// rather than via direct struct binding. See parseComposeBlock there.
type tomlConfig struct {
	Daemon   daemonWire                `toml:"daemon,omitempty"`
	Storage  storageWire               `toml:"storage,omitempty"`
	Defaults defaultsWire              `toml:"defaults,omitempty"`
	Tasks    map[string]*taskWire      `toml:"tasks,omitempty"`
	Services map[string]*serviceWire   `toml:"services,omitempty"`
	Compose  map[string]map[string]any `toml:"compose,omitempty"`
	Notify   notifyWire                `toml:"notify,omitempty"`

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
	// service (model.Task.ManuallyControllable).
	ManualTrigger *bool `toml:"manual_trigger,omitempty"`

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
	// ApplyDefaults into model.Task.Failures.
	Failures []string `toml:"failures,omitempty"`

	// HookTokens decodes on [tasks.*] and [services.*] only — never
	// [defaults] or compose overrides, where one shared token would defeat
	// per-unit scoping.
	HookTokens []hookTokenWire `toml:"hook_tokens,omitempty"`
}

// hookTokenWire is one hook_tokens entry, in either form: a bare string (the
// token, granting every action) or a { token, allow } table. go-toml hands a
// string to UnmarshalText and decodes a table field by field, strictly.
type hookTokenWire struct {
	Token string   `toml:"token"`
	Allow []string `toml:"allow,omitempty"`
}

func (h *hookTokenWire) UnmarshalText(text []byte) error {
	h.Token = string(text)
	return nil
}

// toHookTokens maps the wire entries onto the model. An omitted allow keeps
// Allow nil (every action); the values themselves are checked by
// validateTaskHookTokens once the unit's kind is final.
func toHookTokens(ws []hookTokenWire) []model.HookToken {
	if ws == nil {
		return nil
	}
	out := make([]model.HookToken, len(ws))
	for i, w := range ws {
		out[i].Token = w.Token
		if w.Allow != nil {
			out[i].Allow = make([]model.HookAction, len(w.Allow))
			for j, a := range w.Allow {
				out[i].Allow[j] = model.HookAction(a)
			}
		}
	}
	return out
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
	timeout, err := parseDurationPtr(w.Timeout)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid timeout for task %q: %w", name, err)
	}
	gracefulStop, err := parseDurationPtr(w.GracefulStop)
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
		HookTokens:    toHookTokens(w.HookTokens),
	}
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
	// Exec mode targets a container someone else created, under a project name
	// RunWisp cannot know; the task name would make `compose exec` search a
	// project that does not exist. Leaving it empty lets compose resolve the
	// project as a hand-typed `docker compose -f … exec` would: from the file's
	// directory, its top-level `name:`, or COMPOSE_PROJECT_NAME.
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
// The default depends on `run`: with a command, exec into the already-running
// container; without one there is nothing to exec, so start a fresh container
// running the service's own compose-declared command. "Fresh container, my
// command" is available explicitly via compose_mode = "run".
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

// taskWire is the over-the-wire [tasks.*] shape used only during TOML decoding.
type taskWire struct {
	taskServiceWireCore

	// OnOverlap decodes on [tasks.*] only: a service's copy count is
	// `instances`, and it never runs a second overlapping instance (its
	// resolved OnOverlap is always PolicySkip — see applyServiceDefaults).
	OnOverlap model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`

	// Params declares per-execution inputs, valid only on [tasks.*] — services
	// are never manually triggered.
	Params []paramWire `toml:"params,omitempty"`

	Cron     string `toml:"cron,omitempty"`
	Timezone string `toml:"timezone,omitempty"`
	Jitter   string `toml:"jitter,omitempty"`
	// CatchUp is *int so an explicit `catch_up = 0` (skip) is distinguishable from
	// an omitted key (nil, inherits the default).
	CatchUp *int `toml:"catch_up,omitempty"`
	// RunOnStart is a boolean or a mode string ("daemon" / "boot"), so it
	// decodes as any and is resolved by parseRunOnStart. Exempt from ${...}
	// substitution: the expander cannot write through an interface.
	RunOnStart any `toml:"run_on_start,omitempty" expand:"-"`
	// Autostart is a pointer so an omitted key (nil → default true) is
	// distinguishable from an explicit `autostart = false`.
	Autostart *bool `toml:"autostart,omitempty"`

	MaxConcurrent int  `toml:"max_concurrent,omitempty"`
	MaxQueued     *int `toml:"max_queued,omitempty"`

	RetryAttempts int                `toml:"retry_attempts,omitempty"`
	RetryDelay    string             `toml:"retry_delay,omitempty"`
	RetryBackoff  model.BackoffCurve `toml:"retry_backoff,omitempty"`
}

func (w *taskWire) toTask(name string) (model.Task, error) {
	task, err := w.toTaskCore(name, "task", model.KindTask)
	if err != nil {
		return model.Task{}, err
	}
	retryDelay, err := parseDurationPtr(w.RetryDelay)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid retry_delay for task %q: %w", name, err)
	}
	jitter, err := parseDurationPtr(w.Jitter)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid jitter for task %q: %w", name, err)
	}
	params, err := toTaskParams(w.Params, name)
	if err != nil {
		return model.Task{}, err
	}
	runOnStart, err := parseRunOnStart(w.RunOnStart)
	if err != nil {
		return model.Task{}, fmt.Errorf("task %q has invalid run_on_start: %w", name, err)
	}
	task.OnOverlap = w.OnOverlap
	task.Parameters = params
	task.Cron = w.Cron
	task.Timezone = w.Timezone
	task.Jitter = jitter
	task.CatchUp = w.CatchUp
	task.RunOnStart = runOnStart != ""
	task.RunOnStartMode = runOnStart
	task.Autostart = w.Autostart == nil || *w.Autostart
	if err := checkTaskAutostart(&task); err != nil {
		return model.Task{}, err
	}
	task.MaxConcurrent = w.MaxConcurrent
	task.MaxQueued = w.MaxQueued
	task.RetryAttempts = w.RetryAttempts
	task.RetryDelay = retryDelay
	task.RetryBackoff = w.RetryBackoff
	return task, nil
}

// checkTaskAutostart rejects autostart = false where the paused schedule it
// asks for can't exist, could never be resumed, or is undercut by a run at
// every start. It runs at decode time, where autostart is known to come from
// the operator's TOML rather than a zero-valued Task built in code.
func checkTaskAutostart(task *model.Task) error {
	if task.Autostart {
		return nil
	}
	switch {
	case task.Cron == "":
		return fmt.Errorf("task %q sets autostart = false but has no cron; autostart = false only pauses a cron schedule", task.Name)
	case !task.ManualTrigger:
		return fmt.Errorf("task %q sets autostart = false with manual_trigger = false; its paused schedule could never be resumed", task.Name)
	case task.RunOnStart:
		return fmt.Errorf("task %q sets autostart = false with run_on_start; a task that starts paused should not run at start, remove one of them", task.Name)
	}
	return nil
}

// parseRunOnStart resolves the run_on_start value: true is shorthand for
// "daemon", false and absent mean off (empty mode).
func parseRunOnStart(v any) (model.RunOnStartMode, error) {
	switch v := v.(type) {
	case nil:
		return "", nil
	case bool:
		if v {
			return model.RunOnStartDaemon, nil
		}
		return "", nil
	case string:
		if m := model.RunOnStartMode(v); m == model.RunOnStartDaemon || m == model.RunOnStartBoot {
			return m, nil
		}
		return "", fmt.Errorf("got %q; %s", v, runOnStartValid)
	}
	return "", fmt.Errorf("got %v; %s", v, runOnStartValid)
}

const runOnStartValid = `valid values are true, false, "daemon", and "boot"`

// serviceWire is the over-the-wire shape for [services.*] entries. Services are
// not cron-driven, so cron and catch_up are omitted, and they have no
// on_overlap, max_concurrent or max_queued: instance count is governed by
// `instances`.
type serviceWire struct {
	taskServiceWireCore
	serviceSupervisionWire

	Instances int `toml:"instances,omitempty"`

	// DependsOn names other services that must become healthy before this one
	// starts at boot. Valid only on [services.*] — a task has no boot ordering.
	DependsOn []string `toml:"depends_on,omitempty"`

	HealthCheck *healthCheckWire `toml:"health_check,omitempty"`
}

// healthCheckWire mirrors [services.*.health_check]. It declares exactly the
// [tasks.*] keys a probe accepts, under the same names so they read and parse
// the same, and nothing else — strict decoding rejects every other task key.
type healthCheckWire struct {
	// Run is exempt from ${...} substitution, like a task's run.
	Run string `toml:"run,omitempty" expand:"-"`

	Cron     string   `toml:"cron,omitempty"`
	Timezone string   `toml:"timezone,omitempty"`
	Timeout  string   `toml:"timeout,omitempty"`
	Failures []string `toml:"failures,omitempty"`

	// RetryAttempts is a pointer so an explicit `retry_attempts = 0` (unhealthy
	// on the first failed check) is distinguishable from an omitted key, which
	// resolves to DefaultHealthCheckRetryAttempts rather than a task's 0.
	RetryAttempts *int               `toml:"retry_attempts,omitempty"`
	RetryDelay    string             `toml:"retry_delay,omitempty"`
	RetryBackoff  model.BackoffCurve `toml:"retry_backoff,omitempty"`

	WorkingDir  string            `toml:"working_dir,omitempty"`
	Shell       string            `toml:"shell,omitempty"`
	Umask       string            `toml:"umask,omitempty"`
	EnvBase     string            `toml:"env_base,omitempty"`
	User        string            `toml:"user,omitempty"`
	Env         map[string]string `toml:"env,omitempty"`
	EnvFile     string            `toml:"env_file,omitempty"`
	Secrets     map[string]string `toml:"secrets,omitempty"`
	SecretsFile string            `toml:"secrets_file,omitempty"`

	ComposeFile    string `toml:"compose_file,omitempty"`
	ComposeService string `toml:"compose_service,omitempty"`
	ComposeMode    string `toml:"compose_mode,omitempty"`
}

// healthCheckTask builds the service's probe as a task, through the same
// taskWire.toTask every [tasks.*] entry goes through. A host probe inherits the
// service's host exec keys it leaves unset, so the check runs where and as the
// service does; its env/secrets merge over the service's in ApplyDefaults, once
// both sides have their env_file layers resolved. A compose probe (compose_file
// set) inherits nothing, exactly like a compose-backed task.
func (w *serviceWire) healthCheckTask(name string) (*model.Task, error) {
	hc := w.HealthCheck
	if hc == nil {
		return nil, nil
	}
	// A probe checks the service's running container; `run` mode would start a
	// fresh one per check under a project named after the probe.
	if hc.ComposeMode == model.ComposeModeRun {
		return nil, fmt.Errorf("health_check for service %q: compose_mode = %q is not supported; a probe execs into a running container (compose_mode = %q)",
			name, model.ComposeModeRun, model.ComposeModeExec)
	}
	core := taskServiceWireCore{
		unitOverrideWire: unitOverrideWire{
			Timeout:     hc.Timeout,
			Env:         hc.Env,
			EnvFile:     hc.EnvFile,
			Secrets:     hc.Secrets,
			SecretsFile: hc.SecretsFile,
		},
		WorkingDir:     hc.WorkingDir,
		Shell:          hc.Shell,
		Umask:          hc.Umask,
		EnvBase:        hc.EnvBase,
		User:           hc.User,
		Run:            hc.Run,
		ComposeFile:    hc.ComposeFile,
		ComposeService: hc.ComposeService,
		ComposeMode:    hc.ComposeMode,
		Failures:       hc.Failures,
	}
	if hc.ComposeFile == "" {
		core.WorkingDir = cmp.Or(hc.WorkingDir, w.WorkingDir)
		core.Shell = cmp.Or(hc.Shell, w.Shell)
		core.Umask = cmp.Or(hc.Umask, w.Umask)
		core.EnvBase = cmp.Or(hc.EnvBase, w.EnvBase)
		core.User = cmp.Or(hc.User, w.User)
	}
	retryAttempts := DefaultHealthCheckRetryAttempts
	if hc.RetryAttempts != nil {
		retryAttempts = *hc.RetryAttempts
	}
	probe, err := (&taskWire{
		taskServiceWireCore: core,
		Cron:                hc.Cron,
		Timezone:            hc.Timezone,
		RetryAttempts:       retryAttempts,
		RetryDelay:          hc.RetryDelay,
		RetryBackoff:        hc.RetryBackoff,
	}).toTask(name + healthCheckSuffix)
	if err != nil {
		return nil, err
	}
	return &probe, nil
}

func (w *serviceWire) toTask(name string) (model.Task, error) {
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
	healthCheck, err := w.healthCheckTask(name)
	if err != nil {
		return model.Task{}, err
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
	task.Autostart = w.Autostart == nil || *w.Autostart
	task.DependsOn = w.DependsOn
	task.HealthCheck = healthCheck
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
	RestartAttempts *int   `toml:"restart_attempts,omitempty"`
	RestartDelay    string `toml:"restart_delay,omitempty"`
	RestartBackoff  string `toml:"restart_backoff,omitempty"`

	CatchUp      *int   `toml:"catch_up,omitempty"`
	GracefulStop string `toml:"graceful_stop,omitempty"`

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
	restartDelay, err := parseDurationPtr(w.RestartDelay)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.restart_delay: %w", err)
	}
	gracefulStop, err := parseDurationPtr(w.GracefulStop)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.graceful_stop: %w", err)
	}
	// [defaults] always resolves to a concrete failure classification: the
	// built-in default, replaced or delta-adjusted by the operator's `failures`
	// list. Tasks that leave `failures` unset inherit this resolved matcher.
	failures := model.DefaultFailures()
	if w.Failures != nil {
		spec, perr := model.ParseFailures(w.Failures)
		if perr != nil {
			return Defaults{}, fmt.Errorf("invalid defaults.%w", perr)
		}
		failures = spec.Resolve(failures)
	}
	return Defaults{
		Timeout:         timeout,
		Jitter:          jitter,
		Shell:           w.Shell,
		StopSignal:      w.StopSignal,
		LogMaxSize:      logMaxSize,
		LogOnFull:       w.LogOnFull,
		KeepRuns:        keepRuns,
		KeepFor:         keepFor,
		HealthyAfter:    healthyAfter,
		RestartAttempts: w.RestartAttempts,
		RestartDelay:    restartDelay,
		RestartBackoff:  model.BackoffCurve(w.RestartBackoff),
		CatchUp:         w.CatchUp,
		GracefulStop:    gracefulStop,
		Failures:        failures,
		Env:             w.Env,
		EnvFile:         w.EnvFile,
		Secrets:         w.Secrets,
		SecretsFile:     w.SecretsFile,
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
	AllowStationDispatch bool   `toml:"allow_station_dispatch,omitempty"`
	ShutdownTimeout      string `toml:"shutdown_timeout,omitempty"`
	ExternalURL          string `toml:"external_url,omitempty"`
	// CheckUpdates is a pointer so an omitted key (nil → default true) is
	// distinguishable from an explicit `check_updates = false`.
	CheckUpdates *bool `toml:"check_updates,omitempty"`
	// MetricsEnabled is a pointer for the same reason as CheckUpdates: an
	// omitted key (nil → implied by metrics_listen) must be distinguishable
	// from an explicit `metrics_enabled = false`, which is a conflicting
	// intent once metrics_listen is also set (see toDaemon).
	MetricsEnabled *bool    `toml:"metrics_enabled,omitempty"`
	MetricsListen  string   `toml:"metrics_listen,omitempty"`
	TLS            string   `toml:"tls,omitempty"`
	TLSCert        string   `toml:"tls_cert,omitempty"`
	TLSKey         string   `toml:"tls_key,omitempty"`
	TrustedProxies []string `toml:"trusted_proxies,omitempty"`
	Include        []string `toml:"include,omitempty"`
	IncludeCron    []string `toml:"include_cron,omitempty"`
	// Timezone is the daemon-wide IANA zone used to evaluate cron expressions
	// for any task that doesn't pin its own. A reload re-bases the schedules
	// onto a new zone (Scheduler.SetLocation).
	Timezone string `toml:"timezone,omitempty"`
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
	// A dedicated metrics_listen implies metrics are enabled — the endpoint has
	// no other purpose, so there's no reason to make the operator also flip
	// metrics_enabled. An explicit metrics_enabled = false alongside it is a
	// contradiction the operator needs to resolve, not a silent "listen wins".
	if w.MetricsEnabled != nil && !*w.MetricsEnabled && metricsListen != "" {
		return Daemon{}, fmt.Errorf("invalid [daemon]: metrics_enabled = false cannot be combined with metrics_listen; remove metrics_listen, or drop metrics_enabled to let it stay implied")
	}
	metricsEnabled := metricsListen != ""
	if w.MetricsEnabled != nil {
		metricsEnabled = *w.MetricsEnabled
	}
	tlsMode, err := parseTLSMode(w.TLS)
	if err != nil {
		return Daemon{}, err
	}
	trustedProxies, err := parseTrustedProxies(w.TrustedProxies)
	if err != nil {
		return Daemon{}, fmt.Errorf("invalid daemon.trusted_proxies: %w", err)
	}
	checkUpdates := true
	if w.CheckUpdates != nil {
		checkUpdates = *w.CheckUpdates
	}
	return Daemon{
		AllowStationDispatch: w.AllowStationDispatch,
		ShutdownTimeout:      shutdown,
		ExternalURL:          externalURL,
		CheckUpdates:         checkUpdates,
		MetricsEnabled:       metricsEnabled,
		MetricsListen:        metricsListen,
		TLS:                  tlsMode,
		TLSCert:              strings.TrimSpace(w.TLSCert),
		TLSKey:               strings.TrimSpace(w.TLSKey),
		TrustedProxies:       trustedProxies,
	}, nil
}

// parseTrustedProxies validates each trusted-proxy entry ([daemon]
// trusted_proxies or RUNWISP_TRUSTED_PROXIES) and returns the normalised CIDRs,
// dropping blanks. Catch-all ranges are rejected here so `runwisp validate`
// catches them before a restart.
func parseTrustedProxies(entries []string) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(entries))
	for _, raw := range entries {
		cidr, err := proxycidr.Normalize(raw)
		if err != nil {
			return nil, err
		}
		if cidr != "" {
			out = append(out, cidr)
		}
	}
	return out, nil
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
	CoalesceLimit     int       `toml:"coalesce_limit,omitempty"`
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

	Topic string `toml:"topic,omitempty"`
	Token string `toml:"token,omitempty"`
	User  string `toml:"user,omitempty"`

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
