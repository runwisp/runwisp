// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
	"time"
)

// Every frame. The message payload is `data`.
type Envelope struct {
	// Message name, e.g. `execution:dispatch`.
	Type string `json:"type"`
	// Set on commands only. The receiver answers with one `result` whose replyTo is this id.
	ID string `json:"id,omitzero"`
	// Set on `result` and on `log:lines`. The command this answers.
	ReplyTo string `json:"replyTo,omitzero"`
	// Features the receiver must have to act on this command. A receiver
	// missing any of them answers `unsupported` without acting.
	Requires []string `json:"requires,omitzero"`
	// Sender's clock. Informational.
	SentAt string `json:"sentAt,omitzero"`
	// The payload, per message type. May be absent when empty.
	Data json.RawMessage `json:"data,omitzero"`
}

// No payload.
type Empty struct {
}

type Hello struct {
	// Stable id of this daemon install. Several installs may share one runner token.
	InstanceID string     `json:"instanceId"`
	Agent      *AgentInfo `json:"agent"`
	// Optional features this daemon understands, by name, with whether
	// each can be used right now. A name that's absent is not understood.
	// v1.0 names: exec.shell, exec.container, exec.compose, exec.http,
	// exec.config, agent.restart, agent.upgrade. Everything else in this
	// spec is baseline and has no feature name.
	Features map[string]*FeatureStatus `json:"features"`
	// Operator-set labels (`[daemon] labels`). May be empty.
	Labels map[string]string `json:"labels"`
	// Runs this daemon will hold at once. 0 = no local limit.
	MaxConcurrent int `json:"maxConcurrent"`
	// Hash of the task catalog the daemon would send in tasks:sync.
	TasksHash string `json:"tasksHash"`
	// Highest execution:update seq this install has ever assigned (0 = none).
	LastSeq int64 `json:"lastSeq"`
	// Every execution the daemon still owns: accepted and not yet
	// running, running, or finished with an update not yet acked. An
	// execution the station believes active on this install and absent
	// here was lost by the daemon.
	Executions []InventoryEntry `json:"executions"`
}

type AgentInfo struct {
	// Daemon version, e.g. 1.10.0.
	Version string `json:"version"`
	// linux, darwin, windows, ...
	Os string `json:"os"`
	// amd64, arm64, ...
	Arch string `json:"arch"`
	// Hostname.
	Host string `json:"host"`
	// `[daemon] name` display label; empty when unset.
	Name string `json:"name,omitzero"`
}

type FeatureStatus struct {
	Available bool `json:"available"`
	// Why it's unavailable, for an operator to read.
	Reason string `json:"reason,omitzero"`
}

type InventoryEntry struct {
	ExecutionID string          `json:"executionId"`
	Status      ExecutionStatus `json:"status"`
}

type Welcome struct {
	ConnectionID string `json:"connectionId"`
	// Optional features this station understands. Empty in v1.0.
	Features  []string   `json:"features"`
	Heartbeat *Heartbeat `json:"heartbeat"`
	// How often to send agent:stats. 0 = don't.
	StatsIntervalMs int `json:"statsIntervalMs"`
	// Largest frame the station accepts. The daemon splits or truncates to stay under it.
	MaxFrameBytes int `json:"maxFrameBytes"`
	// False means send tasks:sync now.
	TasksHashKnown bool `json:"tasksHashKnown"`
}

type Heartbeat struct {
	IntervalMs int `json:"intervalMs"`
	TimeoutMs  int `json:"timeoutMs"`
}

type Goodbye struct {
	Code    GoodbyeCode `json:"code"`
	Message string      `json:"message,omitzero"`
	// Authoritative. False: don't reconnect until the daemon's station
	// settings change (or it restarts). True: reconnect after
	// retryAfterMs, plus normal jitter.
	Retry bool `json:"retry"`
	// Wait at least this long before reconnecting. For `draining` the
	// daemon waits a random time in [0, retryAfterMs] so a fleet spreads out.
	RetryAfterMs int `json:"retryAfterMs,omitzero"`
}

// Open. Act on `retry`/`retryAfterMs`, not on the code.
type GoodbyeCode string

