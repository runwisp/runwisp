// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
	"time"
)

type ExecutionStatus string

const (
	ExecutionStatusRunning   ExecutionStatus = "running"
	ExecutionStatusSucceeded ExecutionStatus = "succeeded"
	ExecutionStatusFailed    ExecutionStatus = "failed"
	ExecutionStatusStopped   ExecutionStatus = "stopped"
	ExecutionStatusTimeout   ExecutionStatus = "timeout"
	ExecutionStatusSkipped   ExecutionStatus = "skipped"
)

var ExecutionStatusValues = []ExecutionStatus{ExecutionStatusRunning, ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusStopped, ExecutionStatusTimeout, ExecutionStatusSkipped}

type AuthResultMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	Success         bool      `json:"success"`
	ConnectionID    string    `json:"connectionId,omitzero"`
	Error           string    `json:"error,omitzero"`
}

type PongMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
}

// Dispatches an execution to a daemon.
// For "fan-out" distribution on cluster runners, the control plane sends
// this message to every connected node simultaneously.
type ExecutionDispatchMessage struct {
	Type            string            `json:"type"`
	ProtocolVersion int               `json:"protocolVersion,omitzero"`
	SentAt          time.Time         `json:"sentAt,omitzero"`
	Execution       *ExecutionPayload `json:"execution"`
}

type ExecutionPayload struct {
	TaskID      string               `json:"taskId"`
	TaskName    string               `json:"taskName"`
	ExecutionID string               `json:"executionId"`
	Priority    int                  `json:"priority"`
	InputValues map[string]string    `json:"inputValues"`
	Script      json.RawMessage      `json:"script"`
	Timeout     int                  `json:"timeout"`
	TaskConfig  *ExecutionTaskConfig `json:"taskConfig,omitzero"`
	// Signed PUT URL the daemon uses to upload the gzipped terminal log
	// archive. Optional: omitted (or empty) means archival is disabled for
	// this dispatch (transient station-side issuance error); the daemon keeps
	// the local file and skips upload.
	LogUploadURL string `json:"logUploadUrl,omitzero"`
	// S3 object key the daemon must echo back as `logPath` on the
	// terminal `execution:update` message after a successful upload.
	// Format `{orgId}/{yyyy-mm}/{executionId}.log.gz`. Optional: omitted
	// (or empty) whenever `logUploadUrl` is.
	LogPath string `json:"logPath,omitzero"`
}

// What the daemon does when log output exceeds logMaxSize.
type LogOnFull string

const (
	LogOnFullDropNew LogOnFull = "drop_new"
	LogOnFullDropOld LogOnFull = "drop_old"
	LogOnFullKill    LogOnFull = "kill"
)

var LogOnFullValues = []LogOnFull{LogOnFullDropNew, LogOnFullDropOld, LogOnFullKill}

// Optional per-run execution knobs the station control plane carries through
// dispatch and the daemon overlays onto the dynamically-built station task
// (`buildDynamicStationTask`). Every field is optional; an omitted field
// means "use the daemon default". Knobs not modelled here (stop signal,
// success exit codes, run-as user) are not honored by the daemon and so
// are intentionally absent — adding them would be dead surface.
type ExecutionTaskConfig struct {
	// Environment variables overlaid on the run's process env, beneath
	// the daemon's own env_file/secrets layers. This is the only way a
	// station-dispatched shell run can set process env.
	Env map[string]string `json:"env,omitzero"`
	// Window between SIGTERM and SIGKILL when a run is stopped, in
	// milliseconds (mirrors `timeout`'s unit). 0/omitted uses the daemon
	// default.
	GracefulStop int `json:"gracefulStop,omitzero"`
	// Per-run log size cap in bytes. 0/omitted uses the daemon default.
	LogMaxSize int `json:"logMaxSize,omitzero"`
	// What the daemon does when log output exceeds logMaxSize.
	LogOnFull LogOnFull `json:"logOnFull,omitzero"`
}

type ExecutionStopMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	ExecutionID     string    `json:"executionId"`
	Reason          string    `json:"reason"`
}

// State of a single service instance slot as reported by the daemon
// supervisor. `running` = live; `restarting` = exited and awaiting the
// next backoff respawn; `stopped` = operator-stopped, not being refilled;
// `fatal` = the supervisor gave up on the slot. Consumers must tolerate
// unknown values (forward-compat).
type ServiceInstanceState string

const (
	ServiceInstanceStateRunning    ServiceInstanceState = "running"
	ServiceInstanceStateRestarting ServiceInstanceState = "restarting"
	ServiceInstanceStateStopped    ServiceInstanceState = "stopped"
	ServiceInstanceStateFatal      ServiceInstanceState = "fatal"
)

