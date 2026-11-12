// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// buildTaskDefinitions converts the daemon's task set into tasks:sync rows,
// sorted by name. daemonTZ is the zone cron tasks without their own timezone
// run in.
func buildTaskDefinitions(tasks map[string]*model.Task, daemonTZ string) []protocol.TaskDefinition {
	defs := make([]protocol.TaskDefinition, 0, len(tasks))
	for _, name := range slices.Sorted(maps.Keys(tasks)) {
		if t := tasks[name]; t != nil {
			if def, ok := buildTaskDefinition(t, daemonTZ); ok {
				defs = append(defs, def)
			}
		}
	}
	return defs
}

// buildTaskDefinition converts one task. ok is false when it has no execution
// definition or it can't be marshalled.
func buildTaskDefinition(t *model.Task, daemonTZ string) (protocol.TaskDefinition, bool) {
	execDef := t.ResolvedExecutionDef()
	if execDef == nil {
		return protocol.TaskDefinition{}, false
	}
	spec, err := model.MarshalExecutionDef(execDef)
	if err != nil {
		slog.Warn("Failed to marshal execution", "task", t.Name, "err", err)
		return protocol.TaskDefinition{}, false
	}
	kind := protocol.TaskKindTask
	if t.Kind.IsService() {
		kind = protocol.TaskKindService
	}
	def := protocol.TaskDefinition{
		Name:          t.Name,
		Kind:          kind,
		Group:         t.Group,
		Description:   t.Description,
		Enabled:       true,
		Spec:          spec,
		TimeoutMs:     durationToMillis(t.TimeoutValue()),
		Options:       runOptions(t),
		MaxConcurrent: max(t.MaxConcurrent, 0),
		OnOverlap:     protocol.OverlapPolicy(t.OnOverlap),
		RestartPolicy: string(cmp.Or(t.Restart, model.RestartNever)),
		MaxRetries:    max(t.RetryAttempts, 0),
		RetryDelayMs:  durationToMillis(t.RetryDelayValue()),
		RetryBackoff:  protocol.Backoff(t.RetryBackoff),
		Params:        taskParams(t.Parameters),
		Compose:       composeRef(t.Compose),
	}
	if t.Kind.IsService() {
		// Services carry no cron, so they never emit a schedule.
		def.Service = serviceSettings(t)
	} else {
		applyScheduleFields(&def, t, daemonTZ)
	}
	return def, true
}

// runOptions reports the task's per-run knobs, nil when none is set.
func runOptions(t *model.Task) *protocol.RunOptions {
	opts := protocol.RunOptions{
		Env:            t.Env,
		GracefulStopMs: durationToMillis(t.GracefulStopValue()),
		LogMaxBytes:    max(t.LogMaxSize, 0),
		LogOnFull:      t.LogOnFull,
	}
	if len(opts.Env) == 0 && opts.GracefulStopMs == 0 && opts.LogMaxBytes == 0 && opts.LogOnFull == "" {
		return nil
	}
	return &opts
}

// applyScheduleFields fills the cron-task fields: the schedule (disabled for
// autostart = false, the tasks the daemon starts paused), jitter, and the
// catch-up policy.
func applyScheduleFields(def *protocol.TaskDefinition, t *model.Task, daemonTZ string) {
	cron := strings.TrimSpace(t.Cron)
	if cron == "" {
		return
	}
	def.Schedules = []protocol.TaskSchedule{{
		Cron:     cron,
		Timezone: cmp.Or(t.Timezone, daemonTZ),
		Enabled:  !t.StartsPaused(),
	}}
	// nil means neither the task nor [defaults] sets jitter: send nothing and
	// let the station keep its own. An explicit 0 is sent, as "no jitter".
	if t.Jitter != nil {
		def.JitterMs = new(durationToMillis(*t.Jitter))
	}
	// Config load always resolves catch_up (built-in 1 when neither the task nor
	// [defaults] sets it), so a loaded cron task always reports one.
	if t.CatchUp != nil {
		def.CatchUp = catchUpPolicy(*t.CatchUp)
		if *t.CatchUp > 1 {
			def.MaxCatchUpRuns = *t.CatchUp
		}
	}
}

// catchUpPolicy maps the TOML catch_up count to the station's policy name:
// 0 re-runs nothing, 1 only the most recent missed tick, N > 1 up to N of them
// (the cap travels separately as maxCatchUpRuns).
func catchUpPolicy(n int) string {
	switch {
	case n <= 0:
		return "skip"
	case n == 1:
		return "latest"
	default:
		return "all"
	}
}

// taskParams converts declared params into the wire map keyed by
// TaskParam.Key.
func taskParams(params []model.TaskParam) map[string]*protocol.TaskParam {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]*protocol.TaskParam, len(params))
	for _, p := range params {
		out[p.Key] = &protocol.TaskParam{
			Kind:        string(p.Kind),
			Type:        p.Type,
			Required:    p.Required,
			Default:     p.Default,
			Choices:     p.Choices,
			AllowCustom: p.AllowCustom,
			Description: p.Description,
		}
	}
	return out
}

// serviceSettings reports a service's supervision settings.
func serviceSettings(t *model.Task) *protocol.ServiceSettings {
	return &protocol.ServiceSettings{
		Instances:           max(t.Instances, 0),
		Autostart:           t.Autostart,
		RestartDelayMs:      durationPtrToMillis(t.RestartDelay),
		RestartBackoff:      protocol.Backoff(t.RestartBackoff),
		BackoffResetAfterMs: durationPtrToMillis(t.HealthyAfter),
	}
}

func composeRef(c *model.TaskComposeRef) *protocol.ComposeRef {
	if c == nil {
		return nil
	}
	return &protocol.ComposeRef{File: c.File, ProjectName: c.ProjectName, Service: c.Service}
}

// tasksHash identifies a task catalog: hello carries it so a station that
// already has this exact catalog skips the tasks:sync.
func tasksHash(defs []protocol.TaskDefinition) string {
	payload, err := json.Marshal(defs)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:8])
}

func durationToMillis(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d.Milliseconds())
}

// durationPtrToMillis is durationToMillis for the *time.Duration fields that
// tell an explicit "0s" apart from unset. The wire can't, so both report 0.
func durationPtrToMillis(d *time.Duration) int {
	if d == nil {
		return 0
	}
	return durationToMillis(*d)
}
