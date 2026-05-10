// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/internal/model"
)

// Load reads, decodes, defaults, and validates a runwisp.toml file.
func Load(path string) (*Config, error) {
	cfg, err := LoadRaw(path)
	if err != nil {
		return nil, err
	}
	ApplyDefaults(cfg)
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadRaw reads and decodes a runwisp.toml file without applying defaults or
// running validation.
func LoadRaw(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return decode(data)
}

func decode(data []byte) (*Config, error) {
	var raw tomlConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	taskNames := make([]string, 0, len(raw.Tasks))
	for name, w := range raw.Tasks {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("task name is required")
		}
		if w.Restart == model.RestartAlways {
			return nil, fmt.Errorf("task %q sets restart=\"always\"; use [services.%s] instead", name, name)
		}
		if w.Instances != nil {
			return nil, fmt.Errorf("task %q sets instances; instances is only valid on [services.*]", name)
		}
		taskNames = append(taskNames, name)
	}
	sort.Strings(taskNames)

	serviceNames := make([]string, 0, len(raw.Services))
	for name := range raw.Services {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("service name is required")
		}
		if _, dup := raw.Tasks[name]; dup {
			return nil, fmt.Errorf("name %q used by both [tasks.*] and [services.*]", name)
		}
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

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

	defaults, err := raw.Defaults.toDefaults()
	if err != nil {
		return nil, err
	}
	storage, err := raw.Storage.toStorage()
	if err != nil {
		return nil, err
	}

	notifyCfg, err := raw.toNotifyConfig(taskNames, raw.Tasks, serviceNames, raw.Services)
	if err != nil {
		return nil, err
	}

	return &Config{
		Tasks:     tasks,
		Defaults:  defaults,
		Storage:   storage,
		Daemon:    raw.Daemon,
		Notify:    notifyCfg,
		Scheduler: Scheduler{Timezone: raw.Scheduler.Timezone},
	}, nil
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
	if _, err := ResolveTimezone("scheduler.timezone", cfg.Scheduler.Timezone); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(cfg.Tasks))
	for i := range cfg.Tasks {
		if err := validateTask(&cfg.Tasks[i], seen); err != nil {
			return err
		}
	}
	if err := validateSchedulerTimezone(cfg); err != nil {
		return err
	}
	if err := validateNotify(&cfg.Notify); err != nil {
		return err
	}
	return nil
}

func validateTask(task *model.Task, seen map[string]struct{}) error {
	if strings.TrimSpace(task.Name) == "" {
		return fmt.Errorf("task name is required")
	}
	if _, exists := seen[task.Name]; exists {
		return fmt.Errorf("duplicate task name: %s", task.Name)
	}
	seen[task.Name] = struct{}{}

	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return fmt.Errorf("task run command is required for task: %s", task.Name)
	}
	if shellDef, ok := execDef.(*model.ShellExecution); ok && strings.TrimSpace(shellDef.Script) == "" {
		return fmt.Errorf("task run command is required for task: %s", task.Name)
	}

	if task.Parallelism <= 0 {
		return fmt.Errorf("invalid parallelism for task %s: must be greater than zero", task.Name)
	}

	if err := requireOneOf(fmt.Sprintf("on_overlap for task %s", task.Name),
		string(task.OnOverlap), validOnOverlap, false); err != nil {
		return err
	}
	if err := requireOneOf(fmt.Sprintf("restart for task %s", task.Name),
		string(task.Restart), validRestart, true); err != nil {
		return err
	}
	if err := requireOneOf(fmt.Sprintf("retry_backoff for task %s", task.Name),
		task.RetryBackoff, validRetryBackoff, true); err != nil {
		return err
	}
	if err := requireOneOf(fmt.Sprintf("log_on_full for task %s", task.Name),
		task.LogOnFull, validLogOnFull, true); err != nil {
		return err
	}
	if err := requireOneOf(fmt.Sprintf("catch_up for task %s", task.Name),
		string(task.CatchUp), validCatchUp, true); err != nil {
		return err
	}

	if task.Kind.IsService() {
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
	}

	if task.RetryAttempts < 0 {
		return fmt.Errorf("invalid retry_attempts for task %s: must be non-negative", task.Name)
	}
	if task.MaxCatchUpRuns < -1 {
		return fmt.Errorf("invalid max_catch_up_runs for task %s: must be -1 (unlimited), 0 (inherit default), or a positive count", task.Name)
	}
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

