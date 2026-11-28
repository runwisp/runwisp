// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

// Every frame. The message payload is `data`.
type Envelope struct {
	// Message name, e.g. `execution:dispatch`.
	Type string `json:"type"`
	// Set on commands only. The receiver answers with one `result` whose replyTo is this id.
	ID string `json:"id,omitzero"`
	// Set on `result`, `log:lines` and `log:search:hits`. The command this answers.
	ReplyTo string `json:"replyTo,omitzero"`
	// Set on a `result` whose command failed; `data` is then absent.
	Error *ErrorInfo `json:"error,omitzero"`
	// Features the receiver must have to act on this command. A receiver
	// missing any of them answers `unsupported` without acting.
	Requires []string `json:"requires,omitzero"`
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
	// Hash of the task catalog the daemon would send in tasks:sync.
	TasksHash string `json:"tasksHash"`
	// Every execution the daemon still owns: accepted and not yet
	// running, running, or finished with an update not yet acked. An
	// execution the station believes active on this install and absent
	// here was lost by the daemon. At most 4096 entries: past that,
	// every live one and the oldest finished ones; one left out still
	// gets its updates, which settle it.
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
	State FeatureState `json:"state"`
	// Why it can't be used, for an operator to read.
	Reason string `json:"reason,omitzero"`
}

// Open; any state but available means the feature can't be used now.
// available; disabled (the operator turned it off in the daemon's
// config); unavailable (this host can't provide it, e.g. no Docker).
type FeatureState string

const (
	FeatureStateAvailable   FeatureState = "available"
	FeatureStateDisabled    FeatureState = "disabled"
	FeatureStateUnavailable FeatureState = "unavailable"
)

var FeatureStateValues = []FeatureState{FeatureStateAvailable, FeatureStateDisabled, FeatureStateUnavailable}

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
	// Largest frame the station accepts; at least 64 KiB. The daemon batches log lines and search hits to fit, and drops any other frame over it.
	MaxFrameBytes int           `json:"maxFrameBytes"`
	TasksSync     TasksSyncMode `json:"tasksSync"`
}

// What the daemon sends of its task catalog after welcome: `none` (the
// station has the catalog hello's tasksHash names) or `full` (send
// tasks:sync now). An unknown value is handled as `full`.
type TasksSyncMode string

const (
	TasksSyncModeNone TasksSyncMode = "none"
	TasksSyncModeFull TasksSyncMode = "full"
)

var TasksSyncModeValues = []TasksSyncMode{TasksSyncModeNone, TasksSyncModeFull}

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

type ErrorInfo struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	// Authoritative, so a code added later is still handled right.
	Retryable bool `json:"retryable"`
}

// Open. `invalid` malformed; `unauthorized`; `forbidden` refused by local
// policy; `not_found`; `conflict`; `unsupported` unknown command or
// missing required feature; `unavailable` backend missing; `busy` at
// capacity; `internal`.
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
	ErrorCodeInternal     ErrorCode = "internal"
)

var ErrorCodeValues = []ErrorCode{ErrorCodeInvalid, ErrorCodeUnauthorized, ErrorCodeForbidden, ErrorCodeNotFound, ErrorCodeConflict, ErrorCodeUnsupported, ErrorCodeUnavailable, ErrorCodeBusy, ErrorCodeInternal}

type Ack struct {
	Seq int64 `json:"seq"`
}

// Ends a streaming command (log:subscribe, log:search) early. An id that
// names no running command is ignored.
type Cancel struct {
	// The id of the command to end.
	Command string `json:"command"`
}

// Fields set replace what hello said.
type AgentUpdate struct {
	Agent    *AgentInfo                `json:"agent,omitzero"`
	Features map[string]*FeatureStatus `json:"features,omitzero"`
}

// The daemon's host, sampled when sent. A value the daemon can't
// measure on its platform is absent, never a stand-in.
type AgentStats struct {
	// The host's 1-minute load average, as the OS reports it (not a percent).
	Load1 *float64 `json:"load1,omitzero"`
	// Host memory.
	MemTotalBytes int64 `json:"memTotalBytes,omitzero"`
	// Host memory in use (total minus available).
	MemUsedBytes int64 `json:"memUsedBytes,omitzero"`
	CpuCores     int   `json:"cpuCores,omitzero"`
	// Daemon process uptime.
	UptimeMs int64 `json:"uptimeMs,omitzero"`
}

