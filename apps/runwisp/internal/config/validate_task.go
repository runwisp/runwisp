// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

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
	// `run` alongside a compose backend means "run this command in that
	// service" — exec mode consumes it as the script. It stays an error for the
	// modes that have no place to put it: stack mode runs the whole project, and
	// services mode runs the service's own compose-declared command.
	if ce, isCompose := task.ExecutionDef.(*model.ComposeExecution); isCompose && strings.TrimSpace(task.Run) != "" {
		switch ce.Mode {
		case model.ComposeModeExec:
			// The script; consumed by ComposeExecution.Command.
		case model.ComposeModeStack:
			return fmt.Errorf("task %s sets `run` on a stack-mode compose unit, which brings the whole project up; move the command to its own task", task.Name)
		default:
			return fmt.Errorf(
				"task %s sets `run` with compose_mode = %q, which runs the service's own command; use compose_mode = %q to run your command inside the container",
				task.Name, model.ComposeModeRun, model.ComposeModeExec)
		}
	}
	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return fmt.Errorf("run command is required for %s %s", unitKind(task), task.Name)
	}
	if shellDef, ok := execDef.(*model.ShellExecution); ok && strings.TrimSpace(shellDef.Script) == "" {
		return fmt.Errorf("run command is required for %s %s", unitKind(task), task.Name)
	}
	return nil
}

// validateTaskShell requires the resolved shell to be an absolute path. A
// relative name would be resolved against the daemon's PATH at run time, which
// is non-deterministic. Like working_dir, the shell is never stat-ed here
// because the daemon may run in a different mount namespace than `runwisp
// validate`.
func validateTaskShell(task *model.Task) error {
	if task.Shell == "" {
		// Post-defaults this is always /bin/sh; the executor also falls back to
		// /bin/sh on empty, so an unset shell is valid.
		return nil
	}
	if strings.ContainsRune(task.Shell, 0) {
		return fmt.Errorf("invalid shell for %s %s: contains a NUL byte", unitKind(task), task.Name)
	}
	if !filepath.IsAbs(task.Shell) {
		return fmt.Errorf("invalid shell for %s %s: %q must be an absolute path (e.g. /bin/bash)", unitKind(task), task.Name, task.Shell)
	}
	return nil
}

// validateTaskStopSignal rejects a stop_signal outside the curated allowlist.
// An empty value is accepted — the executor falls back to SIGTERM, matching the
// post-defaults resolution. Only the canonical "SIGxxx" spelling is accepted;
// see validateStopSignal.
func validateTaskStopSignal(task *model.Task) error {
	return validateStopSignal(fmt.Sprintf("stop_signal for %s %s", unitKind(task), task.Name), task.StopSignal)
}

// validateStopSignal is the shared check for per-task and defaults.stop_signal.
// Only the canonical "SIGxxx" spelling is accepted (case-insensitively); a bare
// name like "TERM" is rejected. The importer converts foreign bare names to
// canonical form via NormalizeSignalName before writing them.
func validateStopSignal(scope, signal string) error {
	if signal == "" {
		return nil
	}
	up := strings.ToUpper(strings.TrimSpace(signal))
	canonical, ok := model.NormalizeSignalName(signal)
	if !ok || up != canonical {
		// up != canonical means the operator omitted the SIG prefix (bare form).
		return fmt.Errorf("invalid %s: %q (must be one of %s)", scope, signal, strings.Join(model.StopSignals, ", "))
	}
	return nil
}

// validateTaskRunUser checks the shape of the run-as `user` spec. Only the
// `user` / `user:group` form is validated here; resolving the name to a uid/gid
// is deferred to run time (the account may not exist when the config is loaded).
func validateTaskRunUser(task *model.Task) error {
	if _, _, err := model.ParseRunUserSpec(task.RunUser); err != nil {
		return fmt.Errorf("invalid user for %s %s: %w", unitKind(task), task.Name, err)
	}
	return nil
}

func validateTaskLimits(task *model.Task) error {
	if err := validateConcurrencyLimits(task); err != nil {
		return err
	}
	if err := validateRetryLimits(task); err != nil {
		return err
	}
	// restart_attempts is service-only (validated in validateServiceTask); a
	// [tasks.*] that sets it is already rejected at strict decode.
	return validateTaskDurations(task)
}

func validateConcurrencyLimits(task *model.Task) error {
	if task.MaxConcurrent < 0 {
		return fmt.Errorf("invalid max_concurrent for task %s: must be a positive integer", task.Name)
	}
	if task.MaxConcurrent > MaxConcurrentCap {
		return fmt.Errorf("invalid max_concurrent for task %s: %d exceeds the cap of %d", task.Name, task.MaxConcurrent, MaxConcurrentCap)
	}
	if task.MaxQueued != nil {
		if *task.MaxQueued < 0 {
			return fmt.Errorf("invalid max_queued for task %s: must be 0 or more", task.Name)
		}
		if *task.MaxQueued > MaxQueuedCap {
			return fmt.Errorf("invalid max_queued for task %s: %d exceeds the cap of %d", task.Name, *task.MaxQueued, MaxQueuedCap)
		}
	}
	return nil
}

