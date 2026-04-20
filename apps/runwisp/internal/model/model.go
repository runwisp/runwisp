// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var taskNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// SanitizeTaskName replaces unsafe characters with underscores, making the
// name safe for use in file paths and other identifiers.
func SanitizeTaskName(name string) string {
	return taskNameSanitizer.ReplaceAllString(name, "_")
}

// Task describes a runnable task loaded from configuration.
type Task struct {
	Name         string        `yaml:"-" json:"name"`
	Group        string        `yaml:"group,omitempty" json:"group,omitempty"`
	Description  string        `yaml:"description,omitempty" json:"description,omitempty"`
	Trigger      TaskTrigger   `yaml:"trigger,omitempty" json:"trigger,omitempty"`
	Execution    TaskExecution `yaml:"execution,omitempty" json:"execution,omitempty"`
	Retry        TaskRetry     `yaml:"retry,omitempty" json:"retry,omitempty"`
	Logs         TaskLogs      `yaml:"logs,omitempty" json:"logs,omitempty"`
	Retention    TaskRetention `yaml:"retention,omitempty" json:"retention,omitempty"`
	Run          string        `yaml:"run,omitempty" json:"-"`
	ExecutionDef ExecutionDef  `yaml:"-" json:"-"`
}

// UnmarshalYAML validates the supported task keys before decoding.
func (t *Task) UnmarshalYAML(value *yaml.Node) error {
	type rawTask Task
	var raw rawTask
	if err := ValidateMappingKeys(value,
		"group",
		"description",
		"trigger",
		"execution",
		"retry",
		"logs",
		"retention",
		"run",
	); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*t = Task(raw)
	return nil
}

// ResolvedExecutionDef returns the runtime execution definition for the task.
func (t *Task) ResolvedExecutionDef() ExecutionDef {
	if t.ExecutionDef != nil {
		return t.ExecutionDef
	}
	if strings.TrimSpace(t.Run) == "" {
		return nil
	}
	return &ShellExecution{Script: t.Run}
}

type TaskTrigger struct {
	Cron    string          `yaml:"cron,omitempty" json:"cron,omitempty"`
	API     *bool           `yaml:"api,omitempty" json:"api"`
	Catchup MissedRunPolicy `yaml:"catchup,omitempty" json:"catchup,omitempty" enum:"latest,all,none" doc:"What to do when cron ticks are missed during downtime"`
}

// APIEnabled reports whether the task can be triggered via the API.
// Defaults to true when the field is not explicitly set.
func (t *TaskTrigger) APIEnabled() bool {
	return t.API == nil || *t.API
}