const (
	GoodbyeCodeUnauthorized        GoodbyeCode = "unauthorized"
	GoodbyeCodeRevoked             GoodbyeCode = "revoked"
	GoodbyeCodeDisabled            GoodbyeCode = "disabled"
	GoodbyeCodeLimitReached        GoodbyeCode = "limit_reached"
	GoodbyeCodeUnsupportedProtocol GoodbyeCode = "unsupported_protocol"
	GoodbyeCodeOverloaded          GoodbyeCode = "overloaded"
	GoodbyeCodeDraining            GoodbyeCode = "draining"
	GoodbyeCodeDuplicateInstance   GoodbyeCode = "duplicate_instance"
	GoodbyeCodeShutdown            GoodbyeCode = "shutdown"
	GoodbyeCodeProtocolError       GoodbyeCode = "protocol_error"
)

var GoodbyeCodeValues = []GoodbyeCode{GoodbyeCodeUnauthorized, GoodbyeCodeRevoked, GoodbyeCodeDisabled, GoodbyeCodeLimitReached, GoodbyeCodeUnsupportedProtocol, GoodbyeCodeOverloaded, GoodbyeCodeDraining, GoodbyeCodeDuplicateInstance, GoodbyeCodeShutdown, GoodbyeCodeProtocolError}

// Command-specific result fields sit next to `ok` and `error` in the same
// object (see the *Result schemas).
type Result struct {
	Ok    bool       `json:"ok"`
	Error *ErrorInfo `json:"error,omitzero"`
}

type ErrorInfo struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	// Authoritative, so a code added later is still handled right.
	Retryable    bool `json:"retryable"`
	RetryAfterMs int  `json:"retryAfterMs,omitzero"`
}

// Open. `invalid` malformed; `unauthorized`; `forbidden` refused by local
// policy; `not_found`; `conflict`; `unsupported` unknown command or
// missing required feature; `unavailable` backend missing; `busy` at
// capacity; `expired` notAfter passed; `internal`.
type ErrorCode string

const (
	ErrorCodeInvalid      ErrorCode = "invalid"
	ErrorCodeUnauthorized ErrorCode = "unauthorized"
	ErrorCodeForbidden    ErrorCode = "forbidden"
	ErrorCodeNotFound     ErrorCode = "not_found"
	ErrorCodeConflict     ErrorCode = "conflict"
	ErrorCodeUnsupported  ErrorCode = "unsupported"
	ErrorCodeUnavailable  ErrorCode = "unavailable"
	ErrorCodeBusy         ErrorCode = "busy"
	ErrorCodeExpired      ErrorCode = "expired"
	ErrorCodeInternal     ErrorCode = "internal"
)

var ErrorCodeValues = []ErrorCode{ErrorCodeInvalid, ErrorCodeUnauthorized, ErrorCodeForbidden, ErrorCodeNotFound, ErrorCodeConflict, ErrorCodeUnsupported, ErrorCodeUnavailable, ErrorCodeBusy, ErrorCodeExpired, ErrorCodeInternal}

type Ack struct {
	Seq int64 `json:"seq"`
}

// Any field set replaces the current value.
type SessionUpdate struct {
	Heartbeat *Heartbeat `json:"heartbeat,omitzero"`
	// null = leave as is, 0 = stop sending agent:stats.
	StatsIntervalMs *int `json:"statsIntervalMs,omitzero"`
}

// Fields set replace what hello said.
type AgentUpdate struct {
	Agent         *AgentInfo                `json:"agent,omitzero"`
	Features      map[string]*FeatureStatus `json:"features,omitzero"`
	Labels        map[string]string         `json:"labels,omitzero"`
	MaxConcurrent *int                      `json:"maxConcurrent,omitzero"`
}

type AgentStats struct {
	// Percent, 0-100.
	CpuUsage float64 `json:"cpuUsage,omitzero"`
	// Percent, 0-100.
	MemUsage      float64 `json:"memUsage,omitzero"`
	MemTotalBytes int64   `json:"memTotalBytes,omitzero"`
	MemUsedBytes  int64   `json:"memUsedBytes,omitzero"`
	CpuCores      int     `json:"cpuCores,omitzero"`
	// Daemon process uptime.
	UptimeMs int64 `json:"uptimeMs,omitzero"`
}

// Install release `version` from the daemon's own built-in release source
// (never a station-supplied location), verify it, replace the binary and
// restart. The result is sent before the restart. Refused with
// `forbidden` unless the operator allowed it locally.
type AgentUpgrade struct {
	// Release tag, e.g. v1.10.2.
	Version string `json:"version"`
}

