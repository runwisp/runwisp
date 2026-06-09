// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// Config is the in-memory representation of runwisp.toml after load + defaults.
type Config struct {
	Tasks     []model.Task
	Defaults  Defaults
	Storage   Storage
	Daemon    Daemon
	Notify    NotifyConfig
	Scheduler Scheduler

	// pendingComposeBlocks holds raw [compose.<alias>] tables captured by
	// decode(), to be consumed by expandComposeBlocks() during Load. Once
	// expansion has run this field is set to nil so it never leaks into
	// downstream consumers. Tests that call decode() directly will see the
	// raw blocks here.
	pendingComposeBlocks map[string]map[string]any
}

// Scheduler holds scheduler-wide settings. Timezone is the IANA name used to
// evaluate cron expressions for any task that doesn't pin its own. When the
// operator omits [scheduler] timezone, ApplyDefaults fills it in from the
// host's system timezone (Source = "system"); when the operator sets it
// explicitly, Source = "config".
type Scheduler struct {
	Timezone string
	Source   string
}

// Timezone source tags. Phase 3 surfaces these in the TUI startup banner and
// the Web UI header so operators can confirm at a glance which clock the
// daemon picked.
const (
	TimezoneSourceConfig = "config"
	TimezoneSourceSystem = "system"
)

// NotifyConfig is the resolved notification configuration. Secrets are not
// resolved at this stage — the configload helper in internal/notify/configload
// reads NotifierSpecs and produces a runnable notify.Service. Values stored
// here are plain types so the config package does not need to import notify.
//
// GlobalNotifiers always reflects the operator's intent post-defaults:
// missing/omitted resolves to ["inapp"], an explicit empty list disables the
// zero-config safety net, and any list (e.g. ["slack-ops"]) becomes the
// channels that fire on every failure plus get appended to per-task
// notify_* sugar.
type NotifyConfig struct {
	Notifiers        []NotifierSpec
	Routes           []NotificationRoute
	GlobalNotifiers  []string
	DefaultTimeout   time.Duration
	HistoryKeep      int
	HistoryKeepFor   time.Duration
	CoalesceWindow   time.Duration
	OccurrenceRing   int
	CoalesceOutbound bool
}

// NotifierSpec is one [[notifier]] block, post-decode. Secret-bearing fields
// (webhook_url, bot_token, password) hold their final values already — the
// ${VAR} / ${file:...} substitution pass resolved any indirection at decode
// time.
type NotifierSpec struct {
	ID   string
	Type string

	// Slack-specific
	WebhookURL   string
	SlackChannel string

	// Telegram-specific
	BotToken  string
	ChatID    string
	ParseMode string

	// SMTP-specific
	Host          string
	Port          int
	TLSMode       string // "starttls" | "implicit" | "none" | "" (port-derived)
	TLSSkipVerify bool
	Username      string
	Password      string
	From          string
	ReplyTo       string
	Recipients    []string // To:
	CC            []string
	BCC           []string

	// Webhook-specific
	URL     string
	Headers map[string]string

	TemplatePath string
}

// NotificationRoute pairs a predicate description with target action IDs.
// Match values are stored as strings (kinds, severity, glob); the consumer
// in internal/notify/configload compiles them into notify.Predicate.
type NotificationRoute struct {
	Kinds      []string
	Severity   string
	TaskGlob   string
	NotifierID []string
}

// Daemon holds daemon-wide toggles.
//
// ShutdownTimeout caps how long the daemon waits for in-flight tasks to drain
// after SIGTERM before forcing exit. The default matches Docker's 10-second
// stop-grace so a containerised daemon never leaves orphans.
//
// ExternalURL is the operator-supplied public base URL of the embedded Web UI
// (e.g. "https://runwisp.example.com"). When set, notification renderers
// build deep-links into the dashboard; when empty, link lines are omitted
// from outbound messages rather than rendered as broken URLs.
//
// MetricsEnabled gates the OpenMetrics /metrics endpoint. Default off: the
// per-task labels and the daemon version label are information disclosure
// that a publicly-exposed daemon shouldn't leak by default. Operators who
// scrape with Prometheus opt in. MetricsListen, when non-empty, binds the
// metrics endpoint to a separate address (e.g. "127.0.0.1:9478") instead of
// sharing the main UI/REST listener — useful when --host exposes the UI
// publicly but the scrape surface should stay on loopback.
type Daemon struct {
	AllowCloudDispatch bool          `toml:"-"`
	ShutdownTimeout    time.Duration `toml:"-"`
	ExternalURL        string        `toml:"-"`
	MetricsEnabled     bool          `toml:"-"`
	MetricsListen      string        `toml:"-"`
}

// Defaults provides fallback values applied to every task.
//
// All durations / sizes are parsed from their TOML string form (e.g. "1h",
// "100mb") at config load time and stored as native Go types.
//
// HealthyAfter is the minimum service-instance run duration that marks an
// instance as healthy: reaching it both resets the per-instance restart counter
// and clears the failed-start streak. Instances that survive at least this long
// are treated as healthy; the next failure starts the backoff curve over.
type Defaults struct {
	Timeout      time.Duration
	Shell        string
	StopSignal   string
	ExitCodes    []int
	LogMaxSize   int64
	LogOnFull    string
	KeepRuns     int
	KeepFor      time.Duration
	HealthyAfter time.Duration
	// StartRetries is the default number of consecutive fast failures a service
	// instance may accrue before going FATAL. Zero means "unset" — services
	// fall back to DefaultStartRetries.
	StartRetries int

	// NotifyOnMissed is the [defaults] override for per-task missed-run alerts.
	// nil means the operator didn't set it, so the built-in default (true)
	// applies. ApplyDefaults resolves each task's pointer from this.
	NotifyOnMissed *bool

	// Env is the inline env block from [defaults.env]; env_file values merge
	// in beneath it at load time. Visible in API/UI.
	Env map[string]string
	// EnvFile is the path string from defaults.env_file as written by the
	// operator (relative paths resolve against the runwisp.toml directory at
	// load time).
	EnvFile string
	// Secrets is the inline [defaults.secrets] block plus secrets_file-derived
	// pairs. Never serialized — values stay inside the daemon.
	Secrets map[string]string
	// SecretsFile is the path string from defaults.secrets_file as written by
	// the operator.
	SecretsFile string
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

// MaxServiceInstances caps the number of instances a single service can request.
const MaxServiceInstances = 64
