// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// Load reads, decodes, defaults, and validates a runwisp.toml file, merging in
// any files pulled via [daemon].include.
func Load(path string) (*Config, error) {
	cfg, dirs, err := loadWithIncludes(path)
	if err != nil {
		return nil, err
	}
	if err := expandComposeBlocks(cfg, dirs); err != nil {
		return nil, err
	}
	if err := resolveComposePaths(cfg, dirs); err != nil {
		return nil, err
	}
	if err := resolveEnvLayers(cfg, dirs); err != nil {
		return nil, err
	}
	if err := resolveWorkingDirs(cfg, dirs); err != nil {
		return nil, err
	}
	ApplyDefaults(cfg)
	if err := applyTLSEnvOverride(cfg); err != nil {
		return nil, err
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	cfg.watchFiles = collectWatchFiles(cfg, dirs)
	return cfg, nil
}

// applyTLSEnvOverride lets RUNWISP_TLS override [daemon] tls. It runs on every
// Load — including the reload path in internal/runtime/reconcile.go — so the
// override is applied identically on boot and on every subsequent reload;
// applying it only once at boot would make checkNonReloadable see a changed
// [daemon] section on every reload and reject it. An unset or empty
// RUNWISP_TLS leaves the TOML value untouched.
func applyTLSEnvOverride(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv("RUNWISP_TLS"))
	if raw == "" {
		return nil
	}
	mode, err := parseTLSMode(raw)
	if err != nil {
		return fmt.Errorf("RUNWISP_TLS: %w", err)
	}
	if mode == "" {
		mode = TLSModeAuto
	}
	cfg.Daemon.TLS = mode
	return nil
}

// ApplyTrustedProxiesEnv lets RUNWISP_TRUSTED_PROXIES (comma-separated)
// override [daemon] trusted_proxies. Only the daemon calls it, at boot and on
// every reload, so a reload sees no trusted_proxies change while the env var
// pins the list. It stays out of Load so a stray value in the operator's shell
// can't fail `validate`, `list`, `import` and the other commands that never
// serve HTTP.
func ApplyTrustedProxiesEnv(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv("RUNWISP_TRUSTED_PROXIES"))
	if raw == "" {
		return nil
	}
	proxies, err := parseTrustedProxies(strings.Split(raw, ","))
	if err != nil {
		return fmt.Errorf("RUNWISP_TRUSTED_PROXIES: %w", err)
	}
	cfg.Daemon.TrustedProxies = proxies
	return nil
}

// collectWatchFiles resolves every on-disk input Snapshot should watch beyond
// the root config: included TOML files plus each env_file, each against the dir
// of the config that declared it, plus every crontab read via include_cron, so
// `crontab -e` makes Snapshot.Stale() report "config changed on disk" with no
// machinery of its own. secrets_file is intentionally excluded.
func collectWatchFiles(cfg *Config, dirs entrySources) []string {
	files := append([]string(nil), cfg.includeFiles...)
	files = append(files, cfg.cronFiles...)
	addEnvFile := func(baseDir, path string) {
		// An unresolvable path already failed the load in loadEnvFile.
		if resolved, err := resolvePath(baseDir, path); path != "" && err == nil {
			files = append(files, resolved)
		}
	}
	addEnvFile(dirs.root, cfg.Defaults.EnvFile)
	for owner, unit := range cfg.units() {
		addEnvFile(dirs.dir(owner), unit.EnvFile)
	}
	return files
}

// entrySources maps each task/service/compose-alias name to the absolute path of
// the config file that defined it. It answers two questions with one map: which
// directory an entry's relative paths (env_file, secrets_file, compose_file,
// working_dir) resolve against, and which file an entry came from (what
// Task.Source is derived from and what `promote` needs to know). Names with no
// recorded origin (compose-generated tasks) fall back to the root config dir.
type entrySources struct {
	root   string
	byName map[string]string
}

// dir returns the base directory for the named entry's relative paths.
func (s entrySources) dir(name string) string {
	if f, ok := s.byName[name]; ok {
		return filepath.Dir(f)
	}
	return s.root
}

// resolveEnvLayers reads each env_file / secrets_file referenced by the config
// and merges the file's KEY=VALUE pairs beneath the corresponding inline map:
// inline entries override file entries, docker-compose-style. Relative paths
// resolve against the directory of the config file that declared them. Dotenv
// file contents are taken literally; ${...} substitution applies only to TOML.
func resolveEnvLayers(cfg *Config, dirs entrySources) error {
	var err error
	// [defaults] is root-only, so its env_file/secrets_file always resolve
	// against the root config dir.
	if cfg.Defaults.Env, err = mergeEnvFileLayer(dirs.root, cfg.Defaults.EnvFile, cfg.Defaults.Env, "defaults"); err != nil {
		return err
	}
	if cfg.Defaults.Secrets, err = mergeEnvFileLayer(dirs.root, cfg.Defaults.SecretsFile, cfg.Defaults.Secrets, "defaults"); err != nil {
		return err
	}
	for owner, unit := range cfg.units() {
		baseDir := dirs.dir(owner)
		scope := fmt.Sprintf("task %q", unit.Name)
		if unit.Env, err = mergeEnvFileLayer(baseDir, unit.EnvFile, unit.Env, scope); err != nil {
			return err
		}
		if unit.Secrets, err = mergeEnvFileLayer(baseDir, unit.SecretsFile, unit.Secrets, scope); err != nil {
			return err
		}
	}
	return nil
}