// Replace the runner token. The daemon stores it durably before
// answering ok; the station may revoke the old token after that.
type AgentCredentials struct {
	Token string `json:"token"`
}

type TasksSync struct {
	Hash  string           `json:"hash"`
	Tasks []TaskDefinition `json:"tasks"`
}

// Fields of the `result` answering tasks:sync.
type TasksSyncResult struct {
	Added   int `json:"added,omitzero"`
	Updated int `json:"updated,omitzero"`
	// Tasks marked out of sync.
	Removed int `json:"removed,omitzero"`
	Total   int `json:"total,omitzero"`
}

type TaskDefinition struct {
	Name          string        `json:"name"`
	Kind          TaskKind      `json:"kind"`
	Group         string        `json:"group,omitzero"`
	Description   string        `json:"description,omitzero"`
	Enabled       bool          `json:"enabled"`
	Spec          ExecutionSpec `json:"spec"`
	TimeoutMs     int           `json:"timeoutMs,omitzero"`
	Options       *RunOptions   `json:"options,omitzero"`
	MaxConcurrent int           `json:"maxConcurrent,omitzero"`
	OnOverlap     OverlapPolicy `json:"onOverlap,omitzero"`
	// never, on_failure, always.
	RestartPolicy string         `json:"restartPolicy,omitzero"`
	MaxRetries    int            `json:"maxRetries,omitzero"`
	RetryDelayMs  int            `json:"retryDelayMs,omitzero"`
	RetryBackoff  Backoff        `json:"retryBackoff,omitzero"`
	Schedules     []TaskSchedule `json:"schedules,omitzero"`
	// null = not set locally; 0 = explicitly no jitter.
	JitterMs *int `json:"jitterMs,omitzero"`
	// skip, latest, all.
	CatchUp        string                `json:"catchUp,omitzero"`
	MaxCatchUpRuns int                   `json:"maxCatchUpRuns,omitzero"`
	Params         map[string]*TaskParam `json:"params,omitzero"`
	Service        *ServiceSettings      `json:"service,omitzero"`
	Compose        *ComposeRef           `json:"compose,omitzero"`
}

type TaskKind string

const (
	TaskKindTask    TaskKind = "task"
	TaskKindService TaskKind = "service"
)

var TaskKindValues = []TaskKind{TaskKindTask, TaskKindService}

type OverlapPolicy string

const (
	OverlapPolicyQueue OverlapPolicy = "queue"
	OverlapPolicySkip  OverlapPolicy = "skip"
	OverlapPolicyKill  OverlapPolicy = "kill"
)

var OverlapPolicyValues = []OverlapPolicy{OverlapPolicyQueue, OverlapPolicySkip, OverlapPolicyKill}

type Backoff string

const (
	BackoffConstant    Backoff = "constant"
	BackoffLinear      Backoff = "linear"
	BackoffExponential Backoff = "exponential"
)

var BackoffValues = []Backoff{BackoffConstant, BackoffLinear, BackoffExponential}

type TaskSchedule struct {
	Cron string `json:"cron"`
	// IANA zone.
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}

type TaskParam struct {
	// env, arg, ...
	Kind        string   `json:"kind,omitzero"`
	Type        string   `json:"type,omitzero"`
	Required    bool     `json:"required,omitzero"`
	Default     *string  `json:"default,omitzero"`
	Choices     []string `json:"choices,omitzero"`
	AllowCustom bool     `json:"allowCustom,omitzero"`
	Description string   `json:"description,omitzero"`
}

type ServiceSettings struct {
	Instances           int     `json:"instances,omitzero"`
	Autostart           bool    `json:"autostart,omitzero"`
	RestartDelayMs      int     `json:"restartDelayMs,omitzero"`
	RestartBackoff      Backoff `json:"restartBackoff,omitzero"`
	BackoffResetAfterMs int     `json:"backoffResetAfterMs,omitzero"`
}

type ComposeRef struct {
	File        string `json:"file"`
	ProjectName string `json:"projectName"`
	Service     string `json:"service,omitzero"`
}

// What to run. Discriminated by `type`; type X needs feature `exec.X`,
// so a sender puts `exec.X` in `requires`. Unknown types are refused
// with `unsupported`.
type ExecutionSpec = json.RawMessage

