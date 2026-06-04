// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/model"
)

// Load reads, decodes, defaults, and validates a runwisp.toml file.
func Load(path string) (*Config, error) {
	cfg, err := loadRaw(path)
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Dir(path)
	if err := expandComposeBlocks(cfg, baseDir); err != nil {
		return nil, err
	}
	if err := resolveComposePaths(cfg, baseDir); err != nil {
		return nil, err
	}
	if err := resolveEnvLayers(cfg, baseDir); err != nil {
		return nil, err
	}
	ApplyDefaults(cfg)
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveEnvLayers reads each env_file / secrets_file referenced by the config
// and merges the file's KEY=VALUE pairs beneath the corresponding inline map —
// inline entries override file entries, docker-compose-style. Relative paths
// are resolved against baseDir (the runwisp.toml directory). Dotenv file
// contents are taken literally; ${...} substitution applies only to TOML.
func resolveEnvLayers(cfg *Config, baseDir string) error {
	var err error
	if cfg.Defaults.Env, err = mergeEnvFileLayer(baseDir, cfg.Defaults.EnvFile, cfg.Defaults.Env, "defaults"); err != nil {
		return err
	}
	if cfg.Defaults.Secrets, err = mergeEnvFileLayer(baseDir, cfg.Defaults.SecretsFile, cfg.Defaults.Secrets, "defaults"); err != nil {
		return err
	}
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		scope := fmt.Sprintf("task %q", task.Name)
		if task.Env, err = mergeEnvFileLayer(baseDir, task.EnvFile, task.Env, scope); err != nil {
			return err
		}
		if task.Secrets, err = mergeEnvFileLayer(baseDir, task.SecretsFile, task.Secrets, scope); err != nil {
			return err
		}
	}
	return nil
}

// mergeEnvFileLayer loads a dotenv file (when set) and merges the inline map
// over it. Returns the inline map untouched when no file is configured.
func mergeEnvFileLayer(baseDir, file string, inline map[string]string, scope string) (map[string]string, error) {
	if file == "" {
		return inline, nil
	}
	values, err := loadEnvFile(baseDir, file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", scope, err)
	}
	return mergeEnv(values, inline), nil
}

// resolveComposePaths absolutizes the compose file path on tasks/services that
// set compose_file directly ([services.*] / [tasks.*]); the [compose.*] block
// path already resolves to absolute during expansion, so those are skipped by
// the IsAbs guard. WorkingDir defaults to the file's directory so the CLI runs
// from there, matching docker compose's own behaviour.
func resolveComposePaths(cfg *Config, baseDir string) error {
	for i := range cfg.Tasks {
		ce, ok := cfg.Tasks[i].ExecutionDef.(*model.ComposeExecution)
		if !ok || ce.File == "" || filepath.IsAbs(ce.File) {
			continue
		}
		resolved, err := resolveComposeFile(ce.File, baseDir)
		if err != nil {
			return fmt.Errorf("task %q: %w", cfg.Tasks[i].Name, err)
		}
		ce.File = resolved
		if ce.WorkingDir == "" {
			ce.WorkingDir = filepath.Dir(resolved)
		}
	}
	return nil
}

// Warnings reports non-fatal findings an operator should see after a
// successful config load. Both daemon boot and `runwisp validate` print from
// here, so future advisory checks land in one place and stay in sync.
func Warnings(cfg *Config) []string {
	return gracefulStopWarnings(cfg)
}

// gracefulStopWarnings reports tasks whose graceful_stop exceeds the daemon
// shutdown_timeout. The daemon will SIGKILL such tasks before their grace
// window completes during a daemon-wide shutdown — operators usually want
// to either lengthen [daemon] shutdown_timeout or shorten the per-task value.
func gracefulStopWarnings(cfg *Config) []string {
	limit := cfg.Daemon.ShutdownTimeout
	if limit <= 0 {
		return nil
	}
	var warnings []string
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		if task.GracefulStop > limit {
			warnings = append(warnings, fmt.Sprintf(
				"task %q has graceful_stop=%s but [daemon] shutdown_timeout=%s; the daemon will SIGKILL this task before its grace window completes during shutdown",
				task.Name, task.GracefulStop, limit,
			))
		}
	}
	return warnings
}