// mergeEnvFileLayer loads a dotenv file (when set) and merges the inline map
// over it. Returns the inline map untouched when no file is configured.
func mergeEnvFileLayer(baseDir, file string, inline map[string]string, scope string) (map[string]string, error) {
	if file == "" {
		return inline, nil
	}
	values, err := loadEnvFile(baseDir, file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", scope, err)
	}
	return mergeEnv(values, inline), nil
}

// resolveComposePaths absolutizes the compose file path on tasks/services that
// set compose_file directly ([services.*] / [tasks.*]); the [compose.*] block
// path already resolves to absolute during expansion, so those are skipped by
// the IsAbs guard. WorkingDir defaults to the file's directory so the CLI runs
// from there, matching docker compose's own behaviour.
func resolveComposePaths(cfg *Config, dirs entrySources) error {
	for owner, unit := range cfg.units() {
		ce, ok := unit.ExecutionDef.(*model.ComposeExecution)
		if !ok || ce.File == "" || filepath.IsAbs(ce.File) {
			continue
		}
		resolved, err := resolveComposeFile(ce.File, dirs.dir(owner))
		if err != nil {
			return fmt.Errorf("task %q: %w", unit.Name, err)
		}
		ce.File = resolved
		if ce.WorkingDir == "" {
			ce.WorkingDir = filepath.Dir(resolved)
		}
	}
	return nil
}

// resolveWorkingDirs absolutizes the working_dir set on each task/service.
// Relative paths resolve against the declaring config file's directory, matching
// env_file / compose_file. For compose-backed tasks an explicit working_dir
// overrides the compose file's directory default chosen in resolveComposePaths.
// Existence is checked at run time, not load: like shell, host paths are
// resolved against the daemon's namespace, which may differ from the one
// `runwisp validate` runs in.
//
// A `~` on a task that also sets `user` is the one path left unresolved here;
// see homeIsTheRunUsers.
func resolveWorkingDirs(cfg *Config, dirs entrySources) error {
	for owner, task := range cfg.units() {
		if task.WorkingDir == "" || homeIsTheRunUsers(task) {
			continue
		}
		resolved, err := resolvePath(dirs.dir(owner), task.WorkingDir)
		if err != nil {
			return fmt.Errorf("working_dir for task %q: %w", task.Name, err)
		}
		task.WorkingDir = resolved
		if ce, ok := task.ExecutionDef.(*model.ComposeExecution); ok {
			ce.WorkingDir = resolved
		}
	}
	return nil
}

// homeIsTheRunUsers reports whether a task's working_dir is a `~` that has to
// stay literal until the executor knows whose home it means.
//
// `~` on a task with no `user` is the daemon's own home and resolves here, which
// is both correct and what `runwisp validate` can check. `~` on a task that
// drops to another user means *that* user's home — cron's rule, and the reason a
// system crontab is importable at all — and the daemon can't resolve it at load
// for the same reason resolveRunAs doesn't: the account may not exist on the
// machine running `runwisp validate`, and looking it up here would make a config
// mean different things in different places. The executor resolves it from the
// credential it just looked up.
func homeIsTheRunUsers(task *model.Task) bool {
	if task.RunUser == "" {
		return false
	}
	return task.WorkingDir == "~" || strings.HasPrefix(task.WorkingDir, "~/")
}

// Warnings reports non-fatal findings an operator should see after a
// successful config load. Both daemon boot and `runwisp validate` print from
// here, so future advisory checks land in one place and stay in sync.
func Warnings(cfg *Config) []string {
	w := append(gracefulStopWarnings(cfg), nonPosixShellWarnings(cfg)...)
	w = append(w, composeExecServiceWarnings(cfg)...)
	w = append(w, cfg.composeWarnings...)
	return append(w, cronSourceWarnings(cfg)...)
}

// composeExecServiceWarnings reports long-running services running in compose
// exec mode. Docker offers no way to cancel an exec (the API has ExecCreate,
// ExecStart, ExecAttach and ExecInspect, and nothing that stops one), so when
// RunWisp stops or restarts the unit it can only kill the local `docker compose
// exec` client. The process inside the target container keeps running, and the
// restart then starts a second copy alongside it. For a service that compounds
// on every restart, so it is reported at boot.
func composeExecServiceWarnings(cfg *Config) []string {
	var warnings []string
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		if task.Kind != model.KindService {
			continue
		}
		ce, ok := task.ExecutionDef.(*model.ComposeExecution)
		if !ok || ce.Mode != model.ComposeModeExec {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"service %q uses compose_mode = %q; Docker cannot cancel an exec, so stopping or restarting this service kills only the local client and leaves the process running inside %q — a restart then adds a second copy. Bound it inside the container (e.g. `timeout`), or run it as a fresh container with compose_mode = %q",
			task.Name, model.ComposeModeExec, ce.Service, model.ComposeModeRun,
		))
	}
	return warnings
}

