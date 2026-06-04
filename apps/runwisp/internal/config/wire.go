// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/model"
)

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

// taskServiceWireCore holds the TOML keys shared by [tasks.*] and [services.*]
// entries. It is embedded anonymously in taskWire and serviceWire so go-toml
// decodes its fields as if they were declared on the outer struct.
type taskServiceWireCore struct {
	Group       string `toml:"group,omitempty"`
	Description string `toml:"description,omitempty"`

	APITrigger *bool                   `toml:"api_trigger,omitempty"`
	OnOverlap  model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`

	Timeout      string `toml:"timeout,omitempty"`
	GracefulStop string `toml:"graceful_stop,omitempty"`

	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`

	KeepRuns int    `toml:"keep_runs,omitempty"`
	KeepFor  string `toml:"keep_for,omitempty"`

	// Run is exempt from ${...} substitution (expand:"-"): the shell expands
	// $VAR / ${VAR} at runtime with the full process env, secrets included.
	Run string `toml:"run,omitempty" expand:"-"`

	// ComposeFile / ComposeService route the task through ComposeBackend
	// instead of ShellBackend. Mutually exclusive with Run.
	ComposeFile    string `toml:"compose_file,omitempty"`
	ComposeService string `toml:"compose_service,omitempty"`

	Env         map[string]string `toml:"env,omitempty"`
	EnvFile     string            `toml:"env_file,omitempty"`
	Secrets     map[string]string `toml:"secrets,omitempty"`
	SecretsFile string            `toml:"secrets_file,omitempty"`

	NotifyOnFailure []string `toml:"notify_on_failure,omitempty"`
	NotifyOnSuccess []string `toml:"notify_on_success,omitempty"`
}

// toTaskCore parses the shared wire fields into a model.Task skeleton with
// name/kind stamped and the compose backend resolved. Callers layer their
// task-only / service-only fields on top. label ("task" or "service") names
// the entry kind in error messages.
func (w *taskServiceWireCore) toTaskCore(name, label string, kind model.TaskKind) (model.Task, error) {
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
		Name:         name,
		Kind:         kind,
		Group:        w.Group,
		Description:  w.Description,
		APITrigger:   apiTrigger,
		OnOverlap:    w.OnOverlap,
		Timeout:      timeout,
		GracefulStop: gracefulStop,
		LogMaxSize:   logMaxSize,
		LogOnFull:    w.LogOnFull,
		KeepRuns:     keepRuns,
		KeepFor:      keepFor,
		Run:          w.Run,
		Env:          w.Env,
		EnvFile:      w.EnvFile,
		Secrets:      w.Secrets,
		SecretsFile:  w.SecretsFile,
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
		return model.Task{}, fmt.Errorf("%s %q sets compose_service without compose_file", label, name)
	}
	return task, nil
}

// taskWire is the over-the-wire task shape used only during TOML decoding.
// It exists so api_trigger can be distinguished between "absent" (nil, default true)
// and "explicitly false" (&false).
type taskWire struct {
	taskServiceWireCore

	Cron           string                `toml:"cron,omitempty"`
	Timezone       string                `toml:"timezone,omitempty"`
	CatchUp        model.MissedRunPolicy `toml:"catch_up,omitempty"`
	MaxCatchUpRuns int                   `toml:"max_catch_up_runs,omitempty"`

	Restart       model.RestartPolicy `toml:"restart,omitempty"`
	MaxConcurrent int                 `toml:"max_concurrent,omitempty"`
	QueueMax      int                 `toml:"queue_max,omitempty"`

	// Instances is rejected on [tasks.*]; carried as a pointer so the validator
	// can distinguish "unset" from "explicitly zero".
	Instances *int `toml:"instances,omitempty"`

	RetryAttempts int    `toml:"retry_attempts,omitempty"`
	RetryDelay    string `toml:"retry_delay,omitempty"`
	RetryBackoff  string `toml:"retry_backoff,omitempty"`
}

