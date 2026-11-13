// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"reflect"
	"time"
)

// spawnConfig is every Task field a running process keeps using after it
// starts: what is executed and as whom, its environment, and the limits the
// run's timer, stop sequence, log writer, output matcher and health probe were
// armed with. A running service holds the definition it started under, so
// changing any of these takes effect only on a new process.
//
// The list is explicit on purpose: a Task field absent here is treated as read
// live by the daemon and never restarts anything. TestSpawnConfigClassifiesEveryTaskField
// fails when a field is added to Task without being classified.
type spawnConfig struct {
	Run          string
	ExecutionDef ExecutionDef
	Shell        string
	WorkingDir   string
	Umask        string
	RunUser      string
	EnvBase      EnvBase
	Env          map[string]string
	Secrets      map[string]string
	Parameters   []TaskParam

	Timeout      *time.Duration
	GracefulStop *time.Duration
	StopSignal   string
	LogMaxSize   int64
	LogOnFull    string
	Failures     FailureMatcher
	HealthCheck  *Task

	// Instances is not read by a live process, but a reload applies a new count
	// by recycling the service, which also fills or drops the slots.
	Instances int
}

func (t *Task) spawnConfig() spawnConfig {
	return spawnConfig{
		Run: t.Run, ExecutionDef: t.ExecutionDef, Shell: t.Shell, WorkingDir: t.WorkingDir,
		Umask: t.Umask, RunUser: t.RunUser, EnvBase: t.EnvBase, Env: t.Env, Secrets: t.Secrets,
		Parameters: t.Parameters, Timeout: t.Timeout, GracefulStop: t.GracefulStop,
		StopSignal: t.StopSignal, LogMaxSize: t.LogMaxSize, LogOnFull: t.LogOnFull,
		Failures: t.Failures, HealthCheck: t.HealthCheck, Instances: t.Instances,
	}
}

// SpawnDiffers reports whether other differs from t in anything a running
// process of t would have to be restarted to pick up. Everything else (description,
// retention, restart policy and delay, notifications, ...) is read from the live
// definition and applies without a restart.
func (t *Task) SpawnDiffers(other *Task) bool {
	return !reflect.DeepEqual(t.spawnConfig(), other.spawnConfig())
}