func loadRaw(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return decode(data, filepath.Dir(path))
}

// decode parses TOML bytes into a Config. baseDir is the runwisp.toml
// directory; ${file:...} substitutions resolve relative paths against it.
func decode(data []byte, baseDir string) (*Config, error) {
	var raw tomlConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, formatDecodeError(err)
	}
	if err := expandConfig(&raw, baseDir, os.LookupEnv); err != nil {
		return nil, err
	}

	taskNames, err := collectTaskNames(&raw)
	if err != nil {
		return nil, err
	}
	serviceNames, err := collectServiceNames(&raw)
	if err != nil {
		return nil, err
	}
	tasks, err := buildTaskSlice(&raw, taskNames, serviceNames)
	if err != nil {
		return nil, err
	}

	defaults, err := raw.Defaults.toDefaults()
	if err != nil {
		return nil, err
	}
	storage, err := raw.Storage.toStorage()
	if err != nil {
		return nil, err
	}
	daemon, err := raw.Daemon.toDaemon()
	if err != nil {
		return nil, err
	}

	notifyCfg, err := raw.toNotifyConfig(taskNames, raw.Tasks, serviceNames, raw.Services)
	if err != nil {
		return nil, err
	}

	return &Config{
		Tasks:                tasks,
		Defaults:             defaults,
		Storage:              storage,
		Daemon:               daemon,
		Notify:               notifyCfg,
		Scheduler:            Scheduler{Timezone: raw.Scheduler.Timezone},
		pendingComposeBlocks: raw.Compose,
	}, nil
}