var ServiceInstanceStateValues = []ServiceInstanceState{ServiceInstanceStateRunning, ServiceInstanceStateRestarting, ServiceInstanceStateStopped, ServiceInstanceStateFatal}

// Roll-up state of a whole service. `running` = all desired instances
// live; `degraded` = some but not all live; `stopped` = operator-stopped;
// `fatal` = at least one slot is fatal. Consumers must tolerate unknown
// values (forward-compat).
type ServiceState string

const (
	ServiceStateRunning  ServiceState = "running"
	ServiceStateDegraded ServiceState = "degraded"
	ServiceStateStopped  ServiceState = "stopped"
	ServiceStateFatal    ServiceState = "fatal"
)

var ServiceStateValues = []ServiceState{ServiceStateRunning, ServiceStateDegraded, ServiceStateStopped, ServiceStateFatal}

// Station → daemon. Declares (or re-declares) a service's desired state on
// the runner. Idempotent: the daemon upserts a kind=service task and, when
// `autostart` is true, brings instances up to `instances`. Re-sent when a
// runner reconnects so desired state converges after downtime. Gated by
// the `service:apply` protocol-feature token — old daemons never receive
// it. Duration knobs are milliseconds (mirrors execution timeout), which
// the daemon converts to its native nanosecond Duration fields.
type ServiceApplyMessage struct {
	Type            string               `json:"type"`
	ProtocolVersion int                  `json:"protocolVersion,omitzero"`
	SentAt          time.Time            `json:"sentAt,omitzero"`
	Service         *ServiceApplyPayload `json:"service"`
}

// Backoff curve between consecutive restarts. Omitted uses the daemon default.
type RestartBackoff string

const (
	RestartBackoffConstant    RestartBackoff = "constant"
	RestartBackoffLinear      RestartBackoff = "linear"
	RestartBackoffExponential RestartBackoff = "exponential"
)

var RestartBackoffValues = []RestartBackoff{RestartBackoffConstant, RestartBackoffLinear, RestartBackoffExponential}

type ServiceApplyPayload struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
	// ExecutionDef JSON, same shape as ExecutionPayload.script.
	Script json.RawMessage `json:"script"`
	// Desired number of always-running instances.
	Instances int `json:"instances"`
	// When true, the daemon starts instances on apply (and on reconnect).
	// When false, the service is declared but left stopped until an
	// operator start.
	Autostart bool `json:"autostart,omitzero"`
	// Base delay before each restart, in milliseconds. 0/omitted uses the daemon default.
	RestartDelay int `json:"restartDelay,omitzero"`
	// Backoff curve between consecutive restarts. Omitted uses the daemon default.
	RestartBackoff RestartBackoff `json:"restartBackoff,omitzero"`
	// An instance that runs at least this long (milliseconds) resets its
	// consecutive-restart counter. 0/omitted uses the daemon default.
	BackoffResetAfter int                  `json:"backoffResetAfter,omitzero"`
	TaskConfig        *ExecutionTaskConfig `json:"taskConfig,omitzero"`
}

type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
)

var ActionValues = []Action{ActionStart, ActionStop, ActionRestart}

// Station → daemon. An operator action against a declared service. Maps to
// the daemon's StartServiceInstances / StopService /
// RestartServiceInstances. Gated by the `service:control` feature token.
type ServiceControlMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	TaskID          string    `json:"taskId"`
	Action          Action    `json:"action"`
}

// Station → daemon. Tears down a previously declared service on the runner:
// the daemon cancels its instances and drops the kind=service task. Maps to
// the daemon's RemoveTask. Without this, a station-declared service (and its
// supervisor goroutines) would live until daemon restart, since reconcile
// only ever removes TOML-registry tasks. Gated by the `service:remove`
// protocol-feature token — old daemons never receive it.
type ServiceRemoveMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	TaskID          string    `json:"taskId"`
}

// State of one instance slot, reported in ServiceStatusMessage.
type ServiceInstanceSnapshot struct {
	// Instance slot index in [0, desiredInstances).
	Index int                  `json:"index"`
	State ServiceInstanceState `json:"state"`
	// OS process id of the live instance, when known.
	Pid *int `json:"pid,omitzero"`
	// When the current run of this slot started, when live.
	StartedAt *time.Time `json:"startedAt,omitzero"`
	// Consecutive-restart counter for this slot since its last healthy run.
	RestartCount int `json:"restartCount"`
	// Exit code of this slot's most recent terminated run, when known.
	LastExitCode *int `json:"lastExitCode,omitzero"`
}

