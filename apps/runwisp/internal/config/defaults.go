// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"cmp"
	"maps"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// TLS modes for [daemon] tls. TLSModeAuto serves HTTP on loopback and
// self-signed HTTPS on a non-loopback bind; TLSModeOff is plain HTTP on every
// bind (operator terminates TLS upstream or trusts the network) and is the
// default.
const (
	TLSModeAuto = "auto"
	TLSModeOff  = "off"
)

// Built-in defaults applied by ApplyDefaults when a field is omitted entirely.
const (
	DefaultGracefulStop   = 5 * time.Second
	DefaultRetryDelay     = 5 * time.Second
	DefaultDaemonShutdown = 10 * time.Second
	DefaultHealthyAfter   = 60 * time.Second
	// DefaultStartRetries is the number of consecutive fast failures a service
	// instance may accrue before the supervisor marks it FATAL and stops
	// restarting it. Applied when neither the service nor [defaults] sets
	// restart_attempts.
	DefaultStartRetries = 3
	// DefaultStopSignal is the first signal of the stop ladder when neither the
	// task nor [defaults] selects one. The daemon always follows with SIGKILL
	// after graceful_stop.
	DefaultStopSignal = "SIGTERM"
)

// ApplyDefaults fills in zero-valued fields with sensible defaults. The
// scheduler timezone, in particular, falls back to the host's system zone
// when the operator left [daemon] timezone unset — so a fresh install
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
	// An unset tls defaults to "off", unless the operator already supplied a
	// cert/key pair: leave it as "" there so validateTLS can still tell "never
	// wrote tls" apart from an explicit tls = "off", which contradicts the
	// cert override and is rejected.
	if cfg.Daemon.TLS == "" && (cfg.Daemon.TLSCert == "" || cfg.Daemon.TLSKey == "") {
		cfg.Daemon.TLS = TLSModeOff
	}
	if cfg.Defaults.HealthyAfter == nil {
		v := DefaultHealthyAfter
		cfg.Defaults.HealthyAfter = &v
	}

	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]

		if task.Kind.IsService() {
			applyServiceDefaults(task, cfg.Defaults)
		} else {
			applyTaskDefaults(task)
		}
		applyInheritedDefaults(task, cfg.Defaults)
		applyHealthCheckDefaults(task, cfg.Defaults)
	}
}

// applyInheritedDefaults copies [defaults] values into unit fields that were
// not explicitly set in TOML, then fills in absolute built-in fallbacks. Each
// scalar cascade is one cmp.Or call (unit value, else [defaults], else
// builtin); the pointer field (KeepRuns) and the two special cases (stop_signal
// canonicalization, failures delta-resolution) keep their own helpers.
func applyInheritedDefaults(task *model.Task, d Defaults) {
	// Timeout/Jitter are pointers so an explicit `= "0s"` (opt out — no timeout /
	// no jitter) is distinguishable from an omitted key. Only a nil (omitted)
	// unit value inherits the [defaults]; an explicit value, including 0, wins.
	if task.Timeout == nil && d.Timeout > 0 {
		task.Timeout = new(d.Timeout)
	}
	// Jitter is task-only: a service never inherits [defaults] jitter (it starts
	// every instance at boot, so there's no fire time to spread). An explicit
	// [services.x] jitter is rejected earlier by DisallowUnknownFields.
	if !task.Kind.IsService() && task.Jitter == nil && d.Jitter > 0 {
		task.Jitter = new(d.Jitter)
	}
	task.Shell = cmp.Or(task.Shell, d.Shell, model.DefaultShell)
	applyInheritedStopSignal(task, d)
	task.LogMaxSize = cmp.Or(task.LogMaxSize, d.LogMaxSize, defaultTaskLogMaxSize)
	task.LogOnFull = cmp.Or(task.LogOnFull, d.LogOnFull, model.LogOverflowDropOld)
	if task.KeepRuns == nil {
		task.KeepRuns = d.KeepRuns
	}
	task.KeepFor = cmp.Or(task.KeepFor, d.KeepFor)
	// catch_up is a cron-task concept; a service must not inherit a [defaults]
	// catch_up (a multi-run value against its on_overlap = skip would then be
	// rejected). Services still resolve to the builtin so the field is always set.
	catchUpDefault := d.CatchUp
	if task.Kind.IsService() {
		catchUpDefault = nil
	}
	task.CatchUp = resolveDefault(task.CatchUp, catchUpDefault, model.DefaultCatchUp)
	task.GracefulStop = resolveDefault(task.GracefulStop, d.GracefulStop, DefaultGracefulStop)
	applyInheritedFailures(task, d)
	task.Env = mergeEnv(d.Env, task.Env)
	task.Secrets = mergeEnv(d.Secrets, task.Secrets)
}

// applyInheritedStopSignal inherits stop_signal from defaults, then falls back
// to SIGTERM. An already-SIG-prefixed name is upper-cased so "sigterm" stores
// as "SIGTERM"; a bare or unrecognised value survives unchanged so Validate can
// reject it with a clear error.
func applyInheritedStopSignal(task *model.Task, d Defaults) {
	task.StopSignal = cmp.Or(task.StopSignal, d.StopSignal, DefaultStopSignal)
	up := strings.ToUpper(strings.TrimSpace(task.StopSignal))
	if canonical, ok := model.NormalizeSignalName(task.StopSignal); ok && up == canonical {
		task.StopSignal = canonical
	}
}

// applyInheritedFailures resolves the task's failure matcher. A task with no
// `failures` key (nil spec) inherits the resolved [defaults] matcher wholesale;
// one with a key resolves its spec — a bare list replaces the inherited set, a
// +/- delta adjusts it — against that same [defaults] base. toDefaults always
// leaves the defaults matcher non-nil, so downstream readers never see nil.
// The nil-spec/nil-reasons guard leaves a Task built outside the loader (which
// may set Failures directly) untouched.
func applyInheritedFailures(task *model.Task, d Defaults) {
	if task.FailureSpec == nil {
		if task.Failures.Reasons == nil {
			task.Failures = d.Failures
		}
		return
	}
	task.Failures = task.FailureSpec.Resolve(d.Failures)
	task.FailureSpec = nil
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
	maps.Copy(out, base)
	maps.Copy(out, overlay)
	return out
}

// resolveDefault fills an unset (nil) unit-level pointer from [defaults], then
// from a built-in fallback, without ever colliding an explicit zero at either
// level with "unset" (the whole point of pointer fields like RestartAttempts).
// Always returns non-nil.
func resolveDefault[T any](unit, fromDefaults *T, builtin T) *T {
	if unit != nil {
		return unit
	}
	if fromDefaults != nil {
		return fromDefaults
	}
	v := builtin
	return &v
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
}

func applyServiceDefaults(task *model.Task, d Defaults) {
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
	task.RestartDelay = resolveDefault(task.RestartDelay, d.RestartDelay, DefaultRestartDelay)
	task.RestartBackoff = cmp.Or(task.RestartBackoff, d.RestartBackoff, model.BackoffExponential)
	task.HealthyAfter = resolveDefault(task.HealthyAfter, d.HealthyAfter, DefaultHealthyAfter)
	task.RestartAttempts = resolveDefault(task.RestartAttempts, d.RestartAttempts, DefaultStartRetries)
}
