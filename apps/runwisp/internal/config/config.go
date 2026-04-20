// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	str2duration "github.com/xhit/go-str2duration/v2"
	"gopkg.in/yaml.v3"
)

// Config represents the native runwisp.yaml configuration model.
type Config struct {
	Tasks    []model.Task `yaml:"tasks"`
	Defaults Defaults     `yaml:"defaults,omitempty"`
	Storage  Storage      `yaml:"storage,omitempty"`
	Daemon   Daemon       `yaml:"daemon,omitempty"`
}

type Daemon struct {
	CloudShellTasks bool `yaml:"cloudShellTasks,omitempty"`
}

func (d *Daemon) UnmarshalYAML(value *yaml.Node) error {
	type rawDaemon Daemon
	var raw rawDaemon
	if err := model.ValidateMappingKeys(value, "cloudShellTasks"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*d = Daemon(raw)
	return nil
}

// Defaults provides fallback values applied to every task.
type Defaults struct {
	Timeout   string              `yaml:"timeout,omitempty"`
	Logs      model.TaskLogs      `yaml:"logs,omitempty"`
	Retention model.TaskRetention `yaml:"retention,omitempty"`
}

func (d *Defaults) UnmarshalYAML(value *yaml.Node) error {
	type rawDefaults Defaults
	var raw rawDefaults
	if err := model.ValidateMappingKeys(value, "timeout", "logs", "retention"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*d = Defaults(raw)
	return nil
}

// Storage controls global disk-usage limits for log files.
type Storage struct {
	MaxSize      string `yaml:"maxSize,omitempty"`
	MinFreeSpace string `yaml:"minFreeSpace,omitempty"`

	MaxSizeBytes      int64 `yaml:"-"`
	MinFreeSpaceBytes int64 `yaml:"-"`
}

func (s *Storage) UnmarshalYAML(value *yaml.Node) error {
	type rawStorage Storage
	var raw rawStorage
	if err := model.ValidateMappingKeys(value, "maxSize", "minFreeSpace"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*s = Storage(raw)
	return nil
}

func (cfg *Config) IsCloudShellEnabled() bool {
	return cfg.Daemon.CloudShellTasks
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var parsed fileConfig
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	cfg := parsed.intoConfig()
	ApplyDefaults(cfg)
	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func validateLogOverflow(value string) error {
	if value == "" {
		return nil
	}
	switch value {
	case "tail", "head", "kill":
		return nil
	default:
		return fmt.Errorf("invalid value %q: must be tail, head, or kill", value)
	}
}

func validateMissedRunPolicy(value string) error {
	if value == "" {
		return nil
	}
	switch value {
	case "latest", "all", "none":
		return nil
	default:
		return fmt.Errorf("invalid value %q: must be latest, all, or none", value)
	}
}

func validateRestartPolicy(value model.RestartPolicy) error {
	switch value {
	case "", model.RestartNever, model.RestartAlways, model.RestartOnFailure:
		return nil
	default:
		return fmt.Errorf("invalid value %q: must be never, always, or on-failure", value)
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

func validateRetentionAge(raw string) error {
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
	if cfg.Defaults.Logs.MaxSize != "" {
		if _, err := ParseByteSize(cfg.Defaults.Logs.MaxSize); err != nil {
			return fmt.Errorf("invalid defaults.logs.maxSize: %v", err)
		}
	}
	if err := validateLogOverflow(cfg.Defaults.Logs.Overflow); err != nil {
		return fmt.Errorf("invalid defaults.logs.overflow: %w", err)
	}
	if cfg.Defaults.Timeout != "" {
		if _, err := time.ParseDuration(cfg.Defaults.Timeout); err != nil {
			return fmt.Errorf("invalid defaults.timeout: %v", err)
		}
	}
	if cfg.Defaults.Retention.Runs < 0 {
		return fmt.Errorf("invalid defaults.retention.runs: must be non-negative")
	}
	if err := validateRetentionAge(cfg.Defaults.Retention.Age); err != nil {
		return fmt.Errorf("invalid defaults.retention.age: %w", err)
	}

	if cfg.Storage.MaxSize != "" {
		if _, err := ParseByteSize(cfg.Storage.MaxSize); err != nil {
			return fmt.Errorf("invalid storage.maxSize: %v", err)
		}
	}
	if cfg.Storage.MinFreeSpace != "" {
		if _, err := ParseByteSize(cfg.Storage.MinFreeSpace); err != nil {
			return fmt.Errorf("invalid storage.minFreeSpace: %v", err)
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

		if task.Execution.Concurrency.Limit <= 0 {
			return fmt.Errorf("invalid execution.concurrency.limit for task %s: must be greater than zero", task.Name)
		}

		policy := task.Execution.Concurrency.Policy
		if policy != model.PolicyQueue && policy != model.PolicySkip && policy != model.PolicyTerminate {
			return fmt.Errorf(
				"invalid execution.concurrency.policy for task %s: %s (must be queue, skip, or terminate)",
				task.Name,
				task.Execution.Concurrency.Policy,
			)
		}

		if task.Execution.Timeout != "" {
			if _, err := time.ParseDuration(task.Execution.Timeout); err != nil {
				return fmt.Errorf("invalid execution.timeout for task %s: %v", task.Name, err)
			}
		}

		if err := validateRestartPolicy(task.Execution.Restart); err != nil {
			return fmt.Errorf("invalid execution.restart for task %s: %w", task.Name, err)
		}

		if task.Retry.Limit < 0 {
			return fmt.Errorf("invalid retry.limit for task %s: must be non-negative", task.Name)
		}
		if task.Retry.DelaySec < 0 {
			return fmt.Errorf("invalid retry.delaySec for task %s: must be non-negative", task.Name)
		}
		if err := validateRetryBackoff(task.Retry.Backoff); err != nil {
			return fmt.Errorf("invalid retry.backoff for task %s: %w", task.Name, err)
		}

		if task.Logs.MaxSize != "" {
			if _, err := ParseByteSize(task.Logs.MaxSize); err != nil {
				return fmt.Errorf("invalid logs.maxSize for task %s: %v", task.Name, err)
			}
		}
		if err := validateLogOverflow(task.Logs.Overflow); err != nil {
			return fmt.Errorf("invalid logs.overflow for task %s: %w", task.Name, err)
		}

		if task.Retention.Runs < 0 {
			return fmt.Errorf("invalid retention.runs for task %s: must be non-negative", task.Name)
		}
		if err := validateRetentionAge(task.Retention.Age); err != nil {
			return fmt.Errorf("invalid retention.age for task %s: %w", task.Name, err)
		}

		if err := validateMissedRunPolicy(string(task.Trigger.Catchup)); err != nil {
			return fmt.Errorf("invalid trigger.catchup for task %s: %w", task.Name, err)
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

		if task.Execution.Concurrency.Limit == 0 {
			task.Execution.Concurrency.Limit = 1
		}
		if task.Execution.Concurrency.Policy == "" {
			task.Execution.Concurrency.Policy = model.PolicyQueue
		}
		if task.Trigger.API == nil {
			v := true
			task.Trigger.API = &v
		}
		if task.Trigger.Catchup == "" {
			task.Trigger.Catchup = model.MissedRunLatest
		}

		if task.Execution.Timeout == "" && cfg.Defaults.Timeout != "" {
			task.Execution.Timeout = cfg.Defaults.Timeout
		}
		if task.Logs.MaxSize == "" && cfg.Defaults.Logs.MaxSize != "" {
			task.Logs.MaxSize = cfg.Defaults.Logs.MaxSize
		}
		if task.Logs.Overflow == "" && cfg.Defaults.Logs.Overflow != "" {
			task.Logs.Overflow = cfg.Defaults.Logs.Overflow
		}
		if task.Retention.Runs == 0 && cfg.Defaults.Retention.Runs > 0 {
			task.Retention.Runs = cfg.Defaults.Retention.Runs
		}
		if task.Retention.Age == "" && cfg.Defaults.Retention.Age != "" {
			task.Retention.Age = cfg.Defaults.Retention.Age
		}

		if task.Logs.MaxSize == "" {
			task.Logs.MaxSize = "100mb"
		}
		if task.Logs.Overflow == "" {
			task.Logs.Overflow = "tail"
		}

		if bytes, err := ParseByteSize(task.Logs.MaxSize); err == nil {
			task.Logs.MaxSizeBytes = bytes
		}
	}

	if bytes, err := ParseByteSize(cfg.Storage.MaxSize); err == nil {
		cfg.Storage.MaxSizeBytes = bytes
	}
	if bytes, err := ParseByteSize(cfg.Storage.MinFreeSpace); err == nil {
		cfg.Storage.MinFreeSpaceBytes = bytes
	}
}
