// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"reflect"
	"sort"

	"github.com/runwisp/runwisp/internal/model"
)

// ChangeReason names one way two definitions of the same task differ. A change
// can carry several reasons; the reconciler reads them to decide what to do
// (reschedule a cron entry vs. recycle a service vs. nothing structural).
type ChangeReason string

const (
	// ReasonSchedule: the cron expression or per-task timezone changed.
	ReasonSchedule ChangeReason = "schedule"
	// ReasonKind: a task became a service or vice-versa.
	ReasonKind ChangeReason = "kind"
	// ReasonCommand: what the task runs changed (run script / compose /
	// working dir / shell / umask / user).
	ReasonCommand ChangeReason = "command"
	// ReasonEnv: environment or secrets (inline or file) changed.
	ReasonEnv ChangeReason = "env"
	// ReasonSettings: some other field changed (policies, retention, limits).
	ReasonSettings ChangeReason = "settings"
)

// TaskChange records a task whose definition differs between two configs.
type TaskChange struct {
	Name    string
	Reasons []ChangeReason
}

// Has reports whether the change carries the given reason.
func (c TaskChange) Has(r ChangeReason) bool {
	for _, got := range c.Reasons {
		if got == r {
			return true
		}
	}
	return false
}

// Diff is the set difference between two resolved task sets.
type Diff struct {
	Added   []string
	Removed []string
	Changed []TaskChange
	// Restamped names tasks whose definition is identical but whose derived
	// provenance differs — in practice, a task `runwisp promote` moved out of the
	// staging file into the operator's own config. The live task set has to pick
	// up the new value so the API/UI stop reporting it as staged, but nothing
	// about what runs changed, so it is deliberately not a Changed entry: see
	// ChangeReason's users, which reschedule cron entries and recycle services.
	Restamped []string
}

// IsEmpty reports whether nothing differs. Restamped is excluded on purpose:
// provenance is not a task change, so a reload that only promotes tasks reports
// "no task changes" — which is the honest answer to "did anything I run change?".
func (d Diff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

// DiffTasks compares two fully-resolved task sets (defaults already applied) and
// reports which tasks were added, removed, or changed. It is pure: no clock, no
// I/O, deterministic ordering. Because defaults are merged before this runs, a
// [defaults] edit surfaces as a change on every task it touches.
func DiffTasks(old, updated map[string]*model.Task) Diff {
	var d Diff

	for name, newTask := range updated {
		oldTask, existed := old[name]
		if !existed {
			d.Added = append(d.Added, name)
			continue
		}
		if reasons := changeReasons(oldTask, newTask); len(reasons) > 0 {
			d.Changed = append(d.Changed, TaskChange{Name: name, Reasons: reasons})
			continue
		}
		if oldTask.Staged != newTask.Staged {
			d.Restamped = append(d.Restamped, name)
		}
	}

	for name := range old {
		if _, stillThere := updated[name]; !stillThere {
			d.Removed = append(d.Removed, name)
		}
	}

	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Restamped)
	sort.Slice(d.Changed, func(i, j int) bool { return d.Changed[i].Name < d.Changed[j].Name })
	return d
}

// changeReasons returns the reasons two definitions of one task differ, or nil
// when they are identical. The whole-struct DeepEqual short-circuits the common
// "nothing changed" case; the grouped comparisons then attribute the diff.
//
// Staged is masked out before the comparison because it is derived provenance,
// not definition: `runwisp promote` moves a task's block from the staging file
// into the root without touching a single thing about what it runs. Leaving it in
// would make a promote look like a settings change and bounce a running service
// for no reason. DiffTasks records that case as Restamped instead.
func changeReasons(oldTask, newTask *model.Task) []ChangeReason {
	if sameDefinition(oldTask, newTask) {
		return nil
	}

	var reasons []ChangeReason
	if oldTask.Cron != newTask.Cron || oldTask.Timezone != newTask.Timezone {
		reasons = append(reasons, ReasonSchedule)
	}
	if oldTask.Kind != newTask.Kind {
		reasons = append(reasons, ReasonKind)
	}
	if commandChanged(oldTask, newTask) {
		reasons = append(reasons, ReasonCommand)
	}
	if envChanged(oldTask, newTask) {
		reasons = append(reasons, ReasonEnv)
	}
	// A diff that none of the specific groups explain (policy, retention,
	// concurrency, …) is still a real change — surface it so the operator and
	// the reconciler don't treat it as a no-op.
	if len(reasons) == 0 {
		reasons = append(reasons, ReasonSettings)
	}
	return reasons
}

// sameDefinition reports whether two resolved definitions of one task are
// identical apart from derived provenance. The structs are copied so masking
// Staged never mutates the caller's config; the copies share the same maps and
// slices, which DeepEqual compares by value anyway.
func sameDefinition(oldTask, newTask *model.Task) bool {
	a, b := *oldTask, *newTask
	a.Staged, b.Staged = false, false
	return reflect.DeepEqual(&a, &b)
}

func commandChanged(oldTask, newTask *model.Task) bool {
	return oldTask.Run != newTask.Run ||
		oldTask.Shell != newTask.Shell ||
		oldTask.WorkingDir != newTask.WorkingDir ||
		oldTask.Umask != newTask.Umask ||
		oldTask.RunUser != newTask.RunUser ||
		!reflect.DeepEqual(oldTask.ExecutionDef, newTask.ExecutionDef) ||
		!reflect.DeepEqual(oldTask.Parameters, newTask.Parameters)
}

func envChanged(oldTask, newTask *model.Task) bool {
	return oldTask.EnvBase != newTask.EnvBase ||
		oldTask.EnvFile != newTask.EnvFile ||
		oldTask.SecretsFile != newTask.SecretsFile ||
		!reflect.DeepEqual(oldTask.Env, newTask.Env) ||
		!reflect.DeepEqual(oldTask.Secrets, newTask.Secrets)
}

// ToResult projects the diff onto the API/CLI wire shape.
func (d Diff) ToResult() model.ReloadResult {
	res := model.ReloadResult{
		Added:   d.Added,
		Removed: d.Removed,
	}
	for _, c := range d.Changed {
		reasons := make([]string, len(c.Reasons))
		for i, r := range c.Reasons {
			reasons[i] = string(r)
		}
		res.Changed = append(res.Changed, model.ReloadTaskChange{Name: c.Name, Reasons: reasons})
	}
	return res
}
