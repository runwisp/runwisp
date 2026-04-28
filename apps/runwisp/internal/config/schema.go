// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"github.com/runwisp/runwisp/internal/model"
)

// Config is the in-memory representation of runwisp.toml after load + defaults.
type Config struct {
	Tasks    []model.Task
	Defaults Defaults
	Storage  Storage
	Daemon   Daemon
}

// Daemon holds daemon-wide toggles.
type Daemon struct {
	AllowCloudDispatch bool `toml:"allow_cloud_dispatch,omitempty"`
}

// Defaults provides fallback values applied to every task.
type Defaults struct {
	Timeout    string `toml:"timeout,omitempty"`
	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`
	KeepRuns   int    `toml:"keep_runs,omitempty"`
	KeepFor    string `toml:"keep_for,omitempty"`
}

// Storage controls global disk-usage limits for log files.
type Storage struct {
	MaxSize      string `toml:"max_size,omitempty"`
	MinFreeSpace string `toml:"min_free_space,omitempty"`

	MaxSizeBytes      int64 `toml:"-"`
	MinFreeSpaceBytes int64 `toml:"-"`
}

// IsCloudShellEnabled reports whether the daemon accepts peer-dispatched shell tasks.
func (cfg *Config) IsCloudShellEnabled() bool {
	return cfg.Daemon.AllowCloudDispatch
}

// MaxServiceInstances caps the number of replicas a single service can request.
const MaxServiceInstances = 64

// taskWire is the over-the-wire task shape used only during TOML decoding.
// It exists so api_trigger can be distinguished between "absent" (nil, default true)
// and "explicitly false" (&false).
type taskWire struct {
	Group       string `toml:"group,omitempty"`
	Description string `toml:"description,omitempty"`

	Cron       string                `toml:"cron,omitempty"`
	APITrigger *bool                 `toml:"api_trigger,omitempty"`
	CatchUp    model.MissedRunPolicy `toml:"catch_up,omitempty"`

	Timeout     string                  `toml:"timeout,omitempty"`
	Restart     model.RestartPolicy     `toml:"restart,omitempty"`
	Parallelism int                     `toml:"parallelism,omitempty"`
	OnOverlap   model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`

	// Instances is rejected on [tasks.*]; carried as a pointer so the validator
	// can distinguish "unset" from "explicitly zero".
	Instances *int `toml:"instances,omitempty"`

	RetryAttempts int    `toml:"retry_attempts,omitempty"`
	RetryDelay    string `toml:"retry_delay,omitempty"`
	RetryBackoff  string `toml:"retry_backoff,omitempty"`

	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`

	KeepRuns int    `toml:"keep_runs,omitempty"`
	KeepFor  string `toml:"keep_for,omitempty"`

	Run string `toml:"run,omitempty"`
}

// serviceWire is the over-the-wire shape for [services.*] entries. Cron and
// catch_up are intentionally omitted — services are not cron-driven.
type serviceWire struct {
	Group       string `toml:"group,omitempty"`
	Description string `toml:"description,omitempty"`

	APITrigger *bool `toml:"api_trigger,omitempty"`

	Timeout     string                  `toml:"timeout,omitempty"`
	OnOverlap   model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`
	Instances   int                     `toml:"instances,omitempty"`
	Parallelism int                     `toml:"parallelism,omitempty"`

	RestartDelay   string `toml:"restart_delay,omitempty"`
	RestartBackoff string `toml:"restart_backoff,omitempty"`

	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`

	KeepRuns int    `toml:"keep_runs,omitempty"`
	KeepFor  string `toml:"keep_for,omitempty"`

	Run string `toml:"run,omitempty"`
}

// tomlConfig is the over-the-wire config shape used only during TOML decoding.
type tomlConfig struct {
	Daemon   Daemon                  `toml:"daemon,omitempty"`
	Storage  Storage                 `toml:"storage,omitempty"`
	Defaults Defaults                `toml:"defaults,omitempty"`
	Tasks    map[string]*taskWire    `toml:"tasks,omitempty"`
	Services map[string]*serviceWire `toml:"services,omitempty"`
}

func (w *taskWire) toTask(name string) model.Task {
	apiTrigger := true
	if w.APITrigger != nil {
		apiTrigger = *w.APITrigger
	}
	return model.Task{
		Name:          name,
		Kind:          model.KindTask,
		Group:         w.Group,
		Description:   w.Description,
		Cron:          w.Cron,
		APITrigger:    apiTrigger,
		CatchUp:       w.CatchUp,
		Timeout:       w.Timeout,
		Restart:       w.Restart,
		Parallelism:   w.Parallelism,
		OnOverlap:     w.OnOverlap,
		RetryAttempts: w.RetryAttempts,
		RetryDelay:    w.RetryDelay,
		RetryBackoff:  w.RetryBackoff,
		LogMaxSize:    w.LogMaxSize,
		LogOnFull:     w.LogOnFull,
		KeepRuns:      w.KeepRuns,
		KeepFor:       w.KeepFor,
		Run:           w.Run,
	}
}

func (w *serviceWire) toTask(name string) model.Task {
	apiTrigger := true
	if w.APITrigger != nil {
		apiTrigger = *w.APITrigger
	}
	return model.Task{
		Name:           name,
		Kind:           model.KindService,
		Group:          w.Group,
		Description:    w.Description,
		APITrigger:     apiTrigger,
		Timeout:        w.Timeout,
		Restart:        model.RestartAlways,
		Parallelism:    w.Parallelism,
		OnOverlap:      w.OnOverlap,
		Instances:      w.Instances,
		RestartDelay:   w.RestartDelay,
		RestartBackoff: w.RestartBackoff,
		LogMaxSize:     w.LogMaxSize,
		LogOnFull:      w.LogOnFull,
		KeepRuns:       w.KeepRuns,
		KeepFor:        w.KeepFor,
		Run:            w.Run,
	}
}