// nonPosixShellWarnings reports units whose `shell` RunWisp cannot arm
// fail-fast on. The executor passes `-e` only to interpreters it recognises as
// POSIX shells, because handing the flag to something else can turn a loud
// failure into a silent success (see model.ShellSupportsErrexit). The operator
// hears about it at boot and from `runwisp validate` rather than via a run that
// passed when it shouldn't.
func nonPosixShellWarnings(cfg *Config) []string {
	var warnings []string
	for _, task := range cfg.units() {
		if task.Shell == "" || model.ShellSupportsErrexit(task.Shell) {
			continue
		}
		kind := task.Kind
		if kind == "" {
			kind = model.KindTask
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s %q uses shell %q, which RunWisp does not recognise as a POSIX shell; fail-fast (-e) is not armed for it, so a command failing partway through `run` will not fail the run",
			kind, task.Name, task.Shell,
		))
	}
	return warnings
}

// gracefulStopWarnings reports tasks whose graceful_stop exceeds the daemon
// shutdown_timeout. The daemon will SIGKILL such tasks before their grace
// window completes during a daemon-wide shutdown — operators usually want
// to either lengthen [daemon] shutdown_timeout or shorten the per-task value.
func gracefulStopWarnings(cfg *Config) []string {
	limit := cfg.Daemon.ShutdownTimeout
	if limit <= 0 {
		return nil
	}
	var warnings []string
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		if task.GracefulStopValue() > limit {
			warnings = append(warnings, fmt.Sprintf(
				"task %q has graceful_stop=%s but [daemon] shutdown_timeout=%s; the daemon will SIGKILL this task before its grace window completes during shutdown",
				task.Name, task.GracefulStopValue(), limit,
			))
		}
	}
	return warnings
}

// parseWire decodes TOML bytes into the wire config and runs ${VAR} /
// ${file:...} substitution against baseDir (the file's own directory). It
// stops short of building the model so loadWithIncludes can decode each file
// against its own dir, then merge before the single build pass.
func parseWire(data []byte, baseDir string) (*tomlConfig, error) {
	var raw tomlConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, formatDecodeError(err)
	}
	if err := expandConfig(&raw, baseDir, os.LookupEnv); err != nil {
		return nil, err
	}
	return &raw, nil
}

// buildConfig turns a (possibly merged) wire config into a Config. It runs
// exactly once over the combined task/service/notifier set, so cross-file
// references (a route targeting an included task, a task naming a notifier from
// another file) resolve correctly.
func buildConfig(raw *tomlConfig) (*Config, error) {
	taskNames, err := collectTaskNames(raw)
	if err != nil {
		return nil, err
	}
	serviceNames, err := collectServiceNames(raw)
	if err != nil {
		return nil, err
	}
	tasks, err := buildTaskSlice(raw, taskNames, serviceNames)
	if err != nil {
		return nil, err
	}

	defaults, err := raw.Defaults.toDefaults()
	if err != nil {
		return nil, err
	}
	storage, err := raw.Storage.toStorage()
	if err != nil {
		return nil, err
	}
	daemon, err := raw.Daemon.toDaemon()
	if err != nil {
		return nil, err
	}

	notifyCfg, err := raw.toNotifyConfig(taskNames, raw.Tasks, serviceNames, raw.Services)
	if err != nil {
		return nil, err
	}

	return &Config{
		Tasks:                tasks,
		Defaults:             defaults,
		Storage:              storage,
		Daemon:               daemon,
		Notify:               notifyCfg,
		Scheduler:            Scheduler{Timezone: raw.Daemon.Timezone},
		pendingComposeBlocks: raw.Compose,
	}, nil
}

// collectTaskNames validates every [tasks.*] name. A task/service key valid
// only on the other kind never reaches here: it fails strict decode first,
// with a pointed message from crossKindKeyHints (see suggest.go).
func collectTaskNames(raw *tomlConfig) ([]string, error) {
	names := make([]string, 0, len(raw.Tasks))
	for name := range raw.Tasks {
		if err := model.ValidateTaskName(name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func collectServiceNames(raw *tomlConfig) ([]string, error) {
	names := make([]string, 0, len(raw.Services))
	for name := range raw.Services {
		if err := model.ValidateTaskName(name); err != nil {
			return nil, err
		}
		if _, dup := raw.Tasks[name]; dup {
			return nil, fmt.Errorf("name %q used by both [tasks.*] and [services.*]", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func buildTaskSlice(raw *tomlConfig, taskNames, serviceNames []string) ([]model.Task, error) {
	tasks := make([]model.Task, 0, len(taskNames)+len(serviceNames))
	for _, name := range taskNames {
		t, err := raw.Tasks[name].toTask(name)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	for _, name := range serviceNames {
		t, err := raw.Services[name].toTask(name)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}
