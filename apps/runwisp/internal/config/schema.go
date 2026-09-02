// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"time"

	"github.com/runwisp/runwisp/internal/cronprobe"
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

	// includeFiles are the absolute paths of the TOML files merged in via
	// [daemon].include at this load. includeGlobs are those patterns resolved
	// against the root config dir. watchFiles is every additional on-disk input
	// Snapshot should hash (included TOMLs + env_files, each resolved against
	// its declaring file's dir). All three are load-time bookkeeping for
	// Snapshot and never reach the API/UI; empty for a single-file config with
	// no env_file.
	includeFiles []string
	includeGlobs []string
	watchFiles   []string

	// cronFiles are the absolute paths of the crontabs read as live task sources
	// via [daemon].include_cron at this load, in the order they were merged.
	// cronGlobs are those patterns resolved against the root config dir, and
	// cronMatched every path they matched — including a crontab that then failed
	// to load, since staleness asks what the globs match, not what parsed. Kept
	// apart from includeGlobs/includeFiles because the two are expanded by
	// different rules; see snapshotPins.
	cronFiles   []string
	cronGlobs   []string
	cronMatched []string

	// cronDaemon records whether a system cron daemon looked live when this config
	// was resolved, and in what sense ("is running", "is enabled and will start on
	// the next boot", …). Probed once rather than per question: it costs a
	// systemctl exec, and Warnings is answered on every /api/daemon request.
	//
	// Not fixed for the life of the config. The daemon re-probes on a timer and
	// swaps in a re-derived config via WithCronHold, because the alternative — a
	// hold that only lifts on an explicit reload — leaves an operator who retires
	// cron and forgets to reload with jobs neither scheduler runs. Scheduling is
	// still a pure function of the config the scheduler holds; what this field
	// records is a fact about the machine, not a setting from runwisp.toml.
	cronDaemon cronprobe.State

	// cronBlocks maps a cron-sourced task name to the TOML that produced it, so
	// `runwisp promote` can move the definition the daemon is actually running
	// rather than re-deriving one that might differ.
	cronBlocks map[string]string

	// cronLines maps a cron-sourced task name to where its definition physically
	// lives in the crontab it came from, so `runwisp promote` can verify the exact
	// line before commenting it out — see cronLineOrigin and CronSourceLine.
	cronLines map[string]cronLineOrigin

	// CronFindings lists what an operator should know about those crontabs: the
	// jobs RunWisp declined to schedule, and the ones running under a name the
	// crontab doesn't mention. Exported, unlike the bookkeeping above, because a
	// skipped job is a failure with no run record to make it visible — Warnings and
	// /api/daemon are the only places it can surface. See CronFinding.
	CronFindings []CronFinding

	// origins maps each task/service/compose-alias name to the absolute path of
	// the config file that defined it. Load-time bookkeeping behind OriginFile;
	// never reaches the API/UI, which sees the derived Task.Source instead.
	origins map[string]string
}

// CronFiles returns the crontabs this config read live task definitions from.
func (c *Config) CronFiles() []string { return c.cronFiles }

// CronBlockTOML returns the TOML block behind a cron-sourced task, and false for
// any other name. `runwisp promote` appends it to the operator's root config.
func (c *Config) CronBlockTOML(name string) (string, bool) {
	block, ok := c.cronBlocks[name]
	return block, ok
}

// CronSourceLine returns where a cron-sourced task's definition physically
// lives — the crontab file, its 1-based line, and the exact text that line held
// at load time — and false for any other name, including a cron-sourced one
// whose line couldn't be captured. `runwisp promote` re-reads the file at that
// line and refuses the whole move on any mismatch, rather than touching a line
// that has since changed underneath it.
func (c *Config) CronSourceLine(name string) (file string, line int, text string, ok bool) {
	o, ok := c.cronLines[name]
	if !ok {
		return "", 0, "", false
	}
	return o.File, o.Line, o.Text, true
}

