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
	str2duration "github.com/xhit/go-str2duration/v2"
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

	names := make([]string, 0, len(raw.Tasks))
	for name := range raw.Tasks {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("task name is required")
		}
		names = append(names, name)
	}
	sort.Strings(names)

	tasks := make([]model.Task, 0, len(names))
	for _, name := range names {
		tasks = append(tasks, raw.Tasks[name].toTask(name))
	}

	return &Config{
		Tasks:    tasks,
		Defaults: raw.Defaults,
		Storage:  raw.Storage,
		Daemon:   raw.Daemon,
	}, nil
}

func validateLogOnFull(value string) error {
	if value == "" {
		return nil
	}
	switch value {
	case model.LogOverflowDropNew, model.LogOverflowDropOld, model.LogOverflowKillTask:
		return nil
	default:
		return fmt.Errorf("invalid value %q: must be drop_new, drop_old, or kill_task", value)
	}
}

func validateCatchUp(value string) error {
	if value == "" {
		return nil
	}
	switch value {
	case "latest", "all", "skip":
		return nil
	default:
		return fmt.Errorf("invalid value %q: must be latest, all, or skip", value)
	}
}

func validateRestart(value model.RestartPolicy) error {
	switch value {
	case "", model.RestartNever, model.RestartAlways, model.RestartOnFailure:
		return nil
	default:
		return fmt.Errorf("invalid value %q: must be never, always, or on_failure", value)
	}
}

func validateRetryBackoff(value string) error {
	if value == "" {
		return nil
	}
	switch value {
	case "linear", "exponential":
		return nil
	default:
		return fmt.Errorf("invalid value %q: must be linear or exponential", value)
	}
}

func validateKeepFor(raw string) error {
	if raw == "" {
		return nil
	}
	if _, err := str2duration.ParseDuration(raw); err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	return nil
}

// Validate checks for invalid configuration values.
func Validate(cfg *Config) error {
	if cfg.Defaults.LogMaxSize != "" {
		if _, err := ParseByteSize(cfg.Defaults.LogMaxSize); err != nil {
			return fmt.Errorf("invalid defaults.log_max_size: %v", err)
		}
	}
	if err := validateLogOnFull(cfg.Defaults.LogOnFull); err != nil {
		return fmt.Errorf("invalid defaults.log_on_full: %w", err)
	}
	if cfg.Defaults.Timeout != "" {
		if _, err := time.ParseDuration(cfg.Defaults.Timeout); err != nil {
			return fmt.Errorf("invalid defaults.timeout: %v", err)
		}
	}
	if cfg.Defaults.KeepRuns < 0 {
		return fmt.Errorf("invalid defaults.keep_runs: must be non-negative")
	}
	if err := validateKeepFor(cfg.Defaults.KeepFor); err != nil {
		return fmt.Errorf("invalid defaults.keep_for: %w", err)
	}

	if cfg.Storage.MaxSize != "" {
		if _, err := ParseByteSize(cfg.Storage.MaxSize); err != nil {
			return fmt.Errorf("invalid storage.max_size: %v", err)
		}
	}
	if cfg.Storage.MinFreeSpace != "" {
		if _, err := ParseByteSize(cfg.Storage.MinFreeSpace); err != nil {
			return fmt.Errorf("invalid storage.min_free_space: %v", err)
		}
	}

	seen := make(map[string]struct{}, len(cfg.Tasks))
	for _, task := range cfg.Tasks {
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

		if task.OnOverlap != model.PolicyQueue && task.OnOverlap != model.PolicySkip && task.OnOverlap != model.PolicyTerminate {
			return fmt.Errorf(
				"invalid on_overlap for task %s: %s (must be queue, skip, or terminate)",
				task.Name,
				task.OnOverlap,
			)
		}

		if task.Timeout != "" {
			if _, err := time.ParseDuration(task.Timeout); err != nil {
				return fmt.Errorf("invalid timeout for task %s: %v", task.Name, err)
			}
		}

		if err := validateRestart(task.Restart); err != nil {
			return fmt.Errorf("invalid restart for task %s: %w", task.Name, err)
		}

		if task.RetryAttempts < 0 {
			return fmt.Errorf("invalid retry_attempts for task %s: must be non-negative", task.Name)
		}
		if task.RetryDelay != "" {
			if _, err := time.ParseDuration(task.RetryDelay); err != nil {
				return fmt.Errorf("invalid retry_delay for task %s: %v", task.Name, err)
			}
		}
		if err := validateRetryBackoff(task.RetryBackoff); err != nil {
			return fmt.Errorf("invalid retry_backoff for task %s: %w", task.Name, err)
		}

		if task.LogMaxSize != "" {
			if _, err := ParseByteSize(task.LogMaxSize); err != nil {
				return fmt.Errorf("invalid log_max_size for task %s: %v", task.Name, err)
			}
		}
		if err := validateLogOnFull(task.LogOnFull); err != nil {
			return fmt.Errorf("invalid log_on_full for task %s: %w", task.Name, err)
		}

		if task.KeepRuns < 0 {
			return fmt.Errorf("invalid keep_runs for task %s: must be non-negative", task.Name)
		}
		if err := validateKeepFor(task.KeepFor); err != nil {
			return fmt.Errorf("invalid keep_for for task %s: %w", task.Name, err)
		}

		if err := validateCatchUp(string(task.CatchUp)); err != nil {
			return fmt.Errorf("invalid catch_up for task %s: %w", task.Name, err)
		}
	}

	return nil
}

// ApplyDefaults fills in zero-valued fields with sensible defaults.
func ApplyDefaults(cfg *Config) {
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]

		if task.Group == "" {
			task.Group = "Tasks"
		}
		if task.Parallelism == 0 {
			task.Parallelism = 1
		}
		if task.OnOverlap == "" {
			task.OnOverlap = model.PolicyQueue
		}
		if task.CatchUp == "" {
			task.CatchUp = model.MissedRunLatest
		}

		if task.Timeout == "" && cfg.Defaults.Timeout != "" {
			task.Timeout = cfg.Defaults.Timeout
		}
		if task.LogMaxSize == "" && cfg.Defaults.LogMaxSize != "" {
			task.LogMaxSize = cfg.Defaults.LogMaxSize
		}
		if task.LogOnFull == "" && cfg.Defaults.LogOnFull != "" {
			task.LogOnFull = cfg.Defaults.LogOnFull
		}
		if task.KeepRuns == 0 && cfg.Defaults.KeepRuns > 0 {
			task.KeepRuns = cfg.Defaults.KeepRuns
		}
		if task.KeepFor == "" && cfg.Defaults.KeepFor != "" {
			task.KeepFor = cfg.Defaults.KeepFor
		}

		if task.LogMaxSize == "" {
			task.LogMaxSize = "100mb"
		}
		if task.LogOnFull == "" {
			task.LogOnFull = model.LogOverflowDropOld
		}

		if bytes, err := ParseByteSize(task.LogMaxSize); err == nil {
			task.LogMaxSizeBytes = bytes
		}
	}

	if bytes, err := ParseByteSize(cfg.Storage.MaxSize); err == nil {
		cfg.Storage.MaxSizeBytes = bytes
	}
	if bytes, err := ParseByteSize(cfg.Storage.MinFreeSpace); err == nil {
		cfg.Storage.MinFreeSpaceBytes = bytes
	}
}