func collectTaskNames(raw *tomlConfig) ([]string, error) {
	names := make([]string, 0, len(raw.Tasks))
	for name, w := range raw.Tasks {
		if err := model.ValidateTaskName(name); err != nil {
			return nil, err
		}
		if w.Restart == model.RestartAlways {
			return nil, fmt.Errorf("task %q sets restart=\"always\"; use [services.%s] instead", name, name)
		}
		if w.Instances != nil {
			return nil, fmt.Errorf("task %q sets instances; instances is only valid on [services.*]", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func collectServiceNames(raw *tomlConfig) ([]string, error) {
	names := make([]string, 0, len(raw.Services))
	for name := range raw.Services {
		if err := model.ValidateTaskName(name); err != nil {
			return nil, err
		}
		if _, dup := raw.Tasks[name]; dup {
			return nil, fmt.Errorf("name %q used by both [tasks.*] and [services.*]", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func buildTaskSlice(raw *tomlConfig, taskNames, serviceNames []string) ([]model.Task, error) {
	tasks := make([]model.Task, 0, len(taskNames)+len(serviceNames))
	for _, name := range taskNames {
		t, err := raw.Tasks[name].toTask(name)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	for _, name := range serviceNames {
		t, err := raw.Services[name].toTask(name)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// Validate checks for invalid configuration values. Durations and byte sizes
// have already been parsed at this point — only enum membership, ranges, and
// required fields remain.
func Validate(cfg *Config) error {
	if err := requireOneOf("defaults.log_on_full", cfg.Defaults.LogOnFull, validLogOnFull, true); err != nil {
		return err
	}
	if err := validateKeepRuns("defaults.keep_runs", cfg.Defaults.KeepRuns); err != nil {
		return err
	}
	if err := validateKeepFor("defaults.keep_for", cfg.Defaults.KeepFor); err != nil {
		return err
	}
	if cfg.Defaults.BackoffResetAfter < 0 {
		return fmt.Errorf("invalid defaults.backoff_reset_after: must be a positive duration")
	}
	if cfg.Daemon.ShutdownTimeout < 0 {
		return fmt.Errorf("invalid daemon.shutdown_timeout: must be a positive duration")
	}
	if _, err := ResolveTimezone("scheduler.timezone", cfg.Scheduler.Timezone); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(cfg.Tasks))
	for i := range cfg.Tasks {
		if err := validateTask(&cfg.Tasks[i], seen); err != nil {
			return err
		}
	}
	if err := validateNotify(&cfg.Notify); err != nil {
		return err
	}
	return nil
}

func validateTask(task *model.Task, seen map[string]struct{}) error {
	if err := validateTaskIdentity(task, seen); err != nil {
		return err
	}
	if err := validateTaskCommand(task); err != nil {
		return err
	}
	if err := validateTaskLimits(task); err != nil {
		return err
	}
	if err := validateTaskEnums(task); err != nil {
		return err
	}
	if err := validateTaskEnv(task); err != nil {
		return err
	}
	if task.Kind.IsService() {
		if err := validateServiceTask(task); err != nil {
			return err
		}
	}
	if err := validateTaskRetention(task); err != nil {
		return err
	}
	return validateTaskCron(task)
}

// validateTaskCron rejects cron expressions the scheduler would refuse at
// boot, so `runwisp validate` and daemon startup fail identically. Runs after
// validateTaskRetention so an invalid per-task timezone surfaces as the
// friendlier timezone error rather than a CRON_TZ parse failure. An empty
// cron is a trigger-only task and is allowed.
func validateTaskCron(task *model.Task) error {
	if task.Cron == "" {
		return nil
	}
	if err := cronspec.Validate(task.Cron, task.Timezone); err != nil {
		return fmt.Errorf(
			"invalid cron for task %q: %q — %v; expected 5 fields \"min hour day month weekday\" (e.g. \"0 3 * * *\" = 03:00 daily), a descriptor like @hourly/@daily/@weekly, or @every 1h30m",
			task.Name, task.Cron, err)
	}
	return nil
}

// envKeyPattern is the POSIX-ish shape required for environment variable
// names: letters, digits, and underscores; not starting with a digit.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateTaskEnv enforces shape and size limits on env and secrets. Both maps
// are validated with the same rules and counted against one combined cap so
// the merged process env can safely be turned into KEY=VALUE strings without
// producing malformed entries.
func validateTaskEnv(task *model.Task) error {
	scope := fmt.Sprintf("env for task %s", task.Name)
	if err := validateEnvMap(scope, task.Env); err != nil {
		return err
	}
	secretScope := fmt.Sprintf("secrets for task %s", task.Name)
	if err := validateEnvMap(secretScope, task.Secrets); err != nil {
		return err
	}
	if total := len(task.Env) + len(task.Secrets); total > EnvMaxEntries {
		return fmt.Errorf("invalid env for task %s: %d entries exceeds the cap of %d", task.Name, total, EnvMaxEntries)
	}
	return nil
}

// validateEnvMap is reusable across inline env and env_file values. Scope is a
// human-readable label embedded in error messages (e.g. "env for task foo" or
// "env_file /etc/runwisp/secrets.env").
func validateEnvMap(scope string, env map[string]string) error {
	if len(env) > EnvMaxEntries {
		return fmt.Errorf("invalid %s: %d entries exceeds the cap of %d", scope, len(env), EnvMaxEntries)
	}
	for key, value := range env {
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid %s: key %q must match %s", scope, key, envKeyPattern.String())
		}
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid %s: value for %q contains a NUL byte", scope, key)
		}
		if len(value) > EnvMaxValueLen {
			return fmt.Errorf("invalid %s: value for %q is %d bytes; cap is %d", scope, key, len(value), EnvMaxValueLen)
		}
	}
	return nil
}

func validateTaskIdentity(task *model.Task, seen map[string]struct{}) error {
	if err := model.ValidateTaskName(task.Name); err != nil {
		return err
	}
	if _, exists := seen[task.Name]; exists {
		return fmt.Errorf("duplicate task name: %s", task.Name)
	}
	seen[task.Name] = struct{}{}
	return nil
}

func validateTaskCommand(task *model.Task) error {
	// `run` and a compose backend are mutually exclusive — both set is
	// ambiguous, so fail fast rather than pick a precedence rule.
	if _, isCompose := task.ExecutionDef.(*model.ComposeExecution); isCompose && strings.TrimSpace(task.Run) != "" {
		return fmt.Errorf("task %s sets both `run` and `compose_file`; pick one execution backend", task.Name)
	}
	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return fmt.Errorf("task run command is required for task: %s", task.Name)
	}
	if shellDef, ok := execDef.(*model.ShellExecution); ok && strings.TrimSpace(shellDef.Script) == "" {
		return fmt.Errorf("task run command is required for task: %s", task.Name)
	}
	return nil
}

func validateTaskLimits(task *model.Task) error {
	if task.MaxConcurrent < 0 {
		return fmt.Errorf("invalid max_concurrent for task %s: must be a positive integer", task.Name)
	}
	if task.MaxConcurrent > MaxConcurrentCap {
		return fmt.Errorf("invalid max_concurrent for task %s: %d exceeds the cap of %d", task.Name, task.MaxConcurrent, MaxConcurrentCap)
	}
	if task.QueueMax < 0 {
		return fmt.Errorf("invalid queue_max for task %s: must be a positive integer", task.Name)
	}
	if task.QueueMax > QueueMaxCap {
		return fmt.Errorf("invalid queue_max for task %s: %d exceeds the cap of %d", task.Name, task.QueueMax, QueueMaxCap)
	}
	if task.GracefulStop < 0 {
		return fmt.Errorf("invalid graceful_stop for task %s: must be zero or a positive duration", task.Name)
	}
	if task.RetryAttempts < 0 {
		return fmt.Errorf("invalid retry_attempts for task %s: must be non-negative", task.Name)
	}
	if task.RetryAttempts > RetryAttemptsCap {
		return fmt.Errorf("invalid retry_attempts for task %s: %d exceeds the cap of %d", task.Name, task.RetryAttempts, RetryAttemptsCap)
	}
	if task.MaxCatchUpRuns < 0 {
		return fmt.Errorf("invalid max_catch_up_runs for task %s: must be a positive integer", task.Name)
	}
	return nil
}

func validateTaskEnums(task *model.Task) error {
	enums := []struct {
		scope   string
		value   string
		allowed []string
		emptyOK bool
	}{
		{"on_overlap for task " + task.Name, string(task.OnOverlap), validOnOverlap, false},
		{"restart for task " + task.Name, string(task.Restart), validRestart, true},
		{"retry_backoff for task " + task.Name, task.RetryBackoff, validRetryBackoff, true},
		{"log_on_full for task " + task.Name, task.LogOnFull, validLogOnFull, true},
		{"catch_up for task " + task.Name, string(task.CatchUp), validCatchUp, true},
	}
	for _, e := range enums {
		if err := requireOneOf(e.scope, e.value, e.allowed, e.emptyOK); err != nil {
			return err
		}
	}
	return nil
}

func validateTaskRetention(task *model.Task) error {
	if err := validateKeepRuns(fmt.Sprintf("keep_runs for task %s", task.Name), task.KeepRuns); err != nil {
		return err
	}
	if err := validateKeepFor(fmt.Sprintf("keep_for for task %s", task.Name), task.KeepFor); err != nil {
		return err
	}
	if _, err := ResolveTimezone(fmt.Sprintf("timezone for task %s", task.Name), task.Timezone); err != nil {
		return err
	}
	return nil
}

func validateServiceTask(task *model.Task) error {
	if task.Instances < 1 {
		return fmt.Errorf("invalid instances for service %s: must be >= 1", task.Name)
	}
	if task.Instances > MaxServiceInstances {
		return fmt.Errorf("invalid instances for service %s: must be <= %d", task.Name, MaxServiceInstances)
	}
	if err := requireOneOf(fmt.Sprintf("restart_backoff for service %s", task.Name),
		task.RestartBackoff, validRestartBackoff, true); err != nil {
		return err
	}
	if task.BackoffResetAfter < 0 {
		return fmt.Errorf("invalid backoff_reset_after for service %s: must be a positive duration", task.Name)
	}
	return nil
}

// ResolveTimezone returns the *time.Location for the given IANA name. An empty
// string means "fall back to the scheduler default" — callers handle that.
// Use this for both per-task timezone and the global scheduler timezone so
// the same error shape is produced everywhere.
func ResolveTimezone(scope, name string) (*time.Location, error) {
	if name == "" {
		return nil, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %q is not a valid IANA timezone (e.g. \"UTC\", \"America/New_York\"): %w", scope, name, err)
	}
	return loc, nil
}

// validateKeepRuns rejects negative values and values above the keep_runs cap.
// Zero is the post-parse sentinel for "omitted, inherit defaults"; any
// positive integer up to KeepRunsCap is accepted.
func validateKeepRuns(scope string, n int) error {
	if n < 0 {
		return fmt.Errorf("invalid %s: must be a positive integer", scope)
	}
	if n > KeepRunsCap {
		return fmt.Errorf("invalid %s: %d exceeds the cap of %d", scope, n, KeepRunsCap)
	}
	return nil
}

// validateKeepFor rejects negative durations. Zero is the post-parse sentinel
// for "omitted, inherit defaults"; any positive duration is accepted.
func validateKeepFor(scope string, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("invalid %s: must be a positive duration", scope)
	}
	return nil
}

// requireOneOf returns nil if value is in the allowed set. When emptyOK is
// true, an empty value is accepted (used for optional enums that fall through
// to a default).
func requireOneOf(scope, value string, allowed []string, emptyOK bool) error {
	if value == "" {
		if emptyOK {
			return nil
		}
		return fmt.Errorf("invalid %s: required, must be one of %s", scope, strings.Join(allowed, ", "))
	}
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("invalid %s: %q (must be one of %s)", scope, value, strings.Join(allowed, ", "))
}

var (
	validOnOverlap = []string{
		string(model.PolicyQueue),
		string(model.PolicySkip),
		string(model.PolicyTerminate),
	}
	validRestart = []string{
		string(model.RestartNever),
		string(model.RestartAlways),
		string(model.RestartOnFailure),
	}
	validRetryBackoff     = []string{model.BackoffConstant, model.BackoffLinear, model.BackoffExponential}
	validRestartBackoff   = []string{model.BackoffConstant, model.BackoffLinear, model.BackoffExponential}
	validLogOnFull        = []string{model.LogOverflowDropNew, model.LogOverflowDropOld, model.LogOverflowKillTask}
	validCatchUp          = []string{string(model.MissedRunLatest), string(model.MissedRunAll), string(model.MissedRunSkip)}
	defaultTaskLogMaxSize = int64(100 * 1024 * 1024)
	defaultRestartDelay   = time.Second
)

// Hard caps for integer config fields. Above-cap values fail config load with
// a numeric error message — there's no silent clamping. The caps are
// deliberately generous: any operator who needs more is almost certainly
// configuring something pathological and should rethink, not raise the cap.
const (
	MaxConcurrentCap = 1024
	QueueMaxCap      = 10000
	KeepRunsCap      = 1_000_000
	RetryAttemptsCap = 100

	// EnvMaxEntries caps the combined size of a task's inline env and
	// env_file-derived secret env. Generous enough for any realistic dotenv
	// while keeping a malformed config from blowing up the daemon's memory.
	EnvMaxEntries = 256
	// EnvMaxValueLen caps a single env value at 32 KiB. Linux's argv+env
	// limit is 128 KiB by default; capping per-value leaves room for many
	// entries without bumping into ARG_MAX surprises.
	EnvMaxValueLen = 32 * 1024
)

// Built-in defaults applied by ApplyDefaults when a field is omitted entirely.
const (
	DefaultMaxCatchUpRuns    = 100
	DefaultQueueMax          = 100
	DefaultGracefulStop      = 5 * time.Second
	DefaultDaemonShutdown    = 10 * time.Second
	DefaultBackoffResetAfter = 60 * time.Second
)

// ApplyDefaults fills in zero-valued fields with sensible defaults. The
// scheduler timezone, in particular, falls back to the host's system zone
// when the operator left [scheduler] timezone unset — so a fresh install
// just works without an explicit choice, while the resolved zone is still
// surfaced in the TUI banner and Web UI header.
func ApplyDefaults(cfg *Config) {
	if cfg.Scheduler.Timezone == "" {
		cfg.Scheduler.Timezone = ResolveSystemTimezone()
		cfg.Scheduler.Source = TimezoneSourceSystem
	} else {
		cfg.Scheduler.Source = TimezoneSourceConfig
	}

	if cfg.Daemon.ShutdownTimeout == 0 {
		cfg.Daemon.ShutdownTimeout = DefaultDaemonShutdown
	}
	if cfg.Defaults.BackoffResetAfter == 0 {
		cfg.Defaults.BackoffResetAfter = DefaultBackoffResetAfter
	}

	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]

		if task.Kind.IsService() {
			applyServiceDefaults(task, cfg.Defaults.BackoffResetAfter)
		} else {
			applyTaskDefaults(task)
		}
		if task.CatchUp == "" {
			task.CatchUp = model.MissedRunLatest
		}
		if task.MaxCatchUpRuns == 0 {
			task.MaxCatchUpRuns = DefaultMaxCatchUpRuns
		}
		if task.GracefulStop == 0 {
			task.GracefulStop = DefaultGracefulStop
		}

		applyInheritedDefaults(task, cfg.Defaults)
	}
}

// applyInheritedDefaults copies defaults-section values into task fields that
// were not explicitly set in TOML, then fills in absolute built-in fallbacks.
func applyInheritedDefaults(task *model.Task, d Defaults) {
	if task.Timeout == 0 {
		task.Timeout = d.Timeout
	}
	if task.LogMaxSize == 0 {
		task.LogMaxSize = d.LogMaxSize
	}
	if task.LogOnFull == "" && d.LogOnFull != "" {
		task.LogOnFull = d.LogOnFull
	}
	if task.KeepRuns == 0 && d.KeepRuns != 0 {
		task.KeepRuns = d.KeepRuns
	}
	if task.KeepFor == 0 && d.KeepFor != 0 {
		task.KeepFor = d.KeepFor
	}
	if task.LogMaxSize == 0 {
		task.LogMaxSize = defaultTaskLogMaxSize
	}
	if task.LogOnFull == "" {
		task.LogOnFull = model.LogOverflowDropOld
	}
	task.Env = mergeEnv(d.Env, task.Env)
	task.Secrets = mergeEnv(d.Secrets, task.Secrets)
}

// mergeEnv returns a map containing every key in base then in overlay, with
// overlay winning on collision. Returns nil when both inputs are empty so the
// executor can keep its "no env overlay → inherit parent" fast path. The
// inputs are never mutated.
func mergeEnv(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func applyTaskDefaults(task *model.Task) {
	if task.Group == "" {
		task.Group = "Tasks"
	}
	if task.MaxConcurrent == 0 {
		task.MaxConcurrent = 1
	}
	if task.OnOverlap == "" {
		task.OnOverlap = model.PolicyQueue
	}
	if task.QueueMax == 0 {
		task.QueueMax = DefaultQueueMax
	}
}

func applyServiceDefaults(task *model.Task, defaultBackoffReset time.Duration) {
	if task.Group == "" {
		task.Group = "Services"
	}
	if task.OnOverlap == "" {
		task.OnOverlap = model.PolicySkip
	}
	if task.MaxConcurrent == 0 {
		task.MaxConcurrent = 1
	}
	if task.Instances == 0 {
		task.Instances = 1
	}
	if task.RestartDelay == 0 {
		task.RestartDelay = defaultRestartDelay
	}
	if task.RestartBackoff == "" {
		task.RestartBackoff = model.BackoffExponential
	}
	if task.BackoffResetAfter == 0 {
		task.BackoffResetAfter = defaultBackoffReset
	}
}