func validateRetryLimits(task *model.Task) error {
	if task.Jitter != nil {
		if *task.Jitter < 0 {
			return fmt.Errorf("invalid jitter for task %s: must be zero or a positive duration", task.Name)
		}
		if *task.Jitter > JitterCap {
			return fmt.Errorf("invalid jitter for task %s: %s exceeds the cap of %s", task.Name, *task.Jitter, JitterCap)
		}
	}
	if task.RetryAttempts < 0 {
		return fmt.Errorf("invalid retry_attempts for task %s: must be non-negative", task.Name)
	}
	if task.RetryAttempts > RetryAttemptsCap {
		return fmt.Errorf("invalid retry_attempts for task %s: %d exceeds the cap of %d", task.Name, task.RetryAttempts, RetryAttemptsCap)
	}
	if task.CatchUp != nil {
		if *task.CatchUp < 0 {
			return fmt.Errorf("invalid catch_up for task %s: must be a non-negative integer (0 skips, 1 re-runs the most recent, N re-runs up to the N most recent)", task.Name)
		}
		if *task.CatchUp > CatchUpCap {
			return fmt.Errorf("invalid catch_up for task %s: %d exceeds the cap of %d", task.Name, *task.CatchUp, CatchUpCap)
		}
	}
	return nil
}

// validateTaskDurations rejects negative durations that would otherwise
// parse but then be silently ignored (e.g. the run manager only arms the
// timeout timer when > 0), dropping the operator's intent without a word.
func validateTaskDurations(task *model.Task) error {
	if task.GracefulStop != nil && *task.GracefulStop < 0 {
		return fmt.Errorf("invalid graceful_stop for task %s: must be zero or a positive duration", task.Name)
	}
	if task.Timeout != nil && *task.Timeout < 0 {
		return fmt.Errorf("invalid timeout for task %s: must be zero or a positive duration", task.Name)
	}
	if task.RetryDelay != nil && *task.RetryDelay < 0 {
		return fmt.Errorf("invalid retry_delay for task %s: must be zero or a positive duration", task.Name)
	}
	if task.RestartDelay != nil && *task.RestartDelay < 0 {
		return fmt.Errorf("invalid restart_delay for task %s: must be zero or a positive duration", task.Name)
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
		{"on_overlap for " + unitKind(task) + " " + task.Name, string(task.OnOverlap), validOnOverlap, false},
		{"restart for " + unitKind(task) + " " + task.Name, string(task.Restart), validRestart, true},
		{"retry_backoff for " + unitKind(task) + " " + task.Name, string(task.RetryBackoff), validBackoff, true},
		{"log_on_full for " + unitKind(task) + " " + task.Name, task.LogOnFull, validLogOnFull, true},
	}
	for _, e := range enums {
		if err := requireOneOf(e.scope, e.value, e.allowed, e.emptyOK); err != nil {
			return err
		}
	}
	return nil
}

// validateCatchUpOverlap rejects a combination that silently defeats a
// multi-run catch_up: the runtime replays missed ticks by firing TriggerRun
// back-to-back, which only lands every replay when on_overlap = "queue"
// serializes them. Under "skip" all but the first replay is immediately
// rejected as an overlap (recorded, but not what the operator asked for), and
// under "kill" each replay cancels the previous one mid-run, wasting the work
// already done. Only bites when catch_up > 1 (a single re-run has nothing to
// serialize). Runs after defaults are applied, so both fields hold their
// effective values.
func validateCatchUpOverlap(task *model.Task) error {
	if task.CatchUpValue() <= 1 {
		return nil
	}
	if task.OnOverlap == model.PolicySkip || task.OnOverlap == model.PolicyKill {
		return fmt.Errorf(
			"invalid catch_up for task %s: catch_up = %d replays missed ticks back-to-back, but on_overlap = %q lets only one through at a time — use on_overlap = \"queue\", or lower catch_up to 1 (most recent only) or 0 (skip)",
			task.Name, task.CatchUpValue(), task.OnOverlap)
	}
	return nil
}

func validateTaskRetention(task *model.Task) error {
	if err := validateKeepRuns(fmt.Sprintf("keep_runs for %s %s", unitKind(task), task.Name), task.KeepRuns); err != nil {
		return err
	}
	if err := validateKeepFor(fmt.Sprintf("keep_for for %s %s", unitKind(task), task.Name), task.KeepFor); err != nil {
		return err
	}
	if _, err := ResolveTimezone(fmt.Sprintf("timezone for %s %s", unitKind(task), task.Name), task.Timezone); err != nil {
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
		string(task.RestartBackoff), validBackoff, true); err != nil {
		return err
	}
	if task.HealthyAfter != nil && *task.HealthyAfter < 0 {
		return fmt.Errorf("invalid healthy_after for service %s: must be a positive duration", task.Name)
	}
	if err := validateRestartAttempts(fmt.Sprintf("restart_attempts for service %s", task.Name), task.RestartAttempts); err != nil {
		return err
	}
	return validateHealthCheck(task)
}