type ShellSpec struct {
	Type   string `json:"type"`
	Script string `json:"script"`
	// Absolute interpreter path; empty = /bin/sh.
	Shell      string `json:"shell,omitzero"`
	WorkingDir string `json:"workingDir,omitzero"`
	// 3 or 4 octal digits.
	Umask string `json:"umask,omitzero"`
	// inherit (default) or clean.
	EnvBase string `json:"envBase,omitzero"`
}

type ContainerSpec struct {
	Type      string            `json:"type"`
	Script    string            `json:"script"`
	BaseImage string            `json:"baseImage,omitzero"`
	Blocks    []DockerfileBlock `json:"blocks,omitzero"`
	Env       []KeyValue        `json:"env,omitzero"`
	Volumes   []VolumeMount     `json:"volumes,omitzero"`
	Ports     []PortMapping     `json:"ports,omitzero"`
	// Raw Dockerfile; overrides baseImage and blocks.
	Dockerfile string `json:"dockerfile,omitzero"`
}

type DockerfileBlock struct {
	Label      string `json:"label"`
	Dockerfile string `json:"dockerfile"`
	Enabled    bool   `json:"enabled"`
}

type VolumeMount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	Readonly  bool   `json:"readonly,omitzero"`
}

type PortMapping struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol,omitzero"`
}

type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ComposeSpec struct {
	Type        string `json:"type"`
	File        string `json:"file"`
	ProjectName string `json:"projectName"`
	Service     string `json:"service,omitzero"`
	// run, stack or exec.
	Mode       string   `json:"mode"`
	Profiles   []string `json:"profiles,omitzero"`
	EnvFile    []string `json:"envFile,omitzero"`
	WorkingDir string   `json:"workingDir,omitzero"`
	WithDeps   bool     `json:"withDeps,omitzero"`
	// missing, always or never.
	Pull         string `json:"pull,omitzero"`
	ServicePorts bool   `json:"servicePorts,omitzero"`
	Command      string `json:"command,omitzero"`
}

type HTTPSpec struct {
	Type    string     `json:"type"`
	Method  string     `json:"method"`
	URL     string     `json:"url"`
	Headers []KeyValue `json:"headers,omitzero"`
	Body    *HTTPBody  `json:"body,omitzero"`
}

type HTTPBody struct {
	Kind   string     `json:"kind"`
	Json   string     `json:"json,omitzero"`
	Fields []KeyValue `json:"fields,omitzero"`
}

// Run the daemon's own task `taskName`. The dispatch's timeoutMs and
// options apply to this run only; an env that differs from the task's
// own needs the task's allow_station_dispatch.
type ConfigSpec struct {
	Type     string `json:"type"`
	TaskName string `json:"taskName"`
}

// Per-run knobs. An unset field means the daemon default (for a config spec, the task's own value).
type RunOptions struct {
	Env map[string]string `json:"env,omitzero"`
	// SIGTERM to SIGKILL window.
	GracefulStopMs int   `json:"gracefulStopMs,omitzero"`
	LogMaxBytes    int64 `json:"logMaxBytes,omitzero"`
	// drop_new, drop_old or kill.
	LogOnFull string `json:"logOnFull,omitzero"`
	// The task's `failures` policy in runwisp.toml syntax. Invalid = refused with `invalid`.
	Failures []string `json:"failures,omitzero"`
}

// Idempotent by executionId: a repeat gets ok again without a second
// run. Rejections: not_found (no such config task), forbidden (local
// policy), unavailable (backend missing), busy (retryable), expired.
type ExecutionDispatch struct {
	ExecutionID string            `json:"executionId"`
	Task        *TaskRef          `json:"task"`
	Attempt     int               `json:"attempt,omitzero"`
	Priority    int               `json:"priority,omitzero"`
	Spec        ExecutionSpec     `json:"spec"`
	Inputs      map[string]string `json:"inputs,omitzero"`
	// 0 = no timeout (config spec - the task's own).
	TimeoutMs int         `json:"timeoutMs,omitzero"`
	Options   *RunOptions `json:"options,omitzero"`
	// Upload the log in chunks while running (upload:request purpose log.chunk).
	LogStream bool `json:"logStream,omitzero"`
	// Refuse with `expired` instead of starting after this time.
	NotAfter *time.Time `json:"notAfter,omitzero"`
}

type TaskRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ExecutionStop struct {
	ExecutionID string `json:"executionId"`
	Reason      string `json:"reason,omitzero"`
	// Overrides the run's own window. 0 = keep it.
	GracefulStopMs int `json:"gracefulStopMs,omitzero"`
	// Kill now.
	Force bool `json:"force,omitzero"`
}

// CLOSED. accepted (not started yet), running, finished.
type ExecutionStatus string

const (
	ExecutionStatusAccepted ExecutionStatus = "accepted"
	ExecutionStatusRunning  ExecutionStatus = "running"
	ExecutionStatusFinished ExecutionStatus = "finished"
)

var ExecutionStatusValues = []ExecutionStatus{ExecutionStatusAccepted, ExecutionStatusRunning, ExecutionStatusFinished}

// A finished execution gets one update with status finished, and,
// after its log archive is uploaded, a second finished update carrying
// `log`. Both are sequenced and acked.
type ExecutionUpdate struct {
	Seq         int64           `json:"seq"`
	ExecutionID string          `json:"executionId"`
	Status      ExecutionStatus `json:"status"`
	StartedAt   *time.Time      `json:"startedAt,omitzero"`
	FinishedAt  *time.Time      `json:"finishedAt,omitzero"`
	// Open. Set when finished: succeeded, failed, stopped, timeout,
	// crashed, skipped, log_overflow, queue_full, dst_skipped,
	// daemon_stopped, missed, start_failed, unhealthy, ... A receiver
	// that doesn't know the value uses isFailure.
	Reason string `json:"reason,omitzero"`
	// Set when finished. Whether the run counts as failed under its failures policy.
	IsFailure *bool           `json:"isFailure,omitzero"`
	ExitCode  *int            `json:"exitCode,omitzero"`
	Usage     *ExecutionUsage `json:"usage,omitzero"`
	Log       *LogArchive     `json:"log,omitzero"`
}

type ExecutionUsage struct {
	PeakMemoryBytes *int64 `json:"peakMemoryBytes,omitzero"`
	CpuTimeMs       *int64 `json:"cpuTimeMs,omitzero"`
}

type LogArchive struct {
	// The key of the upload:request target used.
	Key string `json:"key"`
	// Gzipped size.
	Bytes int64 `json:"bytes"`
	// Log chunks uploaded (seq 0..chunks-1); null = unknown.
	Chunks *int `json:"chunks,omitzero"`
}

// Streams log:lines (replyTo = this command's id) from fromLine on, then,
// with follow, live lines with no gap or overlap. Ends with a result
// (LogSubscribeResult): when limit lines were sent, when the run finished
// and all lines were sent, or after log:unsubscribe.
type LogSubscribe struct {
	ExecutionID string `json:"executionId"`
	// First absolute line; 0 = start; negative = the last -fromLine lines (a tail).
	FromLine int64 `json:"fromLine"`
	Follow   bool  `json:"follow"`
	// 0 = no limit.
	Limit int64 `json:"limit,omitzero"`
}

type LogSubscribeResult struct {
	NextLine int64 `json:"nextLine,omitzero"`
	// The run is finished and every line was sent.
	Eof bool `json:"eof,omitzero"`
}

type LogUnsubscribe struct {
	// The log:subscribe command's id.
	Subscription string `json:"subscription"`
}

type LogLines struct {
	Lines []LogLine `json:"lines"`
	Gap   *LogGap   `json:"gap,omitzero"`
}

// Lines [from, to) were dropped under backpressure; replay them if needed.
type LogGap struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type LogLine struct {
	// Absolute line index.
	N int64 `json:"n"`
	// Unix ms; 0 = unknown.
	Ts     int64     `json:"ts,omitzero"`
	Stream LogStream `json:"stream"`
	// No trailing newline.
	Text string `json:"text"`
	// Continues an oversized split line.
	Continued bool `json:"continued,omitzero"`
}

type LogStream string

const (
	LogStreamStdout LogStream = "stdout"
	LogStreamStderr LogStream = "stderr"
	LogStreamSystem LogStream = "system"
)

var LogStreamValues = []LogStream{LogStreamStdout, LogStreamStderr, LogStreamSystem}

type LogSearch struct {
	ExecutionID string `json:"executionId"`
	Query       string `json:"query"`
	// RE2.
	Regex         bool  `json:"regex,omitzero"`
	CaseSensitive bool  `json:"caseSensitive,omitzero"`
	FromLine      int64 `json:"fromLine,omitzero"`
	Limit         int64 `json:"limit,omitzero"`
}

