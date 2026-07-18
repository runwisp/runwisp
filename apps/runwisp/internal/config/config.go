// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/model"
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
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	cfg.watchFiles = collectWatchFiles(cfg, dirs)
	return cfg, nil
}

// collectWatchFiles resolves every on-disk input Snapshot should watch beyond
// the root config: included TOML files plus each env_file, each against the dir
// of the config that declared it. secrets_file is intentionally excluded,
// matching the pre-include behavior.
func collectWatchFiles(cfg *Config, dirs sourceDirs) []string {
	files := append([]string(nil), cfg.includeFiles...)
	if cfg.Defaults.EnvFile != "" {
		files = append(files, resolveAgainst(dirs.root, cfg.Defaults.EnvFile))
	}
	for i := range cfg.Tasks {
		if ef := cfg.Tasks[i].EnvFile; ef != "" {
			files = append(files, resolveAgainst(dirs.dir(cfg.Tasks[i].Name), ef))
		}
	}
	return files
}

// sourceDirs maps each task/service/compose-alias name to the directory of the
// config file that defined it, so an included file's relative paths (env_file,
// secrets_file, compose_file, working_dir) resolve against its own location
// rather than the root config. Names with no recorded origin — compose-
// generated tasks, or any task in a single-file config — fall back to root.
type sourceDirs struct {
	root   string
	byName map[string]string
}

// dir returns the base directory for the named entry's relative paths.
func (s sourceDirs) dir(name string) string {
	if d, ok := s.byName[name]; ok {
		return d
	}
	return s.root
}