// validateSchedulerTimezone enforces that any cron-driven task has an explicit
// timezone available — either its own `timezone =` or `[scheduler] timezone`.
// We refuse to silently default to UTC because "0 2 * * *" silently running at
// 02:00 UTC instead of the operator's local 02:00 is the kind of failure
// Prime Directive #1 forbids. Manual-only tasks (no cron) and services are
// unaffected.
func validateSchedulerTimezone(cfg *Config) error {
	if cfg.Scheduler.Timezone != "" {
		return nil
	}
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		if task.Cron == "" || task.Timezone != "" {
			continue
		}
		return fmt.Errorf(
			"task %q has cron = %q but neither [scheduler] timezone nor a per-task timezone is set; "+
				"set [scheduler] timezone = \"UTC\" (or a specific IANA zone like \"America/New_York\") so cron firings are unambiguous",
			task.Name, task.Cron,
		)
	}
	return nil
}

// validateKeepRuns enforces the tri-state semantics:
//   - 0: inherit defaults
//   - -1: explicit unlimited (no count-based cap)
//   - >0: keep this many runs.
//
// Anything below -1 is a typo, not a value the user could mean.
func validateKeepRuns(scope string, n int) error {
	if n < -1 {
		return fmt.Errorf("invalid %s: must be -1 (unlimited), 0 (inherit default), or a positive count", scope)
	}
	return nil
}

// validateKeepFor enforces the tri-state semantics for keep_for:
//   - 0: inherit defaults
//   - -1: explicit unlimited ("unlimited" token at parse time)
//   - >0: retention window.
func validateKeepFor(scope string, d time.Duration) error {
	if d < -1 {
		return fmt.Errorf("invalid %s: must be \"unlimited\", omitted (inherit default), or a positive duration", scope)
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

// DefaultMaxCatchUpRuns caps the per-task `catch_up = "all"` backfill at startup
// when the operator hasn't picked their own number. A two-week outage on a
// `* * * * *` task would otherwise queue 20,160 catch-up runs and bury the
// scheduler; 100 keeps the backlog visible without serial-executing days of
// it. Operators who want every tick set max_catch_up_runs = -1.
const DefaultMaxCatchUpRuns = 100

// ApplyDefaults fills in zero-valued fields with sensible defaults.
func ApplyDefaults(cfg *Config) {
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]

		if task.Kind.IsService() {
			applyServiceDefaults(task)
		} else {
			applyTaskDefaults(task)
		}
		if task.CatchUp == "" {
			task.CatchUp = model.MissedRunLatest
		}
		// max_catch_up_runs tri-state mirrors keep_runs:
		//   0  → inherit built-in default (DefaultMaxCatchUpRuns)
		//  -1  → explicit unlimited (no cap)
		//  >0  → explicit cap
		if task.MaxCatchUpRuns == 0 {
			task.MaxCatchUpRuns = DefaultMaxCatchUpRuns
		}

		if task.Timeout == 0 {
			task.Timeout = cfg.Defaults.Timeout
		}
		if task.LogMaxSize == 0 {
			task.LogMaxSize = cfg.Defaults.LogMaxSize
		}
		if task.LogOnFull == "" && cfg.Defaults.LogOnFull != "" {
			task.LogOnFull = cfg.Defaults.LogOnFull
		}
		// keep_runs tri-state:
		//   0  → inherit default
		//  -1  → explicit unlimited (do not pull from defaults)
		//  >0  → explicit cap
		if task.KeepRuns == 0 && cfg.Defaults.KeepRuns != 0 {
			task.KeepRuns = cfg.Defaults.KeepRuns
		}
		// keep_for tri-state mirrors keep_runs:
		//   0  → inherit default
		//  -1  → explicit unlimited (do not pull from defaults)
		//  >0  → explicit window
		if task.KeepFor == 0 && cfg.Defaults.KeepFor != 0 {
			task.KeepFor = cfg.Defaults.KeepFor
		}

		if task.LogMaxSize == 0 {
			task.LogMaxSize = defaultTaskLogMaxSize
		}
		if task.LogOnFull == "" {
			task.LogOnFull = model.LogOverflowDropOld
		}
	}
}

func applyTaskDefaults(task *model.Task) {
	if task.Group == "" {
		task.Group = "Tasks"
	}
	if task.Parallelism == 0 {
		task.Parallelism = 1
	}
	if task.OnOverlap == "" {
		task.OnOverlap = model.PolicyQueue
	}
}

func applyServiceDefaults(task *model.Task) {
	if task.Group == "" {
		task.Group = "Services"
	}
	if task.OnOverlap == "" {
		task.OnOverlap = model.PolicySkip
	}
	if task.Parallelism == 0 {
		task.Parallelism = 1
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
}