func (w *taskWire) toTask(name string) (model.Task, error) {
	task, err := w.toTaskCore(name, "task", model.KindTask)
	if err != nil {
		return model.Task{}, err
	}
	retryDelay, err := parseDuration(w.RetryDelay)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid retry_delay for task %q: %w", name, err)
	}
	task.Cron = w.Cron
	task.Timezone = w.Timezone
	task.CatchUp = w.CatchUp
	task.MaxCatchUpRuns = w.MaxCatchUpRuns
	task.Restart = w.Restart
	task.MaxConcurrent = w.MaxConcurrent
	task.QueueMax = w.QueueMax
	task.RetryAttempts = w.RetryAttempts
	task.RetryDelay = retryDelay
	task.RetryBackoff = w.RetryBackoff
	return task, nil
}

// serviceWire is the over-the-wire shape for [services.*] entries. Cron and
// catch_up are intentionally omitted — services are not cron-driven. Services
// have no max_concurrent or queue_max: instance count is governed by `instances`
// and overlap behaviour by `on_overlap`.
type serviceWire struct {
	taskServiceWireCore

	Instances int `toml:"instances,omitempty"`

	RestartDelay      string `toml:"restart_delay,omitempty"`
	RestartBackoff    string `toml:"restart_backoff,omitempty"`
	BackoffResetAfter string `toml:"backoff_reset_after,omitempty"`
}

func (w *serviceWire) toTask(name string) (model.Task, error) {
	task, err := w.toTaskCore(name, "service", model.KindService)
	if err != nil {
		return model.Task{}, err
	}
	restartDelay, err := parseDuration(w.RestartDelay)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid restart_delay for task %q: %w", name, err)
	}
	backoffReset, err := parseDuration(w.BackoffResetAfter)
	if err != nil {
		return model.Task{}, fmt.Errorf("invalid backoff_reset_after for task %q: %w", name, err)
	}
	task.Restart = model.RestartAlways
	task.Instances = w.Instances
	task.RestartDelay = restartDelay
	task.RestartBackoff = w.RestartBackoff
	task.BackoffResetAfter = backoffReset
	return task, nil
}

// defaultsWire mirrors [defaults] before parsing.
type defaultsWire struct {
	Timeout           string `toml:"timeout,omitempty"`
	LogMaxSize        string `toml:"log_max_size,omitempty"`
	LogOnFull         string `toml:"log_on_full,omitempty"`
	KeepRuns          int    `toml:"keep_runs,omitempty"`
	KeepFor           string `toml:"keep_for,omitempty"`
	BackoffResetAfter string `toml:"backoff_reset_after,omitempty"`

	Env         map[string]string `toml:"env,omitempty"`
	EnvFile     string            `toml:"env_file,omitempty"`
	Secrets     map[string]string `toml:"secrets,omitempty"`
	SecretsFile string            `toml:"secrets_file,omitempty"`
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
		Secrets:           w.Secrets,
		SecretsFile:       w.SecretsFile,
	}, nil
}

// storageWire mirrors [storage] before parsing.
type storageWire struct {
	MaxSize      string `toml:"max_size,omitempty"`
	MinFreeSpace string `toml:"min_free_space,omitempty"`
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

// daemonWire mirrors [daemon] before parsing — the duration string for
// shutdown_timeout is parsed at config-load time.
type daemonWire struct {
	AllowCloudDispatch bool   `toml:"allow_cloud_dispatch,omitempty"`
	ShutdownTimeout    string `toml:"shutdown_timeout,omitempty"`
	ExternalURL        string `toml:"external_url,omitempty"`
	MetricsEnabled     bool   `toml:"metrics_enabled,omitempty"`
	MetricsListen      string `toml:"metrics_listen,omitempty"`
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
	}, nil
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

// notifierWire is one [[notifier]] block. Secret-bearing values arrive final:
// operators use ${VAR} / ${file:...} substitution for indirection.
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

	URL     string            `toml:"url,omitempty"`
	Headers map[string]string `toml:"headers,omitempty"`

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