// resolveEnvLayers reads each env_file / secrets_file referenced by the config
// and merges the file's KEY=VALUE pairs beneath the corresponding inline map —
// inline entries override file entries, docker-compose-style. Relative paths
// are resolved against baseDir (the runwisp.toml directory). Dotenv file
// contents are taken literally; ${...} substitution applies only to TOML.
func resolveEnvLayers(cfg *Config, dirs sourceDirs) error {
	var err error
	// [defaults] is root-only, so its env_file/secrets_file always resolve
	// against the root config dir.
	if cfg.Defaults.Env, err = mergeEnvFileLayer(dirs.root, cfg.Defaults.EnvFile, cfg.Defaults.Env, "defaults"); err != nil {
		return err
	}
	if cfg.Defaults.Secrets, err = mergeEnvFileLayer(dirs.root, cfg.Defaults.SecretsFile, cfg.Defaults.Secrets, "defaults"); err != nil {
		return err
	}
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		baseDir := dirs.dir(task.Name)
		scope := fmt.Sprintf("task %q", task.Name)
		if task.Env, err = mergeEnvFileLayer(baseDir, task.EnvFile, task.Env, scope); err != nil {
			return err
		}
		if task.Secrets, err = mergeEnvFileLayer(baseDir, task.SecretsFile, task.Secrets, scope); err != nil {
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
func resolveComposePaths(cfg *Config, dirs sourceDirs) error {
	for i := range cfg.Tasks {
		ce, ok := cfg.Tasks[i].ExecutionDef.(*model.ComposeExecution)
		if !ok || ce.File == "" || filepath.IsAbs(ce.File) {
			continue
		}
		resolved, err := resolveComposeFile(ce.File, dirs.dir(cfg.Tasks[i].Name))
		if err != nil {
			return fmt.Errorf("task %q: %w", cfg.Tasks[i].Name, err)
		}
		ce.File = resolved
		if ce.WorkingDir == "" {
			ce.WorkingDir = filepath.Dir(resolved)
		}
	}
	return nil
}

// resolveWorkingDirs absolutizes the working_dir set on each task/service.
// Relative paths resolve against baseDir (the runwisp.toml directory), matching
// env_file / compose_file. For compose-backed tasks an explicit working_dir
// overrides the compose file's directory default chosen in resolveComposePaths.
// Existence is checked at run time, not load — like shell, host paths are
// resolved against the daemon's namespace, which may differ from the one
// `runwisp validate` runs in.
func resolveWorkingDirs(cfg *Config, dirs sourceDirs) error {
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		if task.WorkingDir == "" {
			continue
		}
		resolved, err := resolvePath(dirs.dir(task.Name), task.WorkingDir)
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

// Warnings reports non-fatal findings an operator should see after a
// successful config load. Both daemon boot and `runwisp validate` print from
// here, so future advisory checks land in one place and stay in sync.
func Warnings(cfg *Config) []string {
	return gracefulStopWarnings(cfg)
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
		if task.GracefulStop > limit {
			warnings = append(warnings, fmt.Sprintf(
				"task %q has graceful_stop=%s but [daemon] shutdown_timeout=%s; the daemon will SIGKILL this task before its grace window completes during shutdown",
				task.Name, task.GracefulStop, limit,
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

// decode parses TOML bytes into a Config. baseDir is the runwisp.toml
// directory; ${file:...} substitutions resolve relative paths against it.
func decode(data []byte, baseDir string) (*Config, error) {
	raw, err := parseWire(data, baseDir)
	if err != nil {
		return nil, err
	}
	return buildConfig(raw)
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
		Scheduler:            Scheduler{Timezone: raw.Scheduler.Timezone},
		pendingComposeBlocks: raw.Compose,
	}, nil
}

func collectTaskNames(raw *tomlConfig) ([]string, error) {
	names := make([]string, 0, len(raw.Tasks))
	for name, w := range raw.Tasks {
		if err := model.ValidateTaskName(name); err != nil {
			return nil, err
		}
		if w.Restart == model.RestartAlways {
			return nil, fmt.Errorf("task %q sets restart=\"always\"; use [services.%s] instead", name, name)
		}
		if w.Instances != nil {
			return nil, fmt.Errorf("task %q sets instances; instances is only valid on [services.*]", name)
		}
		if len(w.DependsOn) > 0 {
			return nil, fmt.Errorf("task %q sets depends_on; depends_on is only valid on [services.*]", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
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
	sort.Strings(names)
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

// validateDefaults checks the [defaults] section: enum membership, ranges, and
// caps for the values inherited by tasks that omit them.
func validateDefaults(d *Defaults) error {
	if err := requireOneOf("defaults.log_on_full", d.LogOnFull, validLogOnFull, true); err != nil {
		return err
	}
	if err := validateKeepRuns("defaults.keep_runs", d.KeepRuns); err != nil {
		return err
	}
	if err := validateKeepFor("defaults.keep_for", d.KeepFor); err != nil {
		return err
	}
	if d.HealthyAfter < 0 {
		return fmt.Errorf("invalid defaults.healthy_after: must be a positive duration")
	}
	if d.StartRetries < 0 {
		return fmt.Errorf("invalid defaults.start_retries: must be non-negative")
	}
	if d.StartRetries > StartRetriesCap {
		return fmt.Errorf("invalid defaults.start_retries: %d exceeds the cap of %d", d.StartRetries, StartRetriesCap)
	}
	if d.Jitter < 0 {
		return fmt.Errorf("invalid defaults.jitter: must be zero or a positive duration")
	}
	if d.Jitter > JitterCap {
		return fmt.Errorf("invalid defaults.jitter: %s exceeds the cap of %s", d.Jitter, JitterCap)
	}
	if err := validateExitCodes("defaults.exit_codes", d.ExitCodes); err != nil {
		return err
	}
	return validateStopSignal("defaults.stop_signal", d.StopSignal)
}

// validateTLS checks the [daemon] TLS keys: the mode must be a known literal,
// and tls_cert/tls_key are all-or-nothing — supplying one without the other is
// a config error, not a silent half-configuration. When both are set the files
// must exist and load as a usable key pair, so a typo'd path fails at boot
// rather than when the first HTTPS request arrives.
func validateTLS(d *Daemon) error {
	// "" is the not-yet-defaulted state (ApplyDefaults fills it with "auto");
	// accept it so Validate is order-independent with respect to ApplyDefaults.
	switch d.TLS {
	case "", TLSModeAuto, TLSModeOff:
	default:
		return fmt.Errorf("invalid daemon.tls: %q (must be \"auto\" or \"off\")", d.TLS)
	}
	switch {
	case d.TLSCert == "" && d.TLSKey == "":
		return nil
	case d.TLSCert == "" || d.TLSKey == "":
		return fmt.Errorf("invalid [daemon]: tls_cert and tls_key must be set together")
	}
	if _, err := tls.LoadX509KeyPair(d.TLSCert, d.TLSKey); err != nil {
		return fmt.Errorf("invalid [daemon] tls_cert/tls_key: %w", err)
	}
	return nil
}

// Validate checks for invalid configuration values. Durations and byte sizes
// have already been parsed at this point — only enum membership, ranges, and
// required fields remain.
// Validate collects every configuration problem it can rather than returning at
// the first, so `runwisp validate` reports them all in one pass (one entry per
// offending task, plus each top-level check). Independent checks each contribute
// at most one error; the result collapses to nil / a single error / a
// *MultiError via joinConfigErrors.
func Validate(cfg *Config) error {
	var errs []error
	if err := validateDefaults(&cfg.Defaults); err != nil {
		errs = append(errs, err)
	}
	if cfg.Daemon.ShutdownTimeout < 0 {
		errs = append(errs, fmt.Errorf("invalid daemon.shutdown_timeout: must be a positive duration"))
	}
	if err := validateTLS(&cfg.Daemon); err != nil {
		errs = append(errs, err)
	}
	if _, err := ResolveTimezone("scheduler.timezone", cfg.Scheduler.Timezone); err != nil {
		errs = append(errs, err)
	}

	seen := make(map[string]struct{}, len(cfg.Tasks))
	for i := range cfg.Tasks {
		if err := validateTask(&cfg.Tasks[i], seen); err != nil {
			errs = append(errs, err)
		}
	}
	if err := validateServiceDependencies(cfg); err != nil {
		errs = append(errs, err)
	}
	if err := validateNotify(&cfg.Notify); err != nil {
		errs = append(errs, err)
	}
	return joinConfigErrors(errs)
}

// validateServiceDependencies checks every service's depends_on graph: each ref
// must name a known service (not a task, not itself), and the graph must be
// acyclic. It runs after the per-task loop because it needs the whole task set.
// depends_on is boot ordering only, so the only structural failure is a cycle —
// which would otherwise deadlock the gated launcher.
func validateServiceDependencies(cfg *Config) error {
	byName := make(map[string]*model.Task, len(cfg.Tasks))
	for i := range cfg.Tasks {
		byName[cfg.Tasks[i].Name] = &cfg.Tasks[i]
	}
	for i := range cfg.Tasks {
		t := &cfg.Tasks[i]
		if !t.Kind.IsService() {
			continue
		}
		for _, dep := range t.DependsOn {
			if dep == t.Name {
				return fmt.Errorf("service %q depends_on itself", t.Name)
			}
			target, ok := byName[dep]
			if !ok {
				return fmt.Errorf("service %q depends_on unknown name %q", t.Name, dep)
			}
			if !target.Kind.IsService() {
				return fmt.Errorf("service %q depends_on %q, which is a task; depends_on may only reference services", t.Name, dep)
			}
		}
	}
	return detectDependencyCycle(byName)
}

// detectDependencyCycle reports the first dependency cycle among services via
// DFS, naming the path (e.g. "a -> b -> a") so the operator can see which edges
// to cut. Only service nodes participate; non-services have no depends_on.
func detectDependencyCycle(byName map[string]*model.Task) error {
	walk := &depCycleWalk{byName: byName, state: make(map[string]int, len(byName))}

	// Sort the roots so the reported cycle is deterministic regardless of map
	// iteration order.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if walk.state[name] == depStateUnseen {
			if err := walk.visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

const (
	depStateUnseen   = 0
	depStateVisiting = 1
	depStateDone     = 2
)

// depCycleWalk carries the DFS state (per-node colour + the active path stack)
// for detectDependencyCycle so the recursion is a plain method rather than a
// closure capturing mutable locals.
type depCycleWalk struct {
	byName map[string]*model.Task
	state  map[string]int
	stack  []string
}

// visit performs one DFS step, returning a cycle error naming the path if it
// reaches a node already on the active stack.
func (w *depCycleWalk) visit(name string) error {
	w.state[name] = depStateVisiting
	w.stack = append(w.stack, name)
	t, ok := w.byName[name]
	if ok && t.Kind.IsService() {
		for _, dep := range t.DependsOn {
			switch w.state[dep] {
			case depStateVisiting:
				return fmt.Errorf("service dependency cycle: %s -> %s", strings.Join(cycleFrom(w.stack, dep), " -> "), dep)
			case depStateDone:
				continue
			default:
				if err := w.visit(dep); err != nil {
					return err
				}
			}
		}
	}
	w.stack = w.stack[:len(w.stack)-1]
	w.state[name] = depStateDone
	return nil
}

// cycleFrom returns the suffix of the DFS stack starting at the node the back
// edge points to, so the rendered path begins where the cycle closes.
func cycleFrom(stack []string, start string) []string {
	for i, name := range stack {
		if name == start {
			return stack[i:]
		}
	}
	return stack
}

func validateTask(task *model.Task, seen map[string]struct{}) error {
	if err := validateTaskIdentity(task, seen); err != nil {
		return err
	}
	if err := validateTaskCommand(task); err != nil {
		return err
	}
	if err := validateTaskShell(task); err != nil {
		return err
	}
	if err := validateTaskStopSignal(task); err != nil {
		return err
	}
	if err := validateTaskRunUser(task); err != nil {
		return err
	}
	if err := validateTaskLimits(task); err != nil {
		return err
	}
	if err := validateTaskEnums(task); err != nil {
		return err
	}
	if err := validateTaskEnv(task); err != nil {
		return err
	}
	if err := validateTaskParams(task); err != nil {
		return err
	}
	if task.Kind.IsService() {
		if err := validateServiceTask(task); err != nil {
			return err
		}
	}
	if err := validateTaskRetention(task); err != nil {
		return err
	}
	return validateTaskCron(task)
}

// validateTaskCron rejects cron expressions the scheduler would refuse at
// boot, so `runwisp validate` and daemon startup fail identically. Runs after
// validateTaskRetention so an invalid per-task timezone surfaces as the
// friendlier timezone error rather than a CRON_TZ parse failure. An empty
// cron is a trigger-only task and is allowed.
func validateTaskCron(task *model.Task) error {
	if task.Cron == "" {
		return nil
	}
	if err := cronspec.Validate(task.Cron, task.Timezone); err != nil {
		return fmt.Errorf(
			"invalid cron for task %q: %q — %v; expected 5 fields \"min hour day month weekday\" (e.g. \"0 3 * * *\" = 03:00 daily), an optional leading seconds field for 6 fields \"sec min hour day month weekday\" (e.g. \"*/30 * * * * *\" = every 30s on the :00 and :30), a descriptor like @hourly/@daily/@weekly, or @every for fixed intervals (e.g. @every 30s, @every 1h30m)",
			task.Name, task.Cron, err)
	}
	return nil
}

// envKeyPattern is the POSIX-ish shape required for environment variable
// names: letters, digits, and underscores; not starting with a digit.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateTaskEnv enforces shape and size limits on env and secrets. Both maps
// are validated with the same rules and counted against one combined cap so
// the merged process env can safely be turned into KEY=VALUE strings without
// producing malformed entries.
func validateTaskEnv(task *model.Task) error {
	scope := fmt.Sprintf("env for task %s", task.Name)
	if err := validateEnvMap(scope, task.Env); err != nil {
		return err
	}
	secretScope := fmt.Sprintf("secrets for task %s", task.Name)
	if err := validateEnvMap(secretScope, task.Secrets); err != nil {
		return err
	}
	if total := len(task.Env) + len(task.Secrets); total > EnvMaxEntries {
		return fmt.Errorf("invalid env for task %s: %d entries exceeds the cap of %d", task.Name, total, EnvMaxEntries)
	}
	return nil
}

// validateEnvMap is reusable across inline env and env_file values. Scope is a
// human-readable label embedded in error messages (e.g. "env for task foo" or
// "env_file /etc/runwisp/secrets.env").
func validateEnvMap(scope string, env map[string]string) error {
	if len(env) > EnvMaxEntries {
		return fmt.Errorf("invalid %s: %d entries exceeds the cap of %d", scope, len(env), EnvMaxEntries)
	}
	for key, value := range env {
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid %s: key %q must match %s", scope, key, envKeyPattern.String())
		}
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid %s: value for %q contains a NUL byte", scope, key)
		}
		if len(value) > EnvMaxValueLen {
			return fmt.Errorf("invalid %s: value for %q is %d bytes; cap is %d", scope, key, len(value), EnvMaxValueLen)
		}
	}
	return nil
}

// validateTaskParams enforces the [tasks.*.params] rules: identity shape,
// modifier compatibility, default coercion, intra-task uniqueness, env/secret
// collisions, positional ordering, and the auto-scheduled required-without-
// default rule. It runs after validateTaskEnv so task.Env/task.Secrets are
// fully merged (inline + env_file + defaults) when the collision check reads
// them. Kind/key derivation and the single-identity rule are already enforced
// at wire-mapping time (toTaskParams).
func validateTaskParams(task *model.Task) error {
	if len(task.Parameters) == 0 {
		return nil
	}
	autoScheduled := task.Cron != "" || task.RunOnStart
	seen := make(map[string]struct{}, len(task.Parameters))
	optionalPositionalSeen := false
	for i := range task.Parameters {
		p := &task.Parameters[i]
		scope := fmt.Sprintf("params[%d] (%s) for task %s", i, p.Key, task.Name)
		if _, dup := seen[p.Key]; dup {
			return fmt.Errorf("invalid %s: duplicate parameter key %q", scope, p.Key)
		}
		seen[p.Key] = struct{}{}

		if err := validateParamEntry(scope, p, task, autoScheduled, optionalPositionalSeen); err != nil {
			return err
		}
		if p.Kind == model.ParamArg && !p.Required {
			optionalPositionalSeen = true
		}
	}
	return nil
}

// validateParamEntry runs every per-parameter check for one declaration.
// optionalPositionalSeen reports whether an earlier optional positional arg has
// already appeared, which would make a later required positional ambiguous.
func validateParamEntry(scope string, p *model.TaskParam, task *model.Task, autoScheduled, optionalPositionalSeen bool) error {
	if err := validateParamIdentity(scope, p); err != nil {
		return err
	}
	if err := validateParamModifiers(scope, p); err != nil {
		return err
	}
	if err := validateParamDefault(scope, p); err != nil {
		return err
	}
	if err := validateParamEnvCollision(scope, p, task); err != nil {
		return err
	}
	if p.Kind == model.ParamArg && p.Required && optionalPositionalSeen {
		return fmt.Errorf("invalid %s: a required positional arg cannot follow an optional one (omitting the optional one would shift this value)", scope)
	}
	if p.Required && p.Default == nil && autoScheduled {
		return fmt.Errorf("invalid %s: required parameters need a default on cron / run_on_start tasks — a scheduled firing has no operator to supply a value", scope)
	}
	return nil
}

// validateParamIdentity checks the canonical key shape per kind: env/arg names
// must be valid identifiers; option/flag tokens must start with a dash.
func validateParamIdentity(scope string, p *model.TaskParam) error {
	switch p.Kind {
	case model.ParamEnv, model.ParamArg:
		if !envKeyPattern.MatchString(p.Key) {
			return fmt.Errorf("invalid %s: %s name %q must match %s", scope, p.Kind, p.Key, envKeyPattern.String())
		}
	case model.ParamOption, model.ParamFlag:
		if !strings.HasPrefix(p.Key, "-") {
			return fmt.Errorf("invalid %s: %s %q must start with '-' (e.g. --name)", scope, p.Kind, p.Key)
		}
		if strings.ContainsAny(p.Key, " \t\n\x00") {
			return fmt.Errorf("invalid %s: %s %q must not contain whitespace or NUL", scope, p.Kind, p.Key)
		}
	}
	return nil
}

// validateParamModifiers checks type / choices / allow_custom compatibility with
// the kind.
func validateParamModifiers(scope string, p *model.TaskParam) error {
	if p.Type != "" && p.Type != model.ParamTypeString && p.Type != model.ParamTypeNumber {
		return fmt.Errorf("invalid %s: type %q must be %q or %q", scope, p.Type, model.ParamTypeString, model.ParamTypeNumber)
	}
	if p.Kind == model.ParamFlag {
		return validateFlagModifiers(scope, p)
	}
	if len(p.Choices) == 0 && p.AllowCustom {
		return fmt.Errorf("invalid %s: allow_custom is only meaningful with choices", scope)
	}
	return validateChoiceValues(scope, p)
}

// validateFlagModifiers rejects modifiers that have no meaning on a flag — its
// value is always boolean, so type/choices/allow_custom/required don't apply.
func validateFlagModifiers(scope string, p *model.TaskParam) error {
	if p.Type != "" {
		return fmt.Errorf("invalid %s: type is not valid on a flag (boolean is implied)", scope)
	}
	if len(p.Choices) > 0 {
		return fmt.Errorf("invalid %s: choices is not valid on a flag", scope)
	}
	if p.AllowCustom {
		return fmt.Errorf("invalid %s: allow_custom is not valid on a flag", scope)
	}
	if p.Required {
		return fmt.Errorf("invalid %s: required is not valid on a flag (it always resolves true/false; set a default to start it on)", scope)
	}
	return nil
}

// validateChoiceValues checks each declared choice is well-formed: never a NUL
// byte, and parseable as a number when the param's type is number (so
// resolve-time enum membership implies the value is numeric).
func validateChoiceValues(scope string, p *model.TaskParam) error {
	for _, c := range p.Choices {
		if strings.ContainsRune(c, 0) {
			return fmt.Errorf("invalid %s: choice %q contains a NUL byte", scope, c)
		}
		if p.Type == model.ParamTypeNumber {
			if _, err := strconv.ParseFloat(c, 64); err != nil {
				return fmt.Errorf("invalid %s: choice %q is not a number but type is %q", scope, c, model.ParamTypeNumber)
			}
		}
	}
	return nil
}

// validateParamDefault checks that a declared default satisfies the kind/type:
// flag → boolean, enum → member (unless allow_custom), number → parses; and the
// NUL/length guards shared with env values.
func validateParamDefault(scope string, p *model.TaskParam) error {
	if p.Default == nil {
		return nil
	}
	def := *p.Default
	if strings.ContainsRune(def, 0) {
		return fmt.Errorf("invalid %s: default contains a NUL byte", scope)
	}
	if len(def) > EnvMaxValueLen {
		return fmt.Errorf("invalid %s: default is %d bytes; cap is %d", scope, len(def), EnvMaxValueLen)
	}
	switch {
	case p.Kind == model.ParamFlag:
		if _, err := strconv.ParseBool(def); err != nil {
			return fmt.Errorf("invalid %s: flag default %q must be a boolean", scope, def)
		}
	case len(p.Choices) > 0 && !p.AllowCustom:
		if slices.Contains(p.Choices, def) {
			return nil
		}
		return fmt.Errorf("invalid %s: default %q is not one of %s", scope, def, strings.Join(p.Choices, ", "))
	case p.Type == model.ParamTypeNumber:
		if _, err := strconv.ParseFloat(def, 64); err != nil {
			return fmt.Errorf("invalid %s: number default %q must parse as a number", scope, def)
		}
	}
	return nil
}

// validateParamEnvCollision rejects an env-kind parameter whose name also
// appears in the task's env or secrets — two mechanisms writing one variable is
// exactly the silent behaviour the product forbids.
func validateParamEnvCollision(scope string, p *model.TaskParam, task *model.Task) error {
	if p.Kind != model.ParamEnv {
		return nil
	}
	if _, ok := task.Env[p.Key]; ok {
		return fmt.Errorf("invalid %s: env parameter %q is also defined in env", scope, p.Key)
	}
	if _, ok := task.Secrets[p.Key]; ok {
		return fmt.Errorf("invalid %s: env parameter %q is also defined in secrets", scope, p.Key)
	}
	return nil
}

func validateTaskIdentity(task *model.Task, seen map[string]struct{}) error {
	if err := model.ValidateTaskName(task.Name); err != nil {
		return err
	}
	if _, exists := seen[task.Name]; exists {
		return fmt.Errorf("duplicate task name: %s", task.Name)
	}
	seen[task.Name] = struct{}{}
	return nil
}

func validateTaskCommand(task *model.Task) error {
	// `run` and a compose backend are mutually exclusive — both set is
	// ambiguous, so fail fast rather than pick a precedence rule.
	if _, isCompose := task.ExecutionDef.(*model.ComposeExecution); isCompose && strings.TrimSpace(task.Run) != "" {
		return fmt.Errorf("task %s sets both `run` and `compose_file`; pick one execution backend", task.Name)
	}
	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return fmt.Errorf("task run command is required for task: %s", task.Name)
	}
	if shellDef, ok := execDef.(*model.ShellExecution); ok && strings.TrimSpace(shellDef.Script) == "" {
		return fmt.Errorf("task run command is required for task: %s", task.Name)
	}
	return nil
}

// validateTaskShell requires the resolved shell to be an absolute path. A
// relative name would be resolved against the daemon's PATH at run time, which
// is non-deterministic. Like working_dir, host paths and interpreters are
// resolved at run time, not load, so `validate` stays namespace-independent —
// the shell is never stat-ed here because the daemon may run in a different
// mount namespace than `runwisp validate`.
func validateTaskShell(task *model.Task) error {
	if task.Shell == "" {
		// Post-defaults this is always /bin/sh; the executor also falls back to
		// /bin/sh on empty, so an unset shell is valid.
		return nil
	}
	if strings.ContainsRune(task.Shell, 0) {
		return fmt.Errorf("invalid shell for task %s: contains a NUL byte", task.Name)
	}
	if !filepath.IsAbs(task.Shell) {
		return fmt.Errorf("invalid shell for task %s: %q must be an absolute path (e.g. /bin/bash)", task.Name, task.Shell)
	}
	return nil
}

// validateTaskStopSignal rejects a stop_signal outside the curated allowlist.
// An empty value is accepted — the executor falls back to SIGTERM, matching the
// post-defaults resolution. Accepts both "TERM" and "SIGTERM" spellings.
func validateTaskStopSignal(task *model.Task) error {
	return validateStopSignal(fmt.Sprintf("stop_signal for task %s", task.Name), task.StopSignal)
}

// validateStopSignal is the shared check for per-task and defaults.stop_signal.
func validateStopSignal(scope, signal string) error {
	if signal == "" {
		return nil
	}
	if _, ok := model.NormalizeSignalName(signal); !ok {
		return fmt.Errorf("invalid %s: %q (must be one of %s)", scope, signal, strings.Join(model.StopSignals, ", "))
	}
	return nil
}

// validateTaskRunUser checks the shape of the run-as `user` spec. Only the
// `user` / `user:group` form is validated here — resolving the name to a uid/gid
// is deferred to run time (the account may not exist when the config is loaded,
// and reload is restart-only).
func validateTaskRunUser(task *model.Task) error {
	if _, _, err := model.ParseRunUserSpec(task.RunUser); err != nil {
		return fmt.Errorf("invalid user for task %s: %w", task.Name, err)
	}
	return nil
}

func validateTaskLimits(task *model.Task) error {
	if task.MaxConcurrent < 0 {
		return fmt.Errorf("invalid max_concurrent for task %s: must be a positive integer", task.Name)
	}
	if task.MaxConcurrent > MaxConcurrentCap {
		return fmt.Errorf("invalid max_concurrent for task %s: %d exceeds the cap of %d", task.Name, task.MaxConcurrent, MaxConcurrentCap)
	}
	if task.QueueMax < 0 {
		return fmt.Errorf("invalid queue_max for task %s: must be a positive integer", task.Name)
	}
	if task.QueueMax > QueueMaxCap {
		return fmt.Errorf("invalid queue_max for task %s: %d exceeds the cap of %d", task.Name, task.QueueMax, QueueMaxCap)
	}
	if task.GracefulStop < 0 {
		return fmt.Errorf("invalid graceful_stop for task %s: must be zero or a positive duration", task.Name)
	}
	if task.Jitter < 0 {
		return fmt.Errorf("invalid jitter for task %s: must be zero or a positive duration", task.Name)
	}
	if task.Jitter > JitterCap {
		return fmt.Errorf("invalid jitter for task %s: %s exceeds the cap of %s", task.Name, task.Jitter, JitterCap)
	}
	if task.RetryAttempts < 0 {
		return fmt.Errorf("invalid retry_attempts for task %s: must be non-negative", task.Name)
	}
	if task.RetryAttempts > RetryAttemptsCap {
		return fmt.Errorf("invalid retry_attempts for task %s: %d exceeds the cap of %d", task.Name, task.RetryAttempts, RetryAttemptsCap)
	}
	if task.MaxCatchUpRuns < 0 {
		return fmt.Errorf("invalid max_catch_up_runs for task %s: must be a positive integer", task.Name)
	}
	return validateExitCodes(fmt.Sprintf("exit_codes for task %s", task.Name), task.ExitCodes)
}

// validateExitCodes enforces the shape of the success-exit-code list. A nil
// slice means "unset" (defaults fill it); an explicit empty list is rejected
// because it would make even exit 0 a failure. Codes must be in the POSIX
// range 0-255 and the list is capped.
func validateExitCodes(scope string, codes []int) error {
	if codes == nil {
		return nil
	}
	if len(codes) == 0 {
		return fmt.Errorf("invalid %s: list at least one exit code, or omit the key to default to [0]", scope)
	}
	if len(codes) > ExitCodesCap {
		return fmt.Errorf("invalid %s: %d entries exceeds the cap of %d", scope, len(codes), ExitCodesCap)
	}
	for _, c := range codes {
		if c < 0 || c > 255 {
			return fmt.Errorf("invalid %s: %d is out of range (exit codes are 0-255)", scope, c)
		}
	}
	return nil
}

func validateTaskEnums(task *model.Task) error {
	enums := []struct {
		scope   string
		value   string
		allowed []string
		emptyOK bool
	}{
		{"on_overlap for task " + task.Name, string(task.OnOverlap), validOnOverlap, false},
		{"restart for task " + task.Name, string(task.Restart), validRestart, true},
		{"retry_backoff for task " + task.Name, task.RetryBackoff, validRetryBackoff, true},
		{"log_on_full for task " + task.Name, task.LogOnFull, validLogOnFull, true},
		{"catch_up for task " + task.Name, string(task.CatchUp), validCatchUp, true},
	}
	for _, e := range enums {
		if err := requireOneOf(e.scope, e.value, e.allowed, e.emptyOK); err != nil {
			return err
		}
	}
	return nil
}

func validateTaskRetention(task *model.Task) error {
	if err := validateKeepRuns(fmt.Sprintf("keep_runs for task %s", task.Name), task.KeepRuns); err != nil {
		return err
	}
	if err := validateKeepFor(fmt.Sprintf("keep_for for task %s", task.Name), task.KeepFor); err != nil {
		return err
	}
	if _, err := ResolveTimezone(fmt.Sprintf("timezone for task %s", task.Name), task.Timezone); err != nil {
		return err
	}
	return nil
}

func validateServiceTask(task *model.Task) error {
	if task.Instances < 1 {
		return fmt.Errorf("invalid instances for service %s: must be >= 1", task.Name)
	}
	if task.Instances > MaxServiceInstances {
		return fmt.Errorf("invalid instances for service %s: must be <= %d", task.Name, MaxServiceInstances)
	}
	if err := requireOneOf(fmt.Sprintf("restart_backoff for service %s", task.Name),
		task.RestartBackoff, validRestartBackoff, true); err != nil {
		return err
	}
	if task.HealthyAfter < 0 {
		return fmt.Errorf("invalid healthy_after for service %s: must be a positive duration", task.Name)
	}
	if task.StartRetries < 0 {
		return fmt.Errorf("invalid start_retries for service %s: must be non-negative", task.Name)
	}
	if task.StartRetries > StartRetriesCap {
		return fmt.Errorf("invalid start_retries for service %s: %d exceeds the cap of %d", task.Name, task.StartRetries, StartRetriesCap)
	}
	return nil
}

// ResolveTimezone returns the *time.Location for the given IANA name. An empty
// string means "fall back to the scheduler default" — callers handle that.
// Use this for both per-task timezone and the global scheduler timezone so
// the same error shape is produced everywhere.
func ResolveTimezone(scope, name string) (*time.Location, error) {
	if name == "" {
		return nil, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %q is not a valid IANA timezone (e.g. \"UTC\", \"America/New_York\"): %w", scope, name, err)
	}
	return loc, nil
}

// validateKeepRuns rejects negative values and values above the keep_runs cap.
// Zero is the post-parse sentinel for "omitted, inherit defaults"; any
// positive integer up to KeepRunsCap is accepted.
func validateKeepRuns(scope string, n int) error {
	if n < 0 {
		return fmt.Errorf("invalid %s: must be a positive integer", scope)
	}
	if n > KeepRunsCap {
		return fmt.Errorf("invalid %s: %d exceeds the cap of %d", scope, n, KeepRunsCap)
	}
	return nil
}

// validateKeepFor rejects negative durations and durations above the keep_for
// cap. Zero is the post-parse sentinel for "omitted, inherit defaults"; any
// positive duration up to KeepForCap is accepted.
func validateKeepFor(scope string, d time.Duration) error {
	if d < 0 {
		return fmt.Errorf("invalid %s: must be a positive duration", scope)
	}
	if d > KeepForCap {
		return fmt.Errorf("invalid %s: %s exceeds the cap of %s", scope, d, KeepForCap)
	}
	return nil
}

// requireOneOf returns nil if value is in the allowed set. When emptyOK is
// true, an empty value is accepted (used for optional enums that fall through
// to a default).
func requireOneOf(scope, value string, allowed []string, emptyOK bool) error {
	if value == "" {
		if emptyOK {
			return nil
		}
		return fmt.Errorf("invalid %s: required, must be one of %s", scope, strings.Join(allowed, ", "))
	}
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return fmt.Errorf("invalid %s: %q (must be one of %s)", scope, value, strings.Join(allowed, ", "))
}

var (
	validOnOverlap = []string{
		string(model.PolicyQueue),
		string(model.PolicySkip),
		string(model.PolicyTerminate),
	}
	validRestart = []string{
		string(model.RestartNever),
		string(model.RestartAlways),
		string(model.RestartOnFailure),
	}
	validRetryBackoff     = []string{model.BackoffConstant, model.BackoffLinear, model.BackoffExponential}
	validRestartBackoff   = []string{model.BackoffConstant, model.BackoffLinear, model.BackoffExponential}
	validLogOnFull        = []string{model.LogOverflowDropNew, model.LogOverflowDropOld, model.LogOverflowKillTask}
	validCatchUp          = []string{string(model.MissedRunLatest), string(model.MissedRunAll), string(model.MissedRunSkip)}
	defaultTaskLogMaxSize = int64(100 * 1024 * 1024)
	defaultRestartDelay   = time.Second
)

// Hard caps for integer config fields. Above-cap values fail config load with
// a numeric error message — there's no silent clamping. The caps are
// deliberately generous: any operator who needs more is almost certainly
// configuring something pathological and should rethink, not raise the cap.
const (
	MaxConcurrentCap = 1024
	QueueMaxCap      = 10000
	KeepRunsCap      = 1_000_000
	RetryAttemptsCap = 100
	// ExitCodesCap bounds the success-exit-code list. POSIX exit codes are
	// 0-255, so a list longer than this is almost certainly a mistake.
	ExitCodesCap = 256
	// StartRetriesCap bounds start_retries. A service that fast-fails this many
	// times in a row is broken; allowing more just delays the FATAL signal.
	StartRetriesCap = 100
	// JitterCap bounds the jitter window at a full day. The runtime clamps each
	// fire to the gap before the next tick, so this is pure typo protection
	// (e.g. "30h" meant "30m") rather than a correctness limit.
	JitterCap = 24 * time.Hour
	// KeepForCap bounds keep_for at ~100 years. Like JitterCap this is pure typo
	// protection (e.g. "999999d" meant "999999s") — it never constrains a real
	// operator and stays well under time.Duration's ~292-year ceiling.
	KeepForCap = 100 * 365 * 24 * time.Hour

	// EnvMaxEntries caps the combined size of a task's inline env and
	// env_file-derived secret env. Generous enough for any realistic dotenv
	// while keeping a malformed config from blowing up the daemon's memory.
	EnvMaxEntries = 256
	// EnvMaxValueLen re-exports the shared per-value cap that lives on the model
	// package (env values and supplied param values share one limit so they
	// never drift). See model.EnvMaxValueLen for the rationale.
	EnvMaxValueLen = model.EnvMaxValueLen
)

// TLS modes for [daemon] tls. TLSModeAuto serves HTTP on loopback and
// self-signed HTTPS on a non-loopback bind; TLSModeOff is plain HTTP on every
// bind (operator terminates TLS upstream or trusts the network).
const (
	TLSModeAuto = "auto"
	TLSModeOff  = "off"
)

// Built-in defaults applied by ApplyDefaults when a field is omitted entirely.
const (
	DefaultMaxCatchUpRuns = 100
	DefaultQueueMax       = 100
	DefaultGracefulStop   = 5 * time.Second
	DefaultDaemonShutdown = 10 * time.Second
	DefaultHealthyAfter   = 60 * time.Second
	// DefaultStartRetries is the number of consecutive fast failures a service
	// instance may accrue before the supervisor marks it FATAL and stops
	// restarting it. Applied when neither the service nor [defaults] sets
	// start_retries.
	DefaultStartRetries = 3
	// DefaultShell is the interpreter used for `run` scripts when neither the
	// task nor [defaults] selects one. The invocation is always
	// `<shell> -c <script>`.
	DefaultShell = "/bin/sh"
	// DefaultStopSignal is the first signal of the stop ladder when neither the
	// task nor [defaults] selects one. The daemon always follows with SIGKILL
	// after graceful_stop.
	DefaultStopSignal = "SIGTERM"
)

// ApplyDefaults fills in zero-valued fields with sensible defaults. The
// scheduler timezone, in particular, falls back to the host's system zone
// when the operator left [scheduler] timezone unset — so a fresh install
// just works without an explicit choice, while the resolved zone is still
// surfaced in the TUI banner and Web UI header.
func ApplyDefaults(cfg *Config) {
	if cfg.Scheduler.Timezone == "" {
		cfg.Scheduler.Timezone = ResolveSystemTimezone()
		cfg.Scheduler.Source = TimezoneSourceSystem
	} else {
		cfg.Scheduler.Source = TimezoneSourceConfig
	}

	if cfg.Daemon.ShutdownTimeout == 0 {
		cfg.Daemon.ShutdownTimeout = DefaultDaemonShutdown
	}
	if cfg.Daemon.TLS == "" {
		cfg.Daemon.TLS = TLSModeAuto
	}
	if cfg.Defaults.HealthyAfter == 0 {
		cfg.Defaults.HealthyAfter = DefaultHealthyAfter
	}

	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]

		if task.Kind.IsService() {
			applyServiceDefaults(task, cfg.Defaults)
		} else {
			applyTaskDefaults(task)
		}
		if task.CatchUp == "" {
			task.CatchUp = model.MissedRunLatest
		}
		if task.MaxCatchUpRuns == 0 {
			task.MaxCatchUpRuns = DefaultMaxCatchUpRuns
		}
		if task.GracefulStop == 0 {
			task.GracefulStop = DefaultGracefulStop
		}

		applyInheritedDefaults(task, cfg.Defaults)
	}
}

// applyInheritedDefaults copies defaults-section values into task fields that
// were not explicitly set in TOML, then fills in absolute built-in fallbacks.
func applyInheritedDefaults(task *model.Task, d Defaults) {
	if task.Timeout == 0 {
		task.Timeout = d.Timeout
	}
	// Jitter is task-only: a service never inherits [defaults] jitter (it starts
	// every instance at boot, so there's no fire time to spread). An explicit
	// [services.x] jitter is rejected earlier by DisallowUnknownFields.
	if !task.Kind.IsService() && task.Jitter == 0 {
		task.Jitter = d.Jitter
	}
	if task.Shell == "" {
		task.Shell = d.Shell
	}
	if task.Shell == "" {
		task.Shell = DefaultShell
	}
	applyInheritedStopSignal(task, d)
	applyInheritedExitCodes(task, d)
	if task.LogMaxSize == 0 {
		task.LogMaxSize = d.LogMaxSize
	}
	if task.LogOnFull == "" && d.LogOnFull != "" {
		task.LogOnFull = d.LogOnFull
	}
	if task.KeepRuns == 0 && d.KeepRuns != 0 {
		task.KeepRuns = d.KeepRuns
	}
	if task.KeepFor == 0 && d.KeepFor != 0 {
		task.KeepFor = d.KeepFor
	}
	if task.LogMaxSize == 0 {
		task.LogMaxSize = defaultTaskLogMaxSize
	}
	if task.LogOnFull == "" {
		task.LogOnFull = model.LogOverflowDropOld
	}
	applyInheritedNotifyOnMissed(task, d)
	task.Env = mergeEnv(d.Env, task.Env)
	task.Secrets = mergeEnv(d.Secrets, task.Secrets)
}

// applyInheritedStopSignal inherits stop_signal from defaults, then falls back
// to SIGTERM, then canonicalizes to "SIGxxx" form. An unrecognised value
// survives unchanged so Validate can reject it with a clear error.
func applyInheritedStopSignal(task *model.Task, d Defaults) {
	if task.StopSignal == "" {
		task.StopSignal = d.StopSignal
	}
	if task.StopSignal == "" {
		task.StopSignal = DefaultStopSignal
	}
	if canonical, ok := model.NormalizeSignalName(task.StopSignal); ok {
		task.StopSignal = canonical
	}
}

// applyInheritedExitCodes treats nil exit_codes as "unset" (inherit, then
// default to [0]); an explicit empty list survives so Validate can reject it.
func applyInheritedExitCodes(task *model.Task, d Defaults) {
	if task.ExitCodes == nil {
		task.ExitCodes = d.ExitCodes
	}
	if task.ExitCodes == nil {
		task.ExitCodes = []int{0}
	}
}

// applyInheritedNotifyOnMissed resolves an unset per-task notify_on_missed by
// inheriting [defaults], then falling back to the built-in true. The result is
// a concrete pointer so downstream readers never see nil regardless of how the
// task was built.
func applyInheritedNotifyOnMissed(task *model.Task, d Defaults) {
	if task.NotifyOnMissed != nil {
		return
	}
	resolved := true
	if d.NotifyOnMissed != nil {
		resolved = *d.NotifyOnMissed
	}
	task.NotifyOnMissed = &resolved
}

// mergeEnv returns a map containing every key in base then in overlay, with
// overlay winning on collision. Returns nil when both inputs are empty so the
// executor can keep its "no env overlay → inherit parent" fast path. The
// inputs are never mutated.
func mergeEnv(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func applyTaskDefaults(task *model.Task) {
	if task.Group == "" {
		task.Group = "Tasks"
	}
	if task.MaxConcurrent == 0 {
		task.MaxConcurrent = 1
	}
	if task.OnOverlap == "" {
		task.OnOverlap = model.PolicyQueue
	}
	if task.QueueMax == 0 {
		task.QueueMax = DefaultQueueMax
	}
}

func applyServiceDefaults(task *model.Task, d Defaults) {
	if task.Group == "" {
		task.Group = "Services"
	}
	if task.OnOverlap == "" {
		task.OnOverlap = model.PolicySkip
	}
	if task.MaxConcurrent == 0 {
		task.MaxConcurrent = 1
	}
	if task.Instances == 0 {
		task.Instances = 1
	}
	if task.RestartDelay == 0 {
		task.RestartDelay = defaultRestartDelay
	}
	if task.RestartBackoff == "" {
		task.RestartBackoff = model.BackoffExponential
	}
	if task.HealthyAfter == 0 {
		task.HealthyAfter = d.HealthyAfter
	}
	// start_retries: explicit on the service wins; else [defaults]; else the
	// built-in default.
	if task.StartRetries == 0 {
		task.StartRetries = d.StartRetries
	}
	if task.StartRetries == 0 {
		task.StartRetries = DefaultStartRetries
	}
}
