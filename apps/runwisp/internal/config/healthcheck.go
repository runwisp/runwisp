// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"cmp"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// Built-in fallbacks for a [services.*.health_check] probe. A probe resolves
// like any task (its own value, else [defaults], else a built-in); these are
// the built-ins where a task's would be wrong for a probe: a task has no
// schedule, no timeout, and no retries by default, but a probe with no
// schedule never runs, one with no timeout can hang unnoticed, and one with no
// retries restarts the service on a single blip.
const (
	DefaultHealthCheckCron          = "@every 10s"
	DefaultHealthCheckTimeout       = 5 * time.Second
	DefaultHealthCheckRetryAttempts = 2
)

// healthCheckSuffix names a service's probe task: [services.api.health_check]
// is the task "api.health_check" in error messages.
const healthCheckSuffix = ".health_check"

// units yields every executable unit the config defines, paired with the name
// of the entry that declared it: each task and service as itself, and each
// service's health_check probe under its service. The owner is what the
// probe's relative paths (env_file, working_dir, compose_file) resolve
// against, so load steps that touch per-unit files iterate this rather than
// cfg.Tasks and never forget the probes.
func (c *Config) units() iter.Seq2[string, *model.Task] {
	return func(yield func(string, *model.Task) bool) {
		for i := range c.Tasks {
			task := &c.Tasks[i]
			if !yield(task.Name, task) {
				return
			}
			if task.HealthCheck != nil && !yield(task.Name, task.HealthCheck) {
				return
			}
		}
	}
}

// applyHealthCheckDefaults resolves a service's probe once the service itself
// is resolved. The probe takes the task defaulting path wholesale, plus what
// only a probe has: the service's env/secrets beneath its own (a host probe
// only — see serviceWire.healthCheckTask), the probe built-ins, and no
// graceful stop — a probe past its timeout is killed outright, not asked to
// wind down.
func applyHealthCheckDefaults(svc *model.Task, d Defaults) {
	probe := svc.HealthCheck
	if probe == nil {
		return
	}
	// Merge the service's env first: it already carries [defaults] beneath it,
	// so applyInheritedDefaults' own [defaults] merge below can't override it.
	if probe.Compose == nil {
		probe.Env = mergeEnv(svc.Env, probe.Env)
		probe.Secrets = mergeEnv(svc.Secrets, probe.Secrets)
	}
	applyTaskDefaults(probe)
	applyInheritedDefaults(probe, d)
	// A probe with no timezone of its own runs in the daemon zone, which the
	// health watcher reads live; it is not copied in here, so a [daemon]
	// timezone reload doesn't count as a probe change and recycle the service.
	probe.Cron = cmp.Or(probe.Cron, DefaultHealthCheckCron)
	if probe.Timeout == nil {
		probe.Timeout = new(DefaultHealthCheckTimeout)
	}
	probe.GracefulStop = new(time.Duration(0))
}

// validateHealthCheck validates a service's probe with the same per-unit
// checks a task gets (validateUnit — identity is the service's, already
// checked), plus the rules only a probe has.
func validateHealthCheck(svc *model.Task) error {
	probe := svc.HealthCheck
	if probe == nil {
		return nil
	}
	if strings.TrimSpace(probe.Run) == "" {
		return fmt.Errorf("health_check for service %s: run is required", svc.Name)
	}
	if probe.TimeoutValue() <= 0 {
		return fmt.Errorf("invalid timeout for health_check of service %s: must be a positive duration; a probe with no timeout could hang forever unnoticed", svc.Name)
	}
	// With a check, healthy_after is the deadline for the first pass; a zero
	// deadline would kill every instance before its first probe.
	if OrDefault(svc.HealthyAfter, DefaultHealthyAfter) <= 0 {
		return fmt.Errorf("invalid healthy_after for service %s: with a health_check it is the deadline for the first passing check and must be a positive duration", svc.Name)
	}
	return validateUnit(probe)
}