func (t *TaskTrigger) UnmarshalYAML(value *yaml.Node) error {
	type rawTaskTrigger TaskTrigger
	var raw rawTaskTrigger
	if err := ValidateMappingKeys(value, "cron", "api", "catchup"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*t = TaskTrigger(raw)
	return nil
}

type TaskExecution struct {
	Timeout     string          `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Restart     RestartPolicy   `yaml:"restart,omitempty" json:"restart,omitempty" enum:"never,always,on-failure" doc:"Whether and when a task is restarted after completion"`
	Concurrency TaskConcurrency `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
}

func (t *TaskExecution) UnmarshalYAML(value *yaml.Node) error {
	type rawTaskExecution TaskExecution
	var raw rawTaskExecution
	if err := ValidateMappingKeys(value, "timeout", "restart", "concurrency"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*t = TaskExecution(raw)
	return nil
}

type TaskConcurrency struct {
	Limit  int               `yaml:"limit,omitempty" json:"limit"`
	Policy ConcurrencyPolicy `yaml:"policy,omitempty" json:"policy,omitempty" enum:"queue,skip,terminate" doc:"How overlapping runs are handled"`
}

func (t *TaskConcurrency) UnmarshalYAML(value *yaml.Node) error {
	type rawTaskConcurrency TaskConcurrency
	var raw rawTaskConcurrency
	if err := ValidateMappingKeys(value, "limit", "policy"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*t = TaskConcurrency(raw)
	return nil
}

type TaskRetry struct {
	Limit    int    `yaml:"limit,omitempty" json:"limit,omitempty"`
	DelaySec int    `yaml:"delaySec,omitempty" json:"delaySec,omitempty"`
	Backoff  string `yaml:"backoff,omitempty" json:"backoff,omitempty"`
}

func (t *TaskRetry) UnmarshalYAML(value *yaml.Node) error {
	type rawTaskRetry TaskRetry
	var raw rawTaskRetry
	if err := ValidateMappingKeys(value, "limit", "delaySec", "backoff"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*t = TaskRetry(raw)
	return nil
}

type TaskLogs struct {
	MaxSize      string `yaml:"maxSize,omitempty" json:"maxSize,omitempty"`
	Overflow     string `yaml:"overflow,omitempty" json:"overflow,omitempty"`
	MaxSizeBytes int64  `yaml:"-" json:"-"`
}

func (t *TaskLogs) UnmarshalYAML(value *yaml.Node) error {
	type rawTaskLogs TaskLogs
	var raw rawTaskLogs
	if err := ValidateMappingKeys(value, "maxSize", "overflow"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*t = TaskLogs(raw)
	return nil
}

type TaskRetention struct {
	Runs int    `yaml:"runs,omitempty" json:"runs,omitempty"`
	Age  string `yaml:"age,omitempty" json:"age,omitempty"`
}

func (t *TaskRetention) UnmarshalYAML(value *yaml.Node) error {
	type rawTaskRetention TaskRetention
	var raw rawTaskRetention
	if err := ValidateMappingKeys(value, "runs", "age"); err != nil {
		return err
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*t = TaskRetention(raw)
	return nil
}

func ValidateMappingKeys(value *yaml.Node, allowedKeys ...string) error {
	if value.Kind == 0 || value.Tag == "!!null" {
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return nil
	}

	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}

	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i].Value
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown field %q", key)
		}
	}

	return nil
}

// RunPhase captures the high-level lifecycle phase of a run.
type RunPhase string

const (
	PhasePending RunPhase = "pending"
	PhaseRunning RunPhase = "running"
	PhaseEnded   RunPhase = "ended"
)

// EndReason describes why a run ended.
type EndReason string

const (
	ReasonSuccess EndReason = "success"
	ReasonFailed  EndReason = "failed"
	ReasonStopped EndReason = "stopped"
	ReasonTimeout EndReason = "timeout"
	ReasonCrashed EndReason = "crashed"
)

// IsTerminal reports whether the phase represents a completed run.
func (p RunPhase) IsTerminal() bool {
	return p == PhaseEnded
}

// TriggeredBy identifies how a run started.
type TriggeredBy string

const (
	TriggeredByCron  TriggeredBy = "cron"
	TriggeredByAPI   TriggeredBy = "api"
	TriggeredByCloud TriggeredBy = "cloud"
)

// ConcurrencyPolicy controls how overlapping runs are handled.
type ConcurrencyPolicy string

const (
	PolicyQueue     ConcurrencyPolicy = "queue"
	PolicySkip      ConcurrencyPolicy = "skip"
	PolicyTerminate ConcurrencyPolicy = "terminate"
)

// RestartPolicy controls whether and when a task is restarted after completion.
type RestartPolicy string

const (
	RestartNever     RestartPolicy = "never"
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
)

// MissedRunPolicy controls what happens when cron ticks are missed (e.g. daemon downtime).
type MissedRunPolicy string

const (
	MissedRunLatest MissedRunPolicy = "latest" // trigger one catch-up run for the most recent missed tick
	MissedRunAll    MissedRunPolicy = "all"    // trigger a run for every missed tick
	MissedRunNone   MissedRunPolicy = "none"   // skip missed ticks entirely
)

// Run represents a persisted execution.
type Run struct {
	ID                  string      `gorm:"type:text;primary_key" json:"id"`
	ExternalExecutionID *string     `gorm:"index" json:"external_execution_id,omitempty"`
	TaskName            string      `gorm:"index;not null;default:''" json:"task_name"`
	Status              RunPhase    `gorm:"type:varchar(20);not null" json:"status" enum:"pending,running,ended" doc:"Run lifecycle phase"`
	EndReason           *EndReason  `gorm:"type:varchar(20)" json:"end_reason,omitempty" enum:"success,failed,stopped,timeout,crashed" doc:"Why the run ended (set when status=ended)"`
	ExitCode            int         `json:"exit_code"`
	LogPath             string      `gorm:"-" json:"-"`
	StartAt             *time.Time  `json:"start_at,omitempty"`
	EndAt               *time.Time  `json:"end_at,omitempty"`
	TriggeredBy         TriggeredBy `gorm:"type:varchar(20);not null" json:"triggered_by" enum:"cron,api,cloud" doc:"How the run was triggered"`
	CreatedAt           time.Time   `json:"created_at"`
	RetryAttempt        int         `json:"retry_attempt"`
	RetryOfRunID        *string     `json:"retry_of_run_id,omitempty"`
}

// Copy creates a deep copy of the Run to prevent data races.
func (r *Run) Copy() *Run {
	if r == nil {
		return nil
	}
	cpy := *r
	if r.ExternalExecutionID != nil {
		eid := *r.ExternalExecutionID
		cpy.ExternalExecutionID = &eid
	}
	if r.EndReason != nil {
		er := *r.EndReason
		cpy.EndReason = &er
	}
	if r.StartAt != nil {
		sa := *r.StartAt
		cpy.StartAt = &sa
	}
	if r.EndAt != nil {
		ea := *r.EndAt
		cpy.EndAt = &ea
	}
	if r.RetryOfRunID != nil {
		rid := *r.RetryOfRunID
		cpy.RetryOfRunID = &rid
	}
	return &cpy
}

// End transitions a run to the ended phase with the given reason.
func (r *Run) End(reason EndReason, exitCode int, endAt time.Time) {
	r.Status = PhaseEnded
	r.EndReason = &reason
	r.ExitCode = exitCode
	r.EndAt = &endAt
}

// IsRetryable reports whether a run ended with a non-success reason.
func (r *Run) IsRetryable() bool {
	return r.Status == PhaseEnded && r.EndReason != nil && *r.EndReason != ReasonSuccess
}

// EndReasonPtr returns a pointer to the given EndReason value.
func EndReasonPtr(r EndReason) *EndReason { return &r }

// DisplayStatus returns the end reason string when ended, otherwise the phase.
func (r *Run) DisplayStatus() string {
	if r.Status == PhaseEnded {
		if r.EndReason != nil {
			return string(*r.EndReason)
		}
		return string(ReasonStopped)
	}
	return string(r.Status)
}

// DaemonInfo holds static identity/config data exposed via /api/info.
type DaemonInfo struct {
	Version      string      `json:"version"`
	Fingerprint  string      `json:"fingerprint"`
	Port         int         `json:"port"`
	CloudEnabled bool        `json:"cloud_enabled"`
	Tasks        []TaskBrief `json:"tasks"`
	Capabilities []CapInfo   `json:"capabilities"`
}

// TaskBrief is a trimmed task descriptor.
type TaskBrief struct {
	Name      string        `json:"name"`
	Group     string        `json:"group,omitempty"`
	Trigger   TaskTrigger   `json:"trigger,omitempty"`
	Execution TaskExecution `json:"execution,omitempty"`
}

// CapInfo describes a daemon capability.
type CapInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// RunSummary holds aggregate run statistics.
type RunSummary struct {
	Total       int64      `json:"total" doc:"Total number of runs"`
	Success     int64      `json:"success" doc:"Number of successful runs"`
	Failed      int64      `json:"failed" doc:"Number of failed runs"`
	LastFailure *time.Time `json:"last_failure,omitempty" doc:"Timestamp of most recent failure"`
}

// IsLongRunningTask returns true for always-restart tasks with concurrency <= 1.
func IsLongRunningTask(restartPolicy RestartPolicy, concurrencyLimit int) bool {
	return restartPolicy == RestartAlways && concurrencyLimit <= 1
}

// TaskRegistration is a one-to-one per-task record for metadata that has no
// natural home in the run log (first-seen timestamp, future per-task flags, etc.).
type TaskRegistration struct {
	TaskName    string    `gorm:"primaryKey"`
	FirstSeenAt time.Time `gorm:"not null"`
}

// ConfigEntry stores a named daemon configuration value persisted in the database.
type ConfigEntry struct {
	Key   string `gorm:"primaryKey"`
	Value string `gorm:"not null"`
}
