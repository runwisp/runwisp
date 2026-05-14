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
// The Source* fields tell the secret resolver which channel to read from
// (env, file, inline). Exactly one of the three is set per secret-bearing
// field, enforced by Validate.
type NotifierSpec struct {
	ID   string
	Type string

	// Slack-specific
	WebhookURL     string
	WebhookURLEnv  string
	WebhookURLFile string
	SlackChannel   string

	// Telegram-specific
	BotToken     string
	BotTokenEnv  string
	BotTokenFile string
	ChatID       string
	ParseMode    string

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
type Daemon struct {
	AllowCloudDispatch bool          `toml:"-"`
	ShutdownTimeout    time.Duration `toml:"-"`
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
}

// storageWire mirrors [storage] before parsing.
type storageWire struct {
	MaxSize      string `toml:"max_size,omitempty"`
	MinFreeSpace string `toml:"min_free_space,omitempty"`
}

// tomlConfig is the over-the-wire config shape used only during TOML decoding.
type tomlConfig struct {
	Daemon    daemonWire              `toml:"daemon,omitempty"`
	Storage   storageWire             `toml:"storage,omitempty"`
	Defaults  defaultsWire            `toml:"defaults,omitempty"`
	Scheduler schedulerWire           `toml:"scheduler,omitempty"`
	Tasks     map[string]*taskWire    `toml:"tasks,omitempty"`
	Services  map[string]*serviceWire `toml:"services,omitempty"`
	Notify    notifyWire              `toml:"notify,omitempty"`

	Notifiers []notifierWire `toml:"notifier,omitempty"`
	Routes    []routeWire    `toml:"notification_route,omitempty"`
}

// daemonWire mirrors [daemon] before parsing — the duration string for
// shutdown_timeout is parsed at config-load time.
type daemonWire struct {
	AllowCloudDispatch bool   `toml:"allow_cloud_dispatch,omitempty"`
	ShutdownTimeout    string `toml:"shutdown_timeout,omitempty"`
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

	WebhookURL     string `toml:"webhook_url,omitempty"`
	WebhookURLEnv  string `toml:"webhook_url_env,omitempty"`
	WebhookURLFile string `toml:"webhook_url_file,omitempty"`
	Channel        string `toml:"channel,omitempty"`

	BotToken     string `toml:"bot_token,omitempty"`
	BotTokenEnv  string `toml:"bot_token_env,omitempty"`
	BotTokenFile string `toml:"bot_token_file,omitempty"`
	ChatID       string `toml:"chat_id,omitempty"`
	ParseMode    string `toml:"parse_mode,omitempty"`

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
	return model.Task{
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
	}, nil
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
	return model.Task{
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
	}, nil
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
	}, nil
}

func (w *daemonWire) toDaemon() (Daemon, error) {
	shutdown, err := parseDuration(w.ShutdownTimeout)
	if err != nil {
		return Daemon{}, fmt.Errorf("invalid daemon.shutdown_timeout: %w", err)
	}
	return Daemon{
		AllowCloudDispatch: w.AllowCloudDispatch,
		ShutdownTimeout:    shutdown,
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
