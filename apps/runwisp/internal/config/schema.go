// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"time"

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
//
// All durations / sizes are parsed from their TOML string form (e.g. "1h",
// "100mb") at config load time and stored as native Go types.
type Defaults struct {
	Timeout    time.Duration
	LogMaxSize int64
	LogOnFull  string
	KeepRuns   int
	KeepFor    time.Duration
}

// Storage controls global disk-usage limits for log files.
type Storage struct {
	MaxSize      int64
	MinFreeSpace int64
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

// defaultsWire mirrors [defaults] before parsing.
type defaultsWire struct {
	Timeout    string `toml:"timeout,omitempty"`
	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`
	KeepRuns   int    `toml:"keep_runs,omitempty"`
	KeepFor    string `toml:"keep_for,omitempty"`
}

// storageWire mirrors [storage] before parsing.
type storageWire struct {
	MaxSize      string `toml:"max_size,omitempty"`
	MinFreeSpace string `toml:"min_free_space,omitempty"`
}

// tomlConfig is the over-the-wire config shape used only during TOML decoding.
type tomlConfig struct {
	Daemon   Daemon                  `toml:"daemon,omitempty"`
	Storage  storageWire             `toml:"storage,omitempty"`
	Defaults defaultsWire            `toml:"defaults,omitempty"`
	Tasks    map[string]*taskWire    `toml:"tasks,omitempty"`
	Services map[string]*serviceWire `toml:"services,omitempty"`
}

func (w *taskWire) toTask(name string) (model.Task, error) {
	apiTrigger := true
	if w.APITrigger != nil {
		apiTrigger = *w.APITrigger
	}
	timeout, err := parseTaskDuration(name, "timeout", w.Timeout)
	if err != nil {
		return model.Task{}, err
	}
	retryDelay, err := parseTaskDuration(name, "retry_delay", w.RetryDelay)
	if err != nil {
		return model.Task{}, err
	}
	keepFor, err := parseTaskKeepFor(name, w.KeepFor)
	if err != nil {
		return model.Task{}, err
	}
	logMaxSize, err := parseTaskByteSize(name, "log_max_size", w.LogMaxSize)
	if err != nil {
		return model.Task{}, err
	}
	return model.Task{
		Name:          name,
		Kind:          model.KindTask,
		Group:         w.Group,
		Description:   w.Description,
		Cron:          w.Cron,
		APITrigger:    apiTrigger,
		CatchUp:       w.CatchUp,
		Timeout:       timeout,
		Restart:       w.Restart,
		Parallelism:   w.Parallelism,
		OnOverlap:     w.OnOverlap,
		RetryAttempts: w.RetryAttempts,
		RetryDelay:    retryDelay,
		RetryBackoff:  w.RetryBackoff,
		LogMaxSize:    logMaxSize,
		LogOnFull:     w.LogOnFull,
		KeepRuns:      w.KeepRuns,
		KeepFor:       keepFor,
		Run:           w.Run,
	}, nil
}

func (w *serviceWire) toTask(name string) (model.Task, error) {
	apiTrigger := true
	if w.APITrigger != nil {
		apiTrigger = *w.APITrigger
	}
	timeout, err := parseTaskDuration(name, "timeout", w.Timeout)
	if err != nil {
		return model.Task{}, err
	}
	restartDelay, err := parseTaskDuration(name, "restart_delay", w.RestartDelay)
	if err != nil {
		return model.Task{}, err
	}
	keepFor, err := parseTaskKeepFor(name, w.KeepFor)
	if err != nil {
		return model.Task{}, err
	}
	logMaxSize, err := parseTaskByteSize(name, "log_max_size", w.LogMaxSize)
	if err != nil {
		return model.Task{}, err
	}
	return model.Task{
		Name:           name,
		Kind:           model.KindService,
		Group:          w.Group,
		Description:    w.Description,
		APITrigger:     apiTrigger,
		Timeout:        timeout,
		Restart:        model.RestartAlways,
		Parallelism:    w.Parallelism,
		OnOverlap:      w.OnOverlap,
		Instances:      w.Instances,
		RestartDelay:   restartDelay,
		RestartBackoff: w.RestartBackoff,
		LogMaxSize:     logMaxSize,
		LogOnFull:      w.LogOnFull,
		KeepRuns:       w.KeepRuns,
		KeepFor:        keepFor,
		Run:            w.Run,
	}, nil
}

func (w *defaultsWire) toDefaults() (Defaults, error) {
	timeout, err := parseScopedDuration("defaults.timeout", w.Timeout)
	if err != nil {
		return Defaults{}, err
	}
	keepFor, err := parseScopedKeepFor("defaults.keep_for", w.KeepFor)
	if err != nil {
		return Defaults{}, err
	}
	logMaxSize, err := parseScopedByteSize("defaults.log_max_size", w.LogMaxSize)
	if err != nil {
		return Defaults{}, err
	}
	return Defaults{
		Timeout:    timeout,
		LogMaxSize: logMaxSize,
		LogOnFull:  w.LogOnFull,
		KeepRuns:   w.KeepRuns,
		KeepFor:    keepFor,
	}, nil
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
