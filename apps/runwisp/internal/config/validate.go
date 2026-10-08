// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

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
	if d.Timeout < 0 {
		return fmt.Errorf("invalid defaults.timeout: must be zero or a positive duration")
	}
	if d.HealthyAfter != nil && *d.HealthyAfter < 0 {
		return fmt.Errorf("invalid defaults.healthy_after: must be a positive duration")
	}
	if err := validateRestartAttempts("defaults.restart_attempts", d.RestartAttempts); err != nil {
		return err
	}
	if d.Jitter < 0 {
		return fmt.Errorf("invalid defaults.jitter: must be zero or a positive duration")
	}
	if d.Jitter > JitterCap {
		return fmt.Errorf("invalid defaults.jitter: %s exceeds the cap of %s", d.Jitter, JitterCap)
	}
	return validateStopSignal("defaults.stop_signal", d.StopSignal)
}

// validateTLS checks the [daemon] TLS keys: the mode must be a known literal,
// and tls_cert/tls_key are all-or-nothing — supplying one without the other is
// a config error, not a silent half-configuration. When both are set the files
// must exist and load as a usable key pair, so a typo'd path fails at boot
// rather than when the first HTTPS request arrives.
func validateTLS(d *Daemon) error {
	// "" is the not-yet-defaulted state (ApplyDefaults fills it with "off");
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
	// tls = "off" alongside a cert/key pair is a contradiction, not a silent
	// "cert wins". An unset tls with a cert/key pair is fine: ApplyDefaults
	// leaves it "" rather than "off".
	if d.TLS == TLSModeOff {
		return fmt.Errorf("invalid [daemon]: tls = \"off\" cannot be combined with tls_cert/tls_key; remove tls_cert and tls_key, or set tls to \"auto\" or leave it unset")
	}
	if _, err := tls.LoadX509KeyPair(d.TLSCert, d.TLSKey); err != nil {
		return fmt.Errorf("invalid [daemon] tls_cert/tls_key: %w", err)
	}
	return nil
}

// Validate checks for invalid configuration values. Durations and byte sizes
// have already been parsed at this point; only enum membership, ranges, and
// required fields remain.
//
// It collects every problem rather than returning at the first, so `runwisp
// validate` reports them all in one pass (one entry per offending task, plus
// each top-level check), joined via errors.Join.
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
	if _, err := ResolveTimezone("daemon.timezone", cfg.Scheduler.Timezone); err != nil {
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
	return errors.Join(errs...)
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
	for _, name := range slices.Sorted(maps.Keys(byName)) {
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

// unitKind returns "service" or "task" so validation messages emitted from the
// shared validateTask path name the unit correctly for both kinds.
func unitKind(task *model.Task) string {
	if task.Kind.IsService() {
		return "service"
	}
	return "task"
}

func validateTask(task *model.Task, seen map[string]struct{}) error {
	if err := validateTaskIdentity(task, seen); err != nil {
		return err
	}
	return validateUnit(task)
}

// validateUnit runs every check an executable unit gets apart from its
// identity (name shape and uniqueness): a task or service, and a service's
// health_check probe, which has no registered name of its own.
func validateUnit(task *model.Task) error {
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
	if err := validateCatchUpOverlap(task); err != nil {
		return err
	}
	if err := validateTaskEnv(task); err != nil {
		return err
	}
	if err := validateTaskParams(task); err != nil {
		return err
	}
	if err := validateTaskHookTokens(task); err != nil {
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

// minHookTokenLength is long enough that guessing a token over HTTP is
// infeasible; the hooks failure limiter only has to stop scanners.
const minHookTokenLength = 32

// validateTaskHookTokens rejects tokens too short to resist guessing, tokens
// with whitespace (a pasted newline would never match a header), and allow
// lists naming an action the unit doesn't support. manual_trigger does not
// gate hooks, so it isn't consulted here. Token values never appear in errors,
// only their position.
func validateTaskHookTokens(task *model.Task) error {
	supported := model.HookActionsFor(task.Kind)
	for i, h := range task.HookTokens {
		if len(h.Token) < minHookTokenLength {
			return fmt.Errorf("hook_tokens[%d] for %s %q is %d characters; use at least %d (e.g. `openssl rand -hex 32`)",
				i, unitKind(task), task.Name, len(h.Token), minHookTokenLength)
		}
		if strings.IndexFunc(h.Token, unicode.IsSpace) >= 0 {
			return fmt.Errorf("hook_tokens[%d] for %s %q contains whitespace", i, unitKind(task), task.Name)
		}
		if h.Allow != nil && len(h.Allow) == 0 {
			return fmt.Errorf("hook_tokens[%d] for %s %q has an empty allow list; omit allow to grant every action", i, unitKind(task), task.Name)
		}
		for _, a := range h.Allow {
			if !slices.Contains(supported, a) {
				return fmt.Errorf("hook_tokens[%d] for %s %q allows %q; valid actions are %v", i, unitKind(task), task.Name, a, supported)
			}
		}
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
	scope := fmt.Sprintf("env for %s %s", unitKind(task), task.Name)
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