// Install release `version` from the daemon's own built-in release source
// (never a station-supplied location), verify it, replace the binary and
// restart. The result is sent before the restart. Refused with
// `forbidden` unless the operator allowed it locally.
type AgentUpgrade struct {
	// Release, e.g. 1.10.2 or v1.10.2.
	Version string `json:"version"`
}

// Replace the runner token. The daemon stores it durably before
// answering; the station may revoke the old token after a result
// without error.
type AgentCredentials struct {
	Token string `json:"token"`
}

type TasksSync struct {
	// Identifies tasks and services together.
	Hash string `json:"hash"`
	// The daemon's own tasks and services (runwisp.toml), by name.
	Tasks []TaskDefinition `json:"tasks"`
	// The taskId of every service the station declared (service:apply)
	// that this daemon still holds. One the station no longer has is
	// removed with service:remove.
	Services []string `json:"services"`
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
	Name          string         `json:"name"`
	Kind          TaskKind       `json:"kind"`
	Group         string         `json:"group,omitzero"`
	Description   string         `json:"description,omitzero"`
	Spec          ExecutionSpec  `json:"spec"`
	TimeoutMs     int            `json:"timeoutMs,omitzero"`
	Options       *RunOptions    `json:"options,omitzero"`
	MaxConcurrent int            `json:"maxConcurrent,omitzero"`
	OnOverlap     OverlapPolicy  `json:"onOverlap,omitzero"`
	RestartPolicy RestartPolicy  `json:"restartPolicy,omitzero"`
	MaxRetries    int            `json:"maxRetries,omitzero"`
	RetryDelayMs  int            `json:"retryDelayMs,omitzero"`
	RetryBackoff  Backoff        `json:"retryBackoff,omitzero"`
	Schedules     []TaskSchedule `json:"schedules,omitzero"`
	// null = not set locally; 0 = explicitly no jitter.
	JitterMs       *int                  `json:"jitterMs,omitzero"`
	CatchUp        CatchUp               `json:"catchUp,omitzero"`
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

type RestartPolicy string

const (
	RestartPolicyNever     RestartPolicy = "never"
	RestartPolicyOnFailure RestartPolicy = "on_failure"
	RestartPolicyAlways    RestartPolicy = "always"
)

var RestartPolicyValues = []RestartPolicy{RestartPolicyNever, RestartPolicyOnFailure, RestartPolicyAlways}

// Missed schedule ticks run: none, the latest one, or up to maxCatchUpRuns.
type CatchUp string

const (
	CatchUpSkip   CatchUp = "skip"
	CatchUpLatest CatchUp = "latest"
	CatchUpAll    CatchUp = "all"
)

var CatchUpValues = []CatchUp{CatchUpSkip, CatchUpLatest, CatchUpAll}

// How a param reaches the run. A flag is a boolean.
type ParamKind string

const (
	ParamKindEnv    ParamKind = "env"
	ParamKindArg    ParamKind = "arg"
	ParamKindOption ParamKind = "option"
	ParamKindFlag   ParamKind = "flag"
)

var ParamKindValues = []ParamKind{ParamKindEnv, ParamKindArg, ParamKindOption, ParamKindFlag}

// A value type; a param with choices is an enum of them.
type ParamType string

const (
	ParamTypeString ParamType = "string"
	ParamTypeNumber ParamType = "number"
)

var ParamTypeValues = []ParamType{ParamTypeString, ParamTypeNumber}

// The env a run starts from: the daemon's own, or crond's minimal set.
type EnvBase string

const (
	EnvBaseInherit EnvBase = "inherit"
	EnvBaseClean   EnvBase = "clean"
)

var EnvBaseValues = []EnvBase{EnvBaseInherit, EnvBaseClean}

type ComposeMode string

const (
	ComposeModeRun   ComposeMode = "run"
	ComposeModeStack ComposeMode = "stack"
	ComposeModeExec  ComposeMode = "exec"
)

var ComposeModeValues = []ComposeMode{ComposeModeRun, ComposeModeStack, ComposeModeExec}

type ComposePull string

const (
	ComposePullMissing ComposePull = "missing"
	ComposePullAlways  ComposePull = "always"
	ComposePullNever   ComposePull = "never"
)

var ComposePullValues = []ComposePull{ComposePullMissing, ComposePullAlways, ComposePullNever}

// What a run does when its log reaches logMaxBytes.
type LogOnFull string

const (
	LogOnFullDropNew LogOnFull = "drop_new"
	LogOnFullDropOld LogOnFull = "drop_old"
	LogOnFullKill    LogOnFull = "kill"
)

var LogOnFullValues = []LogOnFull{LogOnFullDropNew, LogOnFullDropOld, LogOnFullKill}

type HTTPBodyKind string

const (
	HTTPBodyKindNone     HTTPBodyKind = "none"
	HTTPBodyKindJson     HTTPBodyKind = "json"
	HTTPBodyKindFormData HTTPBodyKind = "formData"
)

var HTTPBodyKindValues = []HTTPBodyKind{HTTPBodyKindNone, HTTPBodyKindJson, HTTPBodyKindFormData}

type TaskSchedule struct {
	Cron string `json:"cron"`
	// IANA zone.
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}

type TaskParam struct {
	Kind        ParamKind `json:"kind,omitzero"`
	Type        ParamType `json:"type,omitzero"`
	Required    bool      `json:"required,omitzero"`
	Default     *string   `json:"default,omitzero"`
	Choices     []string  `json:"choices,omitzero"`
	AllowCustom bool      `json:"allowCustom,omitzero"`
	Description string    `json:"description,omitzero"`
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
	// Default inherit.
	EnvBase EnvBase `json:"envBase,omitzero"`
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
	Type         string      `json:"type"`
	File         string      `json:"file"`
	ProjectName  string      `json:"projectName"`
	Service      string      `json:"service,omitzero"`
	Mode         ComposeMode `json:"mode"`
	Profiles     []string    `json:"profiles,omitzero"`
	EnvFile      []string    `json:"envFile,omitzero"`
	WorkingDir   string      `json:"workingDir,omitzero"`
	WithDeps     bool        `json:"withDeps,omitzero"`
	Pull         ComposePull `json:"pull,omitzero"`
	ServicePorts bool        `json:"servicePorts,omitzero"`
	Command      string      `json:"command,omitzero"`
}

type HTTPSpec struct {
	Type    string     `json:"type"`
	Method  string     `json:"method"`
	URL     string     `json:"url"`
	Headers []KeyValue `json:"headers,omitzero"`
	Body    *HTTPBody  `json:"body,omitzero"`
}

type HTTPBody struct {
	Kind   HTTPBodyKind `json:"kind"`
	Json   string       `json:"json,omitzero"`
	Fields []KeyValue   `json:"fields,omitzero"`
}

// Run the daemon's own task `taskName`. The dispatch's timeoutMs and
// options apply to this run only; an env that differs from the task's
// own needs the task's RUNWISP_ALLOW=station-dispatch.
type ConfigSpec struct {
	Type     string `json:"type"`
	TaskName string `json:"taskName"`
}

// Per-run knobs. An unset field means the daemon default (for a config spec, the task's own value).
type RunOptions struct {
	Env map[string]string `json:"env,omitzero"`
	// SIGTERM to SIGKILL window.
	GracefulStopMs int       `json:"gracefulStopMs,omitzero"`
	LogMaxBytes    int64     `json:"logMaxBytes,omitzero"`
	LogOnFull      LogOnFull `json:"logOnFull,omitzero"`
	// The task's `failures` policy in runwisp.toml syntax. Invalid = refused with `invalid`.
	Failures []string `json:"failures,omitzero"`
}

// Idempotent by executionId: a repeat succeeds again without a second
// run. Rejections: not_found (no such config task), forbidden (local
// policy), unavailable (backend missing), busy (retryable).
type ExecutionDispatch struct {
	ExecutionID string `json:"executionId"`
	// The station's task this runs.
	TaskID string            `json:"taskId"`
	Spec   ExecutionSpec     `json:"spec"`
	Inputs map[string]string `json:"inputs,omitzero"`
	// 0 = no timeout (config spec - the task's own).
	TimeoutMs int         `json:"timeoutMs,omitzero"`
	Options   *RunOptions `json:"options,omitzero"`
	LogUpload LogUpload   `json:"logUpload"`
}

// Open; an unknown value is treated as archive. What the daemon uploads
// of a run's log: none (it stays on the daemon, read through log:subscribe
// and log:search), archive (one log.archive upload when the run ends),
// stream (as archive, plus log.chunk uploads while it runs).
type LogUpload string

const (
	LogUploadNone    LogUpload = "none"
	LogUploadArchive LogUpload = "archive"
	LogUploadStream  LogUpload = "stream"
)

var LogUploadValues = []LogUpload{LogUploadNone, LogUploadArchive, LogUploadStream}

// Stops a running execution, or one accepted and still waiting to start;
// either finishes with reason stopped. Stopping a finished execution
// succeeds; one the daemon never had is refused with not_found.
type ExecutionStop struct {
	ExecutionID string `json:"executionId"`
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
//
// A service instance's run is an execution the daemon starts itself: its
// executionId is the daemon's run id, and every update of it carries
// taskId and instance, so the station records the run from whichever
// update it sees first.
type ExecutionUpdate struct {
	Seq         int64  `json:"seq"`
	ExecutionID string `json:"executionId"`
	// Set on a service instance's run. The service, as in service:status.taskId.
	TaskID string `json:"taskId,omitzero"`
	// Set on a service instance's run. 0-based instance slot.
	Instance *int `json:"instance,omitzero"`
	// running or finished. A dispatch's result is its acceptance; accepted appears only in hello's inventory.
	Status ExecutionStatus `json:"status"`
	// Unix ms.
	StartedAt int64 `json:"startedAt,omitzero"`
	// Unix ms.
	FinishedAt int64 `json:"finishedAt,omitzero"`
	// Set when finished.
	Reason EndReason `json:"reason,omitzero"`
	// Set when finished. Whether the run counts as failed under its failures policy.
	IsFailure *bool           `json:"isFailure,omitzero"`
	ExitCode  *int            `json:"exitCode,omitzero"`
	Usage     *ExecutionUsage `json:"usage,omitzero"`
	Log       *LogArchive     `json:"log,omitzero"`
}

// Open. Why a run finished; a receiver that doesn't know the value uses isFailure.
type EndReason string

const (
	EndReasonSucceeded     EndReason = "succeeded"
	EndReasonFailed        EndReason = "failed"
	EndReasonStopped       EndReason = "stopped"
	EndReasonTimeout       EndReason = "timeout"
	EndReasonCrashed       EndReason = "crashed"
	EndReasonSkipped       EndReason = "skipped"
	EndReasonLogOverflow   EndReason = "log_overflow"
	EndReasonQueueFull     EndReason = "queue_full"
	EndReasonDstSkipped    EndReason = "dst_skipped"
	EndReasonDaemonStopped EndReason = "daemon_stopped"
	EndReasonMissed        EndReason = "missed"
	EndReasonStartFailed   EndReason = "start_failed"
	EndReasonUnhealthy     EndReason = "unhealthy"
)

var EndReasonValues = []EndReason{EndReasonSucceeded, EndReasonFailed, EndReasonStopped, EndReasonTimeout, EndReasonCrashed, EndReasonSkipped, EndReasonLogOverflow, EndReasonQueueFull, EndReasonDstSkipped, EndReasonDaemonStopped, EndReasonMissed, EndReasonStartFailed, EndReasonUnhealthy}

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
// and all lines were sent, without follow once it caught up, or after
// `cancel`. A log the daemon can't read ends it with an `internal` error.
type LogSubscribe struct {
	ExecutionID string `json:"executionId"`
	// First absolute line; 0 = start; negative = the last -fromLine lines (a tail).
	FromLine int64 `json:"fromLine"`
	Follow   bool  `json:"follow"`
	// 0 = no limit.
	Limit int64 `json:"limit,omitzero"`
}

type LogSubscribeResult struct {
	NextLine int64            `json:"nextLine"`
	Stopped  LogSubscribeStop `json:"stopped"`
}

// Open. Why a log:subscribe ended: done (the run finished and every line
// was sent), caught_up (no follow: every line written so far was sent;
// the run goes on), limit, cancelled. Only done means there is nothing
// more to read.
type LogSubscribeStop string

const (
	LogSubscribeStopDone      LogSubscribeStop = "done"
	LogSubscribeStopCaughtUp  LogSubscribeStop = "caught_up"
	LogSubscribeStopLimit     LogSubscribeStop = "limit"
	LogSubscribeStopCancelled LogSubscribeStop = "cancelled"
)

var LogSubscribeStopValues = []LogSubscribeStop{LogSubscribeStopDone, LogSubscribeStopCaughtUp, LogSubscribeStopLimit, LogSubscribeStopCancelled}

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

// Searches the local logs of the daemon's station executions, dispatched
// or service instances' runs: with executionId that run alone, else
// every run tasks, since and until select, newest first, one run at a
// time. Hits stream as log:search:hits (replyTo = this command's id); a
// LogSearchResult ends it once every run is scanned, a cap or timeoutMs
// is reached, or after `cancel`. With executionId, an unknown execution
// is refused with not_found and one not started yet with unavailable.
type LogSearch struct {
	// A line matches when it matches every term. Compiled by the station from the user's query.
	Terms []LogSearchTerm `json:"terms,omitzero"`
	// RE2. When set, a line matches when it matches regex, and terms is ignored.
	Regex string `json:"regex,omitzero"`
	// False: terms and regex ignore case (terms' parts and the line are
	// both lowercased, as Go's strings.ToLower does).
	CaseSensitive bool `json:"caseSensitive,omitzero"`
	// Search only this execution's log; tasks, since and until are ignored.
	ExecutionID string `json:"executionId,omitzero"`
	// With executionId, start at this absolute line, to resume where nextLine left off.
	FromLine int64 `json:"fromLine,omitzero"`
	// Tasks to search; empty = all. An entry is a taskId as sent in
	// execution:dispatch or service:apply, or a task name of the
	// daemon's own (the taskName of a config spec, or a service run's
	// taskId in execution:update).
	Tasks []string `json:"tasks,omitzero"`
	// Unix ms; only runs that overlap [since, until]: still running, or ended at or after since. 0 = no bound.
	Since int64 `json:"since,omitzero"`
	// Unix ms; only runs that overlap [since, until]: started at or before until. 0 = no bound.
	Until int64 `json:"until,omitzero"`
	// Lines before and after each hit, 0-5.
	Context int `json:"context,omitzero"`
	// Stop after this many hits.
	MaxHits int `json:"maxHits"`
	// Stop after this many bytes of hit text (lines and context). 0 = no limit.
	MaxBytes int `json:"maxBytes,omitzero"`
	// The daemon stops on its own after this long.
	TimeoutMs int `json:"timeoutMs"`
}

// One term: its literal parts in order, separated by `*` (any run of
// characters on the line, including none). A word character is a
// Unicode letter or digit; everything else separates words.
type LogSearchTerm struct {
	// Non-empty literal pieces between the wildcards, in order.
	Parts []string `json:"parts"`
	// Where the first part must begin.
	Start TermAnchor `json:"start"`
	// Where the last part must end.
	End TermAnchor `json:"end"`
}

// Where a term's edge may sit: `any` position, or a `word` boundary.
// A search with an anchor the daemon doesn't know is refused as
// `invalid`.
type TermAnchor string

const (
	TermAnchorAny  TermAnchor = "any"
	TermAnchorWord TermAnchor = "word"
)

var TermAnchorValues = []TermAnchor{TermAnchorAny, TermAnchorWord}

type LogSearchHits struct {
	Hits     []LogSearchHit     `json:"hits"`
	Progress *LogSearchProgress `json:"progress"`
}

// A matching line and its context. Each line's text is cut to 4 KiB, with continued set.
type LogSearchHit struct {
	ExecutionID string    `json:"executionId"`
	Line        *LogLine  `json:"line"`
	Before      []LogLine `json:"before,omitzero"`
	After       []LogLine `json:"after,omitzero"`
}

type LogSearchProgress struct {
	RunsScanned  int   `json:"runsScanned"`
	RunsTotal    int   `json:"runsTotal"`
	BytesScanned int64 `json:"bytesScanned"`
}

// Fields of the `result` answering log:search.
type LogSearchResult struct {
	Progress *LogSearchProgress `json:"progress"`
	Stopped  LogSearchStop      `json:"stopped"`
	// With executionId, when the search stopped before the log's end, the line after the last hit sent. 0 = nothing left.
	NextLine int64 `json:"nextLine,omitzero"`
}

// Open. Why a log:search ended.
type LogSearchStop string

const (
	LogSearchStopDone      LogSearchStop = "done"
	LogSearchStopMaxHits   LogSearchStop = "max_hits"
	LogSearchStopMaxBytes  LogSearchStop = "max_bytes"
	LogSearchStopTimeout   LogSearchStop = "timeout"
	LogSearchStopCancelled LogSearchStop = "cancelled"
)

var LogSearchStopValues = []LogSearchStop{LogSearchStopDone, LogSearchStopMaxHits, LogSearchStopMaxBytes, LogSearchStopTimeout, LogSearchStopCancelled}

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
	Headers map[string]string `json:"headers,omitzero"`
	Key     string            `json:"key"`
}

// Idempotent upsert of a service. Re-sent after reconnect. spec null
// keeps the daemon's own command for a service from its config.
type ServiceApply struct {
	// The station's task, or the name of a service the daemon defines itself.
	TaskID string `json:"taskId"`
	// null (or absent) keeps the daemon's own command.
	Spec     ExecutionSpec    `json:"spec,omitzero"`
	Settings *ServiceSettings `json:"settings"`
	Options  *RunOptions      `json:"options,omitzero"`
}

type ServiceControl struct {
	TaskID string        `json:"taskId"`
	Action ServiceAction `json:"action"`
}

// Open; an unknown action is refused with `unsupported`.
type ServiceAction string

const (
	ServiceActionStart   ServiceAction = "start"
	ServiceActionStop    ServiceAction = "stop"
	ServiceActionRestart ServiceAction = "restart"
)

var ServiceActionValues = []ServiceAction{ServiceActionStart, ServiceActionStop, ServiceActionRestart}

// Removes a service the station declared; a taskId the daemon doesn't
// hold succeeds. A service from the daemon's own config is refused with
// `forbidden`.
type ServiceRemove struct {
	TaskID string `json:"taskId"`
}

// Sent on change and after welcome. Lives as long as the connection. A
// service the daemon no longer has (removed, or dropped from its config)
// gets one last status with state removed and no instances; the station
// forgets it.
type ServiceStatus struct {
	// The taskId of service:apply for a service the station declared, else the daemon's own task name.
	TaskID string       `json:"taskId"`
	State  ServiceState `json:"state"`
	// One per desired instance, by index.
	Instances []ServiceInstance `json:"instances"`
}

// Open. running, degraded, stopped, fatal, removed (the service is gone).
type ServiceState string

const (
	ServiceStateRunning  ServiceState = "running"
	ServiceStateDegraded ServiceState = "degraded"
	ServiceStateStopped  ServiceState = "stopped"
	ServiceStateFatal    ServiceState = "fatal"
	ServiceStateRemoved  ServiceState = "removed"
)

var ServiceStateValues = []ServiceState{ServiceStateRunning, ServiceStateDegraded, ServiceStateStopped, ServiceStateFatal, ServiceStateRemoved}

type ServiceInstance struct {
	Index int                  `json:"index"`
	State ServiceInstanceState `json:"state"`
	Pid   *int                 `json:"pid,omitzero"`
	// Unix ms.
	StartedAt int64 `json:"startedAt,omitzero"`
	// Exits since the last healthy run, operator actions included.
	RestartCount int `json:"restartCount"`
	// Failed starts in a row since the last healthy run.
	StartFails   int  `json:"startFails"`
	LastExitCode *int `json:"lastExitCode,omitzero"`
	// The slot's current or latest run, as in execution:update. Absent before its first run.
	ExecutionID string `json:"executionId,omitzero"`
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