type LogSearchResult struct {
	Hits      []LogLine `json:"hits"`
	NextLine  int64     `json:"nextLine,omitzero"`
	Exhausted bool      `json:"exhausted"`
}

type UploadRequest struct {
	ExecutionID string        `json:"executionId"`
	Purpose     UploadPurpose `json:"purpose"`
	// First chunk seq (log.chunk).
	FromSeq int `json:"fromSeq,omitzero"`
	// Targets wanted (log.chunk).
	Count int `json:"count,omitzero"`
}

// Open; an unknown purpose is answered `unsupported`.
type UploadPurpose string

const (
	UploadPurposeLogArchive UploadPurpose = "log.archive"
	UploadPurposeLogChunk   UploadPurpose = "log.chunk"
)

var UploadPurposeValues = []UploadPurpose{UploadPurposeLogArchive, UploadPurposeLogChunk}

// Fewer targets than asked for means a cap was reached; stop chunking.
type UploadRequestResult struct {
	Uploads []UploadTarget `json:"uploads"`
}

type UploadTarget struct {
	Seq    int    `json:"seq,omitzero"`
	URL    string `json:"url"`
	Method string `json:"method"`
	// Send exactly these headers with the upload.
	Headers   map[string]string `json:"headers,omitzero"`
	Key       string            `json:"key"`
	ExpiresAt time.Time         `json:"expiresAt,omitzero"`
}

// Idempotent upsert of a service. Re-sent after reconnect. spec null
// keeps the daemon's own command for a service from its config.
type ServiceApply struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
	// null (or absent) keeps the daemon's own command.
	Spec     ExecutionSpec    `json:"spec,omitzero"`
	Settings *ServiceSettings `json:"settings"`
	Options  *RunOptions      `json:"options,omitzero"`
}

type ServiceControl struct {
	TaskID string        `json:"taskId"`
	Action ServiceAction `json:"action"`
}

type ServiceAction string

const (
	ServiceActionStart   ServiceAction = "start"
	ServiceActionStop    ServiceAction = "stop"
	ServiceActionRestart ServiceAction = "restart"
)

var ServiceActionValues = []ServiceAction{ServiceActionStart, ServiceActionStop, ServiceActionRestart}

type ServiceRemove struct {
	TaskID string `json:"taskId"`
}

// Sent on change and after welcome. Lives as long as the connection.
type ServiceStatus struct {
	TaskID           string            `json:"taskId"`
	State            ServiceState      `json:"state"`
	DesiredInstances int               `json:"desiredInstances"`
	RunningInstances int               `json:"runningInstances"`
	Instances        []ServiceInstance `json:"instances"`
}

// Open. running, degraded, stopped, fatal.
type ServiceState string

const (
	ServiceStateRunning  ServiceState = "running"
	ServiceStateDegraded ServiceState = "degraded"
	ServiceStateStopped  ServiceState = "stopped"
	ServiceStateFatal    ServiceState = "fatal"
)

var ServiceStateValues = []ServiceState{ServiceStateRunning, ServiceStateDegraded, ServiceStateStopped, ServiceStateFatal}

type ServiceInstance struct {
	Index     int                  `json:"index"`
	State     ServiceInstanceState `json:"state"`
	Pid       *int                 `json:"pid,omitzero"`
	StartedAt *time.Time           `json:"startedAt,omitzero"`
	// Exits since the last healthy run, operator actions included.
	RestartCount int `json:"restartCount"`
	// Failed starts in a row since the last healthy run.
	StartFails   int  `json:"startFails"`
	LastExitCode *int `json:"lastExitCode,omitzero"`
}

// Open. running, restarting, stopped, fatal.
type ServiceInstanceState string

const (
	ServiceInstanceStateRunning    ServiceInstanceState = "running"
	ServiceInstanceStateRestarting ServiceInstanceState = "restarting"
	ServiceInstanceStateStopped    ServiceInstanceState = "stopped"
	ServiceInstanceStateFatal      ServiceInstanceState = "fatal"
)

var ServiceInstanceStateValues = []ServiceInstanceState{ServiceInstanceStateRunning, ServiceInstanceStateRestarting, ServiceInstanceStateStopped, ServiceInstanceStateFatal}