// validateRestartAttempts bounds restart_attempts the same way for services,
// restarting tasks, and [defaults]: negative is meaningless, and
// StartRetriesCap keeps a misconfigured value from disabling the give-up
// behaviour in practice. A nil
// pointer means the key was omitted (inherit/default); an explicit 0 means
// "give up after the very first failure" and is accepted like any other
// in-range value. Mirrors validateKeepRuns's nil-vs-zero shape.
func validateRestartAttempts(scope string, attempts *int) error {
	if attempts == nil {
		return nil
	}
	if *attempts < 0 {
		return fmt.Errorf("invalid %s: must be non-negative", scope)
	}
	if *attempts > StartRetriesCap {
		return fmt.Errorf("invalid %s: %d exceeds the cap of %d", scope, *attempts, StartRetriesCap)
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
// A nil pointer means the key was omitted (inherit); an explicit 0 means "keep
// no completed runs"; any positive integer up to KeepRunsCap is accepted.
func validateKeepRuns(scope string, n *int) error {
	if n == nil {
		return nil
	}
	if *n < 0 {
		return fmt.Errorf("invalid %s: must be a non-negative integer", scope)
	}
	if *n > KeepRunsCap {
		return fmt.Errorf("invalid %s: %d exceeds the cap of %d", scope, *n, KeepRunsCap)
	}
	return nil
}

// validateKeepFor rejects negative durations and durations above the keep_for
// cap. Zero is the post-parse sentinel for "omitted, inherit defaults"; any
// positive duration up to KeepForCap is accepted.
func validateKeepFor(scope string, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("invalid %s: must be a positive duration", scope)
	}
	if d > KeepForCap {
		return fmt.Errorf("invalid %s: %s exceeds the cap of %s", scope, d, KeepForCap)
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
	if slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("invalid %s: %q (must be one of %s)", scope, value, strings.Join(allowed, ", "))
}

var (
	validOnOverlap = []string{
		string(model.PolicyQueue),
		string(model.PolicySkip),
		string(model.PolicyKill),
	}
	validRestart = []string{
		string(model.RestartNever),
		string(model.RestartAlways),
		string(model.RestartOnFailure),
	}
	validBackoff          = []string{string(model.BackoffConstant), string(model.BackoffLinear), string(model.BackoffExponential)}
	validLogOnFull        = []string{model.LogOverflowDropNew, model.LogOverflowDropOld, model.LogOverflowKill}
	defaultTaskLogMaxSize = int64(100 * 1024 * 1024)
	// DefaultRestartDelay is the delay before a service instance's first
	// restart when neither the service nor a caller supplies one. Exported so
	// runtime consumers of a *model.Task built without going through Load
	// (test literals, station ephemeral dispatch) can fall back to the same
	// protective default Load would have applied — see DurationOrDefault.
	DefaultRestartDelay = time.Second
)

// Hard caps for integer config fields. Above-cap values fail config load with
// a numeric error message — there's no silent clamping. The caps are
// deliberately generous: any operator who needs more is almost certainly
// configuring something pathological and should rethink, not raise the cap.
const (
	MaxConcurrentCap = 1024
	MaxQueuedCap     = 10000
	KeepRunsCap      = 1_000_000
	RetryAttemptsCap = 100
	// StartRetriesCap bounds restart_attempts. A service that fast-fails this many
	// times in a row is broken; allowing more just delays the FATAL signal.
	StartRetriesCap = 100
	// CatchUpCap bounds catch_up. Unlike the typo-protection caps above, each
	// catch-up run is a real process spawned in a tight loop at boot — this exists
	// to stop a misconfigured value from trying to fire tens of thousands of
	// processes back-to-back after a long outage.
	CatchUpCap = 10_000
	// JitterCap bounds the jitter window at a full day. The runtime clamps each
	// fire to the gap before the next tick, so this is pure typo protection
	// (e.g. "30h" meant "30m") rather than a correctness limit.
	JitterCap = 24 * time.Hour
	// KeepForCap bounds keep_for at ~100 years. Like JitterCap this is pure typo
	// protection (e.g. "999999d" meant "999999s") — it never constrains a real
	// operator and stays well under time.Duration's ~292-year ceiling.
	KeepForCap = 100 * 365 * 24 * time.Hour

	// EnvMaxEntries caps the combined size of a task's inline env and
	// env_file-derived secret env. Generous enough for any realistic dotenv
	// while keeping a malformed config from blowing up the daemon's memory.
	EnvMaxEntries = 256
	// EnvMaxValueLen re-exports the shared per-value cap that lives on the model
	// package (env values and supplied param values share one limit so they
	// never drift). See model.EnvMaxValueLen for the rationale.
	EnvMaxValueLen = model.EnvMaxValueLen
)