// OriginFile returns the absolute path of the config file that defined the
// named task, service, or compose alias. It returns "" for a name the config
// doesn't define and for compose-generated tasks, whose definition comes from
// the compose file's alias rather than a TOML table of their own.
//
// This is how a caller tells a hand-authored entry from one that lives in the
// machine-owned staging file (compare against StagingFilePath) or a crontab read
// via include_cron — the loader derives Task.Source from it, and `promote` uses it
// to decide which file to move a task out of.
func (c *Config) OriginFile(name string) string {
	return c.origins[name]
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

// Timezone source tags, surfaced in the TUI startup banner and the Web UI
// header so operators can confirm at a glance which clock the daemon picked.
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
	Notifiers         []NotifierSpec
	Routes            []NotificationRoute
	GlobalNotifiers   []string
	RetryBudget       time.Duration
	KeepNotifications int
	KeepFor           time.Duration
	CoalesceWindow    time.Duration
	KeepOccurrences   int
	CoalesceOutbound  bool
}

// NotifierSpec is one [notifiers.<id>] block, post-decode. Secret-bearing fields
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
	TLSMode       string // "starttls" | "implicit" | "off" | "" (port-derived)
	TLSSkipVerify bool
	Username      string
	Password      string
	From          string
	ReplyTo       string
	Recipients    []string // To:
	CC            []string
	BCC           []string

	// sendmail-specific: an explicit MTA binary, empty meaning "find the system
	// one". Addressing (From/Recipients/CC/BCC) is shared with SMTP.
	SendmailPath string

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
//
// TLS controls transport encryption for the main UI/REST listener. "off"
// (the default) forces plain HTTP everywhere (the operator is terminating TLS
// at a reverse proxy, trusts the network, or hasn't opted in yet); "auto"
// serves plain HTTP on loopback but self-signs and serves HTTPS the moment
// the bind host is non-loopback, so a network-exposed daemon can be encrypted
// with zero further operator effort once opted in. TLSCert/TLSKey, when both
// set, supply an operator-provided certificate and key that take precedence
// over auto self-signing on any bind.
type Daemon struct {
	AllowCloudDispatch bool
	ShutdownTimeout    time.Duration
	ExternalURL        string
	MetricsEnabled     bool
	MetricsListen      string
	TLS                string
	TLSCert            string
	TLSKey             string
	// CheckUpdates polls GitHub in the background for a newer release and
	// surfaces it in the Web UI / TUI. Default on; disable to keep the daemon
	// fully offline. Restart-only, like every other [daemon] key.
	CheckUpdates bool
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
	Timeout time.Duration
	// Jitter is the [defaults] start-spread window inherited by cron tasks that
	// don't set their own. Tasks-only: services never inherit it (they start
	// every instance at boot). The deliberate exception to "don't default
	// cron-tied keys" — one line in [defaults] spreads the whole task set.
	Jitter       time.Duration
	Shell        string
	StopSignal   string
	ExitCodes    []int
	LogMaxSize   int64
	LogOnFull    string
	KeepRuns     *int
	KeepFor      time.Duration
	HealthyAfter time.Duration
	// RestartAttempts is the default number of consecutive fast failures a service
	// instance may accrue before going FATAL. Zero means "unset" — services
	// fall back to DefaultStartRetries.
	RestartAttempts int

	// TreatMissedAsFailure is the [defaults] override for per-task missed-run alerts.
	// nil means the operator didn't set it, so the built-in default (true)
	// applies. ApplyDefaults resolves each task's pointer from this.
	TreatMissedAsFailure *bool

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

// IsCloudDispatchEnabled reports whether the daemon accepts peer-dispatched
// ad-hoc shell, container, or compose runs.
func (cfg *Config) IsCloudDispatchEnabled() bool {
	return cfg.Daemon.AllowCloudDispatch
}

// MaxServiceInstances caps the number of instances a single service can request.
const MaxServiceInstances = 64
