// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
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

// NotifierSpec is one [[notifier]] block, post-decode but pre-secret-resolution.
// The secret-bearing fields (WebhookURL, BotToken, Password) hold the raw value
// exactly as written in TOML — an inline literal or a ${VAR} / ${file:/path}
// placeholder. Placeholders are resolved late, in internal/notify/configload,
// so the resolved secret never lives in config.Config and never reaches the
// REST API or UI.
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
// RevealVars lists environment-variable names whose resolved value may be
// shown in the REST API / Web UI. Free-form fields (inline env values,
// description, group) that interpolate only revealed vars display their
// resolved value; anything else displays the raw ${...} placeholder. The
// default — forgetting to list a var — always hides, so a secret can never
// leak by omission. ${file:...} values can never be revealed.
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
	RevealVars         []string      `toml:"-"`
}

// Defaults provides fallback values applied to every task.
//
// All durations / sizes are parsed from their TOML string form (e.g. "1h",
// "100mb") at config load time and stored as native Go types.
//
// BackoffResetAfter is the minimum service-instance run duration that resets
// the per-instance restart counter. Instances that survive at least this long
// are treated as healthy; the next failure starts the backoff curve over.
type Defaults struct {
	Timeout           time.Duration
	LogMaxSize        int64
	LogOnFull         string
	KeepRuns          int
	KeepFor           time.Duration
	BackoffResetAfter time.Duration

	// Env is the inline env block from [defaults.env]. Visible in API/UI.
	Env map[string]string
	// EnvFile is the path string from defaults.env_file as written by the
	// operator (relative paths resolve against the runwisp.toml directory at
	// load time).
	EnvFile string
	// SecretEnv is populated by the config loader from EnvFile.
	SecretEnv map[string]string
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

// taskWire is the over-the-wire task shape used only during TOML decoding.
// It exists so api_trigger can be distinguished between "absent" (nil, default true)
// and "explicitly false" (&false).
type taskWire struct {
	Group       string `toml:"group,omitempty"`
	Description string `toml:"description,omitempty"`

	Cron           string                `toml:"cron,omitempty"`
	Timezone       string                `toml:"timezone,omitempty"`
	APITrigger     *bool                 `toml:"api_trigger,omitempty"`
	CatchUp        model.MissedRunPolicy `toml:"catch_up,omitempty"`
	MaxCatchUpRuns int                   `toml:"max_catch_up_runs,omitempty"`

	Timeout       string                  `toml:"timeout,omitempty"`
	GracefulStop  string                  `toml:"graceful_stop,omitempty"`
	Restart       model.RestartPolicy     `toml:"restart,omitempty"`
	MaxConcurrent int                     `toml:"max_concurrent,omitempty"`
	QueueMax      int                     `toml:"queue_max,omitempty"`
	OnOverlap     model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`

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

	// ComposeFile / ComposeService route the task through ComposeBackend
	// instead of ShellBackend. Mutually exclusive with Run.
	ComposeFile    string `toml:"compose_file,omitempty"`
	ComposeService string `toml:"compose_service,omitempty"`

	Env     map[string]string `toml:"env,omitempty"`
	EnvFile string            `toml:"env_file,omitempty"`

	NotifyOnFailure []string `toml:"notify_on_failure,omitempty"`
	NotifyOnSuccess []string `toml:"notify_on_success,omitempty"`
}

// serviceWire is the over-the-wire shape for [services.*] entries. Cron and
// catch_up are intentionally omitted — services are not cron-driven. Services
// have no max_concurrent or queue_max: instance count is governed by `instances`
// and overlap behaviour by `on_overlap`.
type serviceWire struct {
	Group       string `toml:"group,omitempty"`
	Description string `toml:"description,omitempty"`

	APITrigger *bool `toml:"api_trigger,omitempty"`

	Timeout      string                  `toml:"timeout,omitempty"`
	GracefulStop string                  `toml:"graceful_stop,omitempty"`
	OnOverlap    model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`
	Instances    int                     `toml:"instances,omitempty"`

	RestartDelay      string `toml:"restart_delay,omitempty"`
	RestartBackoff    string `toml:"restart_backoff,omitempty"`
	BackoffResetAfter string `toml:"backoff_reset_after,omitempty"`

	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`

	KeepRuns int    `toml:"keep_runs,omitempty"`
	KeepFor  string `toml:"keep_for,omitempty"`

	Run string `toml:"run,omitempty"`

	// ComposeFile / ComposeService route the service through ComposeBackend
	// instead of ShellBackend. Mutually exclusive with Run.
	ComposeFile    string `toml:"compose_file,omitempty"`
	ComposeService string `toml:"compose_service,omitempty"`

	Env     map[string]string `toml:"env,omitempty"`
	EnvFile string            `toml:"env_file,omitempty"`

	NotifyOnFailure []string `toml:"notify_on_failure,omitempty"`
	NotifyOnSuccess []string `toml:"notify_on_success,omitempty"`
}

// defaultsWire mirrors [defaults] before parsing.
type defaultsWire struct {
	Timeout           string `toml:"timeout,omitempty"`
	LogMaxSize        string `toml:"log_max_size,omitempty"`
	LogOnFull         string `toml:"log_on_full,omitempty"`
	KeepRuns          int    `toml:"keep_runs,omitempty"`
	KeepFor           string `toml:"keep_for,omitempty"`
	BackoffResetAfter string `toml:"backoff_reset_after,omitempty"`

	Env     map[string]string `toml:"env,omitempty"`
	EnvFile string            `toml:"env_file,omitempty"`
}

// storageWire mirrors [storage] before parsing.
type storageWire struct {
	MaxSize      string `toml:"max_size,omitempty"`
	MinFreeSpace string `toml:"min_free_space,omitempty"`
}

// tomlConfig is the over-the-wire config shape used only during TOML decoding.
//
// Compose is decoded as a free-form map: each [compose.<alias>] block mixes
// reserved scalar keys (file, mode, include, …) with per-service override
// sub-tables, so we destructure the alias map in internal/config/compose.go
// rather than via direct struct binding. See parseComposeBlock there.
type tomlConfig struct {
	Daemon    daemonWire                `toml:"daemon,omitempty"`
	Storage   storageWire               `toml:"storage,omitempty"`
	Defaults  defaultsWire              `toml:"defaults,omitempty"`
	Scheduler schedulerWire             `toml:"scheduler,omitempty"`
	Tasks     map[string]*taskWire      `toml:"tasks,omitempty"`
	Services  map[string]*serviceWire   `toml:"services,omitempty"`
	Compose   map[string]map[string]any `toml:"compose,omitempty"`
	Notify    notifyWire                `toml:"notify,omitempty"`

	Notifiers []notifierWire `toml:"notifier,omitempty"`
	Routes    []routeWire    `toml:"notification_route,omitempty"`
}

// daemonWire mirrors [daemon] before parsing — the duration string for
// shutdown_timeout is parsed at config-load time.
type daemonWire struct {
	AllowCloudDispatch bool     `toml:"allow_cloud_dispatch,omitempty"`
	ShutdownTimeout    string   `toml:"shutdown_timeout,omitempty"`
	ExternalURL        string   `toml:"external_url,omitempty"`
	MetricsEnabled     bool     `toml:"metrics_enabled,omitempty"`
	MetricsListen      string   `toml:"metrics_listen,omitempty"`
	RevealVars         []string `toml:"reveal_vars,omitempty"`
}

// schedulerWire mirrors [scheduler] before parsing.
type schedulerWire struct {
	Timezone string `toml:"timezone,omitempty"`
}

// notifyWire mirrors the [notify] block before parsing. GlobalNotifiers is a
// pointer so we can distinguish "key omitted" (apply built-in default of
// ["inapp"]) from "key set to []" (operator explicitly opted out of the
// in-app safety net).
type notifyWire struct {
	DefaultTimeout  string    `toml:"default_timeout,omitempty"`
	GlobalNotifiers *[]string `toml:"global_notifiers,omitempty"`
	HistoryKeep     int       `toml:"history_keep,omitempty"`
	HistoryKeepFor  string    `toml:"history_keep_for,omitempty"`
	CoalesceWindow  string    `toml:"coalesce_window,omitempty"`
	OccurrenceRing  int       `toml:"occurrence_ring,omitempty"`
	// CoalesceOutbound is *bool so we can distinguish "unset" (default-on)
	// from explicit `coalesce_outbound = false` (the rare opt-out).
	CoalesceOutbound *bool `toml:"coalesce_outbound,omitempty"`
}

// notifierWire is one [[notifier]] block before secret resolution.
type notifierWire struct {
	ID   string `toml:"id"`
	Type string `toml:"type"`

	WebhookURL string `toml:"webhook_url,omitempty"`
	Channel    string `toml:"channel,omitempty"`

	BotToken  string `toml:"bot_token,omitempty"`
	ChatID    string `toml:"chat_id,omitempty"`
	ParseMode string `toml:"parse_mode,omitempty"`

	Host          string   `toml:"host,omitempty"`
	Port          int      `toml:"port,omitempty"`
	TLS           string   `toml:"tls,omitempty"`
	TLSSkipVerify bool     `toml:"tls_skip_verify,omitempty"`
	Username      string   `toml:"username,omitempty"`
	Password      string   `toml:"password,omitempty"`
	From          string   `toml:"from,omitempty"`
	ReplyTo       string   `toml:"reply_to,omitempty"`
	To            []string `toml:"to,omitempty"`
	CC            []string `toml:"cc,omitempty"`
	BCC           []string `toml:"bcc,omitempty"`

	TemplatePath string `toml:"template_path,omitempty"`
}

// routeWire is one [[notification_route]] block before validation.
type routeWire struct {
	Match  routeMatchWire `toml:"match"`
	Notify []string       `toml:"notify"`
}

type routeMatchWire struct {
	Kind     []string `toml:"kind,omitempty"`
	Severity string   `toml:"severity,omitempty"`
	Task     string   `toml:"task,omitempty"`
}

func (w *taskWire) toTask(name string) (model.Task, error) {
	apiTrigger := true
	if w.APITrigger != nil {
		apiTrigger = *w.APITrigger
	}
	timeout, err := parseDuration(w.Timeout)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid timeout for task %q: %w", name, err)
	}
	gracefulStop, err := parseDuration(w.GracefulStop)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid graceful_stop for task %q: %w", name, err)
	}
	retryDelay, err := parseDuration(w.RetryDelay)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid retry_delay for task %q: %w", name, err)
	}
	keepFor, err := parseKeepFor(w.KeepFor)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid keep_for for task %q: %w", name, err)
	}
	keepRuns, err := parseKeepRuns(w.KeepRuns)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid keep_runs for task %q: %w", name, err)
	}
	logMaxSize, err := parseLogMaxSize(w.LogMaxSize)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid log_max_size for task %q: %w", name, err)
	}
	task := model.Task{
		Name:           name,
		Kind:           model.KindTask,
		Group:          w.Group,
		Description:    w.Description,
		Cron:           w.Cron,
		Timezone:       w.Timezone,
		APITrigger:     apiTrigger,
		CatchUp:        w.CatchUp,
		MaxCatchUpRuns: w.MaxCatchUpRuns,
		Timeout:        timeout,
		GracefulStop:   gracefulStop,
		Restart:        w.Restart,
		MaxConcurrent:  w.MaxConcurrent,
		QueueMax:       w.QueueMax,
		OnOverlap:      w.OnOverlap,
		RetryAttempts:  w.RetryAttempts,
		RetryDelay:     retryDelay,
		RetryBackoff:   w.RetryBackoff,
		LogMaxSize:     logMaxSize,
		LogOnFull:      w.LogOnFull,
		KeepRuns:       keepRuns,
		KeepFor:        keepFor,
		Run:            w.Run,
		Env:            w.Env,
		EnvFile:        w.EnvFile,
	}
	if w.ComposeFile != "" {
		svc := w.ComposeService
		if svc == "" {
			svc = name
		}
		task.ExecutionDef = &model.ComposeExecution{
			File:        w.ComposeFile,
			ProjectName: name,
			Service:     svc,
			Mode:        model.ComposeModeServices,
		}
		task.Compose = &model.TaskComposeRef{
			File:        w.ComposeFile,
			Service:     svc,
			ProjectName: name,
		}
	} else if w.ComposeService != "" {
		return model.Task{}, fmt.Errorf("task %q sets compose_service without compose_file", name)
	}
	return task, nil
}

func (w *serviceWire) toTask(name string) (model.Task, error) {
	apiTrigger := true
	if w.APITrigger != nil {
		apiTrigger = *w.APITrigger
	}
	timeout, err := parseDuration(w.Timeout)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid timeout for task %q: %w", name, err)
	}
	gracefulStop, err := parseDuration(w.GracefulStop)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid graceful_stop for task %q: %w", name, err)
	}
	restartDelay, err := parseDuration(w.RestartDelay)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid restart_delay for task %q: %w", name, err)
	}
	backoffReset, err := parseDuration(w.BackoffResetAfter)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid backoff_reset_after for task %q: %w", name, err)
	}
	keepFor, err := parseKeepFor(w.KeepFor)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid keep_for for task %q: %w", name, err)
	}
	keepRuns, err := parseKeepRuns(w.KeepRuns)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid keep_runs for task %q: %w", name, err)
	}
	logMaxSize, err := parseLogMaxSize(w.LogMaxSize)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid log_max_size for task %q: %w", name, err)
	}
	task := model.Task{
		Name:              name,
		Kind:              model.KindService,
		Group:             w.Group,
		Description:       w.Description,
		APITrigger:        apiTrigger,
		Timeout:           timeout,
		GracefulStop:      gracefulStop,
		Restart:           model.RestartAlways,
		OnOverlap:         w.OnOverlap,
		Instances:         w.Instances,
		RestartDelay:      restartDelay,
		RestartBackoff:    w.RestartBackoff,
		BackoffResetAfter: backoffReset,
		LogMaxSize:        logMaxSize,
		LogOnFull:         w.LogOnFull,
		KeepRuns:          keepRuns,
		KeepFor:           keepFor,
		Run:               w.Run,
		Env:               w.Env,
		EnvFile:           w.EnvFile,
	}
	if w.ComposeFile != "" {
		svc := w.ComposeService
		if svc == "" {
			svc = name
		}
		task.ExecutionDef = &model.ComposeExecution{
			File:        w.ComposeFile,
			ProjectName: name,
			Service:     svc,
			Mode:        model.ComposeModeServices,
		}
		task.Compose = &model.TaskComposeRef{
			File:        w.ComposeFile,
			Service:     svc,
			ProjectName: name,
		}
	} else if w.ComposeService != "" {
		return model.Task{}, fmt.Errorf("service %q sets compose_service without compose_file", name)
	}
	return task, nil
}

func (w *defaultsWire) toDefaults() (Defaults, error) {
	timeout, err := parseDuration(w.Timeout)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.timeout: %w", err)
	}
	keepFor, err := parseKeepFor(w.KeepFor)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.keep_for: %w", err)
	}
	keepRuns, err := parseKeepRuns(w.KeepRuns)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.keep_runs: %w", err)
	}
	logMaxSize, err := parseLogMaxSize(w.LogMaxSize)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.log_max_size: %w", err)
	}
	backoffReset, err := parseDuration(w.BackoffResetAfter)
	if err != nil {
		return Defaults{}, fmt.Errorf("invalid defaults.backoff_reset_after: %w", err)
	}
	return Defaults{
		Timeout:           timeout,
		LogMaxSize:        logMaxSize,
		LogOnFull:         w.LogOnFull,
		KeepRuns:          keepRuns,
		KeepFor:           keepFor,
		BackoffResetAfter: backoffReset,
		Env:               w.Env,
		EnvFile:           w.EnvFile,
	}, nil
}

func (w *daemonWire) toDaemon() (Daemon, error) {
	shutdown, err := parseDuration(w.ShutdownTimeout)
	if err != nil {
		return Daemon{}, fmt.Errorf("invalid daemon.shutdown_timeout: %w", err)
	}
	externalURL, err := parseExternalURL(w.ExternalURL)
	if err != nil {
		return Daemon{}, err
	}
	metricsListen, err := parseMetricsListen(w.MetricsListen)
	if err != nil {
		return Daemon{}, err
	}
	if metricsListen != "" && !w.MetricsEnabled {
		return Daemon{}, fmt.Errorf("invalid daemon.metrics_listen: set without daemon.metrics_enabled = true")
	}
	return Daemon{
		AllowCloudDispatch: w.AllowCloudDispatch,
		ShutdownTimeout:    shutdown,
		ExternalURL:        externalURL,
		MetricsEnabled:     w.MetricsEnabled,
		MetricsListen:      metricsListen,
		RevealVars:         w.RevealVars,
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