// Daemon → station. A full per-service snapshot of the supervisor's state.
// Sent on change and on a slow heartbeat — never per log line. Station keeps
// only the latest snapshot (Valkey), never per-instance rows. Additive:
// old station ignores it; new station tolerates its absence.
type ServiceStatusMessage struct {
	Type             string                    `json:"type"`
	ProtocolVersion  int                       `json:"protocolVersion,omitzero"`
	SentAt           time.Time                 `json:"sentAt,omitzero"`
	TaskID           string                    `json:"taskId"`
	State            ServiceState              `json:"state"`
	DesiredInstances int                       `json:"desiredInstances"`
	RunningInstances int                       `json:"runningInstances"`
	Instances        []ServiceInstanceSnapshot `json:"instances"`
}

// Station → daemon. Requests a bounded historical page of log lines for an
// execution. The daemon replies with one or more log:replayChunk messages
// whose final flag marks the end of the page.
type LogReplayRequestMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	RequestID       string    `json:"requestId"`
	ExecutionID     string    `json:"executionId"`
	FromLine        int64     `json:"fromLine"`
	Limit           int64     `json:"limit"`
}

// Station → daemon. Greps one execution's on-disk log for lines matching a
// query and replies with a single log:searchChunk. A gated station→daemon
// type (X-Runner-Protocol-Features "log:searchRequest"): station only sends
// it to daemons that advertise support, since an old daemon would drop it
// and the search would time out. Scope is one execution's live/recent log
// on disk — archived (rotated/uploaded) lines are not searchable here.
type LogSearchRequestMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	RequestID       string    `json:"requestId"`
	ExecutionID     string    `json:"executionId"`
	// Substring (or RE2 regex when regex=true) to match.
	Query string `json:"query"`
	// Treat query as an RE2 regular expression.
	Regex bool `json:"regex,omitzero"`
	// Match case-sensitively (default is case-insensitive).
	CaseSensitive bool `json:"caseSensitive,omitzero"`
	// Max hits to return in the reply chunk.
	Limit int64 `json:"limit,omitzero"`
	// Resume cursor: the first absolute line to scan (0 scans the whole
	// log). Pass a previous chunk's nextLine to continue mid-run without
	// re-emitting prior hits.
	FromLine int64 `json:"fromLine,omitzero"`
}

type LogListenMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	ExecutionID     string    `json:"executionId"`
}

type LogStopMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	ExecutionID     string    `json:"executionId"`
}

// Station → daemon. Asks the daemon to gracefully restart its own process (re-exec the agent binary), e.g. after a config change or to recover a wedged agent. Carries no execution scope; it acts on the agent itself.
type AgentRestartMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
}

// The `error` frame, sent in both directions with an identical shape:
// station→daemon and daemon→station. Receivers treat unrecognized codes as
// connection-scoped diagnostics (log + metric, never a state flip).
type ProtocolErrorMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	// Canonical values: AUTH_ERROR, VALIDATION_ERROR, CONFLICT_ERROR,
	// TRANSIENT_ERROR, UNKNOWN_EXECUTION. Kept as a free-form string on
	// the wire so old peers emitting other codes still decode; receivers
	// treat unrecognized codes as connection-scoped diagnostics.
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitzero"`
	// Execution this error pertains to, when applicable. Empty (old peers
	// always omit it) means the error is connection-scoped and must not
	// flip any execution's state.
	ExecutionID string `json:"executionId,omitzero"`
}

type PingMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	ID              string    `json:"id,omitzero"`
	// Optional live system snapshot piggybacked on the heartbeat so the
	// control plane can show a runner's CPU/memory/identity without a
	// dedicated message or any DB write (station stores only the latest
	// snapshot in Valkey). Add-only field; old daemons omit it and old
	// station ignores it.
	SystemStats *SystemStatsInfo `json:"systemStats,omitzero"`
}

// A single live system-resource + identity snapshot for the runner host.
// Mirrors the daemon's local /api/system payload. Volatile fields
// (cpuUsage/memUsage/mem*) reflect the instant the heartbeat was built.
type SystemStatsInfo struct {
	// CPU usage percentage (0-100).
	CpuUsage float64 `json:"cpuUsage,omitzero"`
	// Memory usage percentage (0-100).
	MemUsage float64 `json:"memUsage,omitzero"`
	// Total memory in bytes.
	MemTotal int64 `json:"memTotal,omitzero"`
	// Used memory in bytes.
	MemUsed int64 `json:"memUsed,omitzero"`
	// Number of CPU cores.
	CpuCores int `json:"cpuCores,omitzero"`
	// Human-readable daemon uptime.
	Uptime string `json:"uptime,omitzero"`
	// RunWisp daemon version.
	Version string `json:"version,omitzero"`
	// Hostname of the runner machine.
	Host string `json:"host,omitzero"`
	// Operating system (e.g. linux, darwin, windows).
	Os string `json:"os,omitzero"`
	// CPU architecture (e.g. amd64, arm64).
	Arch string `json:"arch,omitzero"`
}

// Daemon → station. Sent immediately after an execution:dispatch passes
// validation (before the run is registered or triggered). Confirms
// receipt so the control plane can stop re-dispatching. A duplicate
// dispatch for an execution the daemon already knows is re-acked
// without starting a second run. Old daemons never send this; the
// station treats execution:update(running) as an implicit ack.
type ExecutionAckMessage struct {
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	ExecutionID     string    `json:"executionId"`
}

type ExecutionUpdateMessage struct {
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocolVersion,omitzero"`
	SentAt          time.Time       `json:"sentAt,omitzero"`
	ExecutionID     string          `json:"executionId"`
	Status          ExecutionStatus `json:"status"`
	ExitCode        *int            `json:"exitCode,omitzero"`
	StartedAt       *time.Time      `json:"startedAt,omitzero"`
	FinishedAt      *time.Time      `json:"finishedAt,omitzero"`
	// Echoed S3 object key from `ExecutionPayload.logPath`, set only on
	// terminal updates after a successful upload.
	LogPath *string `json:"logPath,omitzero"`
	// Gzipped byte count of the uploaded archive. Set only on terminal
	// updates after a successful upload.
	LogSize *int64 `json:"logSize,omitzero"`
}

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
	StreamSystem Stream = "system"
)

var StreamValues = []Stream{StreamStdout, StreamStderr, StreamSystem}

// A single log line published or replayed by the daemon.
type LogLineEntry struct {
	// Absolute, monotonically increasing line index.
	N int64 `json:"n"`
	// Unix milliseconds. 0 if unavailable.
	Ts     int64  `json:"ts,omitzero"`
	Stream Stream `json:"stream"`
	// Line content WITHOUT trailing newline and without on-disk prefix.
	Text string `json:"text"`
	// True if this segment continues an oversized split line.
	Continued bool `json:"continued,omitzero"`
}

// Daemon → station. Pushed live for any execution with an active log:listen
// subscription. Each message carries exactly one absolute-numbered line.
type LogLineMessage struct {
	LogLineEntry
	Type            string    `json:"type"`
	ProtocolVersion int       `json:"protocolVersion,omitzero"`
	SentAt          time.Time `json:"sentAt,omitzero"`
	ExecutionID     string    `json:"executionId"`
}

// Daemon → station. A coalesced batch of live log lines for one execution
// with an active log:listen subscription — the same per-line payload as
// log:line, but grouped so a burst of output ships as one frame instead
// of one frame per line (fewer WS frames, NATS publishes, and SSE writes
// on the hot path). Lines are in ascending `n` order. An additive
// daemon→station type: old station never receives it, and a daemon may still
// send single log:line frames — a receiver must handle both.
type LogLinesMessage struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocolVersion,omitzero"`
	SentAt          time.Time      `json:"sentAt,omitzero"`
	ExecutionID     string         `json:"executionId"`
	Lines           []LogLineEntry `json:"lines"`
}

// Daemon → station. One page of historical log lines in reply to a
// log:replayRequest. A reply may span multiple chunks; final=true marks
// the last chunk.
type LogReplayChunkMessage struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocolVersion,omitzero"`
	SentAt          time.Time      `json:"sentAt,omitzero"`
	RequestID       string         `json:"requestId"`
	ExecutionID     string         `json:"executionId"`
	Lines           []LogLineEntry `json:"lines"`
	Final           bool           `json:"final"`
}

// Daemon → station. The single reply to a log:searchRequest. hits are
// ordered by ascending line number within the execution. exhausted=true
// means the daemon scanned the whole on-disk log; when false, nextLine
// carries the resume cursor (pass it back as fromLine to page on).
type LogSearchChunkMessage struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocolVersion,omitzero"`
	SentAt          time.Time      `json:"sentAt,omitzero"`
	RequestID       string         `json:"requestId"`
	ExecutionID     string         `json:"executionId"`
	Hits            []LogLineEntry `json:"hits"`
	// First absolute line still to scan when exhausted=false; 0 and
	// exhausted=true means the scan is complete.
	NextLine  int64 `json:"nextLine,omitzero"`
	Exhausted bool  `json:"exhausted"`
}
