// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/composespec"
	"github.com/runwisp/runwisp/internal/model"
)

// ComposeAutoDiscoveryFilenames is the ordered fallback list `docker compose`
// itself uses. Auto-discovery picks the first one that exists next to the
// runwisp.toml file. Exposed so the first-run scaffold can offer the same
// detection without duplicating the list.
var ComposeAutoDiscoveryFilenames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// composeDefaultsKey is the reserved [compose.<alias>.override.<key>] entry that
// applies its override surface to every imported service (per-service entries
// win). A compose service actually named "defaults" can't get an individual
// override through this key (see composeBlockWire.Override in wire.go).
const composeDefaultsKey = "defaults"

// Compose block import strategy — the [compose.*] `import` key. This is a
// config-layer axis distinct from a task's execution ComposeMode: "services"
// emits one RunWisp service per compose service (each executed as ComposeModeRun),
// "stack" emits a single task managing the whole project (ComposeModeStack).
const (
	composeImportServices = "services"
	composeImportStack    = "stack"
)

var validComposeImport = []string{composeImportServices, composeImportStack}
var validComposePull = []string{
	model.ComposePullMissing,
	model.ComposePullAlways,
	model.ComposePullNever,
	"", // unset == default (missing) — handled at the backend
}

// namedComposePullValues lists validComposePull's entries worth naming in an
// error message, dropping the "" (unset-means-default) sentinel.
var namedComposePullValues = func() []string {
	named := make([]string, 0, len(validComposePull))
	for _, v := range validComposePull {
		if v != "" {
			named = append(named, v)
		}
	}
	return named
}()

// expandComposeBlocks consumes cfg.pendingComposeBlocks, enumerates each
// referenced compose file via composespec.Load, and appends a model.Task
// per imported service (services mode) or per project (stack mode). The
// generated tasks flow through resolveEnvLayers, ApplyDefaults and Validate
// like any hand-written [services.*] or [tasks.*] entry.
func expandComposeBlocks(cfg *Config, dirs entrySources) error {
	blocks := cfg.pendingComposeBlocks
	cfg.pendingComposeBlocks = nil
	if len(blocks) == 0 {
		return nil
	}

	aliases := make([]string, 0, len(blocks))
	for alias := range blocks {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	existingNames := make(map[string]struct{}, len(cfg.Tasks))
	for i := range cfg.Tasks {
		existingNames[cfg.Tasks[i].Name] = struct{}{}
	}

	var notify []composeNotifySugar
	for _, alias := range aliases {
		if err := model.ValidateTaskName(alias); err != nil {
			return fmt.Errorf("invalid compose alias %q: %w", alias, err)
		}
		newTasks, newNotify, err := expandComposeAlias(alias, blocks[alias], dirs.dir(alias), existingNames)
		if err != nil {
			return fmt.Errorf("compose.%s: %w", alias, err)
		}
		for i := range newTasks {
			existingNames[newTasks[i].Name] = struct{}{}
		}
		cfg.Tasks = append(cfg.Tasks, newTasks...)
		notify = append(notify, newNotify...)
	}

	// Compose blocks expand after toNotifyConfig has already built cfg.Notify,
	// so their per-service notify_on_* sugar desugars into synthetic routes on
	// the finished config rather than through desugar{Task,Service}Notify.
	return appendComposeNotify(&cfg.Notify, notify)
}

// composeNotifySugar carries one imported service's `notify` selections, keyed
// by the generated task name, from expansion to the notify-route desugaring step.
type composeNotifySugar struct {
	taskName string
	notify   []string
}

// appendComposeNotify turns collected compose per-service notify sugar into
// synthetic routes on an already-built NotifyConfig, then resolves any inline
// "<id>:<override>" tokens the new routes introduced. This mirrors what
// desugarServiceNotify + expandInlineTokens do for [services.*], but runs late
// because compose expansion happens after toNotifyConfig.
func appendComposeNotify(out *NotifyConfig, sugar []composeNotifySugar) error {
	if len(sugar) == 0 {
		return nil
	}
	from := len(out.Routes)
	for _, s := range sugar {
		appendNotifyRoute(out, s.taskName, s.notify)
	}
	return expandInlineTokensFrom(out, from)
}

// composeBlock is the expanded form of one decoded [compose.<alias>] block:
// composeBlockWire's scalars, plus its Override map split into per-service
// overrides and the block-level defaults.
type composeBlock struct {
	composeBlockWire
	Alias string

	// Overrides keyed by compose-service name (post-`services` filtering).
	// Derived from composeBlockWire.Override, minus the "defaults" entry.
	Overrides map[string]*composeServiceOverrideWire

	// Defaults is the [compose.<alias>.override.defaults] entry applied to
	// every imported service before its per-service override (nil when
	// absent).
	Defaults *composeServiceOverrideWire
}

func expandComposeAlias(alias string, wire *composeBlockWire, baseDir string, existingNames map[string]struct{}) ([]model.Task, []composeNotifySugar, error) {
	block, err := parseComposeBlock(alias, wire)
	if err != nil {
		return nil, nil, err
	}

	resolvedFile, err := resolveComposeFile(block.File, baseDir)
	if err != nil {
		return nil, nil, err
	}
	block.File = resolvedFile

	if err := resolveComposeBlockPaths(block, baseDir, resolvedFile); err != nil {
		return nil, nil, err
	}

	project, err := composespec.Load(resolvedFile, block.Profiles, block.EnvFile, block.WorkingDir)
	if err != nil {
		return nil, nil, err
	}

	switch block.Import {
	case composeImportStack:
		tasks, err := expandComposeStack(block, project, existingNames)
		return tasks, nil, err
	default:
		return expandComposeServices(block, project, existingNames)
	}
}

// resolveComposeBlockPaths absolutizes the block's working_dir (defaulting to
// the compose file's directory) and env-file paths in place. Both are passed
// to a CLI whose cwd is working_dir, not baseDir, so they must be absolute.
func resolveComposeBlockPaths(block *composeBlock, baseDir, resolvedFile string) error {
	if block.WorkingDir == "" {
		block.WorkingDir = filepath.Dir(resolvedFile)
	} else {
		if !filepath.IsAbs(block.WorkingDir) {
			block.WorkingDir = filepath.Join(baseDir, block.WorkingDir)
		}
		abs, err := filepath.Abs(block.WorkingDir)
		if err != nil {
			return err
		}
		block.WorkingDir = abs
	}

	for i, p := range block.EnvFile {
		if !filepath.IsAbs(p) {
			abs, err := filepath.Abs(filepath.Join(baseDir, p))
			if err != nil {
				return err
			}
			block.EnvFile[i] = abs
		}
	}
	return nil
}

// parseComposeBlock splits an already-decoded [compose.<alias>] block's
// Override map into per-service overrides and the block-level defaults
// (the reserved "defaults" entry), then defaults and validates the result.
func parseComposeBlock(alias string, wire *composeBlockWire) (*composeBlock, error) {
	overrides := make(map[string]*composeServiceOverrideWire, len(wire.Override))
	var defaults *composeServiceOverrideWire
	for name, override := range wire.Override {
		if name == composeDefaultsKey {
			defaults = override
			continue
		}
		overrides[name] = override
	}

	block := &composeBlock{
		composeBlockWire: *wire,
		Alias:            alias,
		Overrides:        overrides,
		Defaults:         defaults,
	}
	applyComposeBlockDefaults(block, alias)
	if err := validateComposeBlock(block); err != nil {
		return nil, err
	}
	return block, nil
}

// applyComposeBlockDefaults fills the unset scalar fields of a parsed block
// with their compose-import defaults (mode, pull, name_format, group, and
// project_name all default off the alias).
func applyComposeBlockDefaults(block *composeBlock, alias string) {
	if block.Import == "" {
		block.Import = composeImportServices
	}
	if block.Pull == "" {
		block.Pull = model.ComposePullMissing
	}
	if block.NameFormat == "" {
		block.NameFormat = "{alias}.{service}"
	}
	if block.Group == "" {
		block.Group = alias
	}
	if block.ProjectName == "" {
		block.ProjectName = alias
	}
}

// validateComposeBlock rejects out-of-range fields on a defaulted block, and
// enforces the stack-mode restrictions (no overrides, no `services` filtering).
func validateComposeBlock(block *composeBlock) error {
	if !slices.Contains(validComposeImport, block.Import) {
		return fmt.Errorf("invalid import %q: must be one of %s", block.Import, strings.Join(validComposeImport, ", "))
	}
	if !slices.Contains(validComposePull, block.Pull) {
		return fmt.Errorf("invalid pull %q: must be one of %s", block.Pull, strings.Join(namedComposePullValues, ", "))
	}
	if !strings.Contains(block.NameFormat, "{service}") && block.Import == composeImportServices {
		return fmt.Errorf("name_format %q must contain {service}", block.NameFormat)
	}
	if block.Import == composeImportStack {
		if len(block.Overrides) > 0 || block.Defaults != nil {
			return fmt.Errorf("per-service overrides are not allowed in import=\"stack\"")
		}
		if len(block.Services) > 0 {
			return fmt.Errorf("`services` filtering is not allowed in import=\"stack\"")
		}
	}
	return nil
}

func expandComposeServices(block *composeBlock, project *composespec.Project, existingNames map[string]struct{}) ([]model.Task, []composeNotifySugar, error) {
	available := project.ServiceNames()
	availableSet := make(map[string]struct{}, len(available))
	for _, n := range available {
		availableSet[n] = struct{}{}
	}

	imported, err := selectImportedComposeServices(block, available, availableSet)
	if err != nil {
		return nil, nil, err
	}
	importedSet := make(map[string]struct{}, len(imported))
	for _, n := range imported {
		importedSet[n] = struct{}{}
	}

	if err := validateComposeOverridesExist(block.Overrides, importedSet, availableSet); err != nil {
		return nil, nil, err
	}

	tasks := make([]model.Task, 0, len(imported))
	var notify []composeNotifySugar
	for _, svcName := range imported {
		svc := project.Service(svcName)
		taskName := applyNameFormat(block.NameFormat, block.Alias, svcName)
		if err := model.ValidateTaskName(taskName); err != nil {
			return nil, nil, fmt.Errorf("name_format %q produced invalid task name %q: %w", block.NameFormat, taskName, err)
		}
		if _, dup := existingNames[taskName]; dup {
			return nil, nil, fmt.Errorf("name %q (from compose service %q) collides with an existing task or service", taskName, svcName)
		}

		task, err := buildComposeServiceTask(block, svc, svcName, taskName)
		if err != nil {
			return nil, nil, err
		}
		tasks = append(tasks, task)
		if s, ok := composeServiceNotify(block.Defaults, taskName); ok {
			notify = append(notify, s)
		}
		if s, ok := composeServiceNotify(block.Overrides[svcName], taskName); ok {
			notify = append(notify, s)
		}
		existingNames[taskName] = struct{}{}
	}
	return tasks, notify, nil
}

// composeServiceNotify extracts a service override's `notify` selection into
// notify sugar keyed by the generated task name. Reports ok=false when the
// override is absent or declares no notifiers, so the caller adds no route.
func composeServiceNotify(w *composeServiceOverrideWire, taskName string) (composeNotifySugar, bool) {
	if w == nil || len(w.Notify) == 0 {
		return composeNotifySugar{}, false
	}
	return composeNotifySugar{
		taskName: taskName,
		notify:   w.Notify,
	}, true
}

// selectImportedComposeServices validates the `services` filter names against
// the available services, and returns the post-filter set of services to
// import.
func selectImportedComposeServices(block *composeBlock, available []string, availableSet map[string]struct{}) ([]string, error) {
	include, exclude, err := parseComposeServices(block.Services)
	if err != nil {
		return nil, err
	}
	if err := validateComposeNameSet(include, availableSet, "services"); err != nil {
		return nil, err
	}
	if err := validateComposeNameSet(exclude, availableSet, "services"); err != nil {
		return nil, err
	}
	return selectComposeServices(available, include, exclude), nil
}

// parseComposeServices splits a [compose.<alias>] `services` list into allowlist
// (bare or "+"-prefixed) and denylist ("-"-prefixed) name sets. The two
// polarities are mutually exclusive: a single block either names the services to
// keep or the services to drop, never both.
func parseComposeServices(services []string) (include, exclude []string, err error) {
	for _, raw := range services {
		name := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(name, "-"):
			name = strings.TrimSpace(name[1:])
			if name == "" {
				return nil, nil, fmt.Errorf("services: %q has no service name after \"-\"", raw)
			}
			exclude = append(exclude, name)
		case strings.HasPrefix(name, "+"):
			name = strings.TrimSpace(name[1:])
			if name == "" {
				return nil, nil, fmt.Errorf("services: %q has no service name after \"+\"", raw)
			}
			include = append(include, name)
		case name == "":
			return nil, nil, fmt.Errorf("services: empty entry")
		default:
			include = append(include, name)
		}
	}
	if len(include) > 0 && len(exclude) > 0 {
		return nil, nil, fmt.Errorf("services: cannot mix kept (+) and dropped (-) entries in one block")
	}
	return include, exclude, nil
}

// validateComposeOverridesExist ensures every per-service override targets a
// service that was actually imported, distinguishing "no such service" from
// "filtered out by `services`".
func validateComposeOverridesExist(overrides map[string]*composeServiceOverrideWire, importedSet, availableSet map[string]struct{}) error {
	for name := range overrides {
		if _, ok := importedSet[name]; !ok {
			if _, exists := availableSet[name]; !exists {
				return fmt.Errorf("override for service %q does not exist in compose file", name)
			}
			return fmt.Errorf("override for service %q does not match any imported service (filtered out by `services`)", name)
		}
	}
	return nil
}

func expandComposeStack(block *composeBlock, _ *composespec.Project, existingNames map[string]struct{}) ([]model.Task, error) {
	taskName := block.Alias
	if err := model.ValidateTaskName(taskName); err != nil {
		return nil, err
	}
	if _, dup := existingNames[taskName]; dup {
		return nil, fmt.Errorf("name %q (compose stack) collides with an existing task or service", taskName)
	}
	task := model.Task{
		Name:          taskName,
		Kind:          model.KindService,
		Group:         block.Group,
		ManualTrigger: true,
		Autostart:     true,
		Restart:       model.RestartOnFailure,
		Instances:     1,
		ExecutionDef: &model.ComposeExecution{
			File:        block.File,
			ProjectName: block.ProjectName,
			Mode:        model.ComposeModeStack,
			Profiles:    block.Profiles,
			EnvFile:     block.EnvFile,
			WorkingDir:  block.WorkingDir,
			WithDeps:    block.WithDeps,
			Pull:        block.Pull,
		},
		Compose: &model.TaskComposeRef{
			File:        block.File,
			ProjectName: block.ProjectName,
		},
	}
	return []model.Task{task}, nil
}

// buildComposeServiceTask applies compose-import defaults and per-service
// overrides to produce a single supervisable service task. Compose-import
// defaults: kind=service, restart=on_failure, instances=1, group=alias,
// graceful_stop=compose stop_grace_period (when set). Precedence, low to high:
// compose-import default → the block's [compose.<alias>.override.defaults] →
// the per-service [compose.<alias>.override.<svc>] override.
func buildComposeServiceTask(block *composeBlock, svc *composespec.Service, svcName, taskName string) (model.Task, error) {
	task := model.Task{
		Name:          taskName,
		Kind:          model.KindService,
		Group:         block.Group,
		ManualTrigger: true,
		Autostart:     true,
		Restart:       model.RestartOnFailure,
		Instances:     1,
	}
	if svc != nil && svc.StopGracePeriod > 0 {
		g := svc.StopGracePeriod
		task.GracefulStop = &g
	}
	if err := applyComposeOverride(&task, block.Defaults, svcName); err != nil {
		return model.Task{}, err
	}
	if err := applyComposeOverride(&task, block.Overrides[svcName], svcName); err != nil {
		return model.Task{}, err
	}
	task.ExecutionDef = &model.ComposeExecution{
		File:        block.File,
		ProjectName: block.ProjectName,
		Service:     svcName,
		Mode:        model.ComposeModeRun,
		Profiles:    block.Profiles,
		EnvFile:     block.EnvFile,
		WorkingDir:  block.WorkingDir,
		WithDeps:    block.WithDeps,
		Pull:        block.Pull,
	}
	task.Compose = &model.TaskComposeRef{
		File:        block.File,
		Service:     svcName,
		ProjectName: block.ProjectName,
	}
	return task, nil
}

// applyComposeOverride merges a per-service override into the task in place.
// Empty/zero override fields leave the compose-import default intact.
func applyComposeOverride(task *model.Task, w *composeServiceOverrideWire, svcName string) error {
	if w == nil {
		return nil
	}
	if w.OnOverlap != "" {
		return fmt.Errorf("service %q override sets on_overlap; on_overlap is only valid on [tasks.*] — a service never runs a second overlapping instance, instances controls parallelism", svcName)
	}
	applyComposeOverrideSupervision(task, w)
	applyComposeOverrideEnv(task, w)
	return applyComposeOverrideParsed(task, w, svcName)
}

// applyComposeOverrideSupervision copies the override's identity and
// supervision-policy fields (group, description, restart, instances, retries,
// priority, autostart, log-on-full, keep_runs) onto the task, leaving unset
// values at their compose-import default.
func applyComposeOverrideSupervision(task *model.Task, w *composeServiceOverrideWire) {
	if w.Group != "" {
		task.Group = w.Group
	}
	if w.Description != "" {
		task.Description = w.Description
	}
	if w.Restart != "" {
		task.Restart = w.Restart
	}
	if w.Instances > 0 {
		task.Instances = w.Instances
	}
	if w.StopSignal != "" {
		task.StopSignal = w.StopSignal
	}
	if w.RestartAttempts != nil {
		task.RestartAttempts = w.RestartAttempts
	}
	if w.Priority != 0 {
		task.Priority = w.Priority
	}
	if w.Autostart != nil {
		task.Autostart = *w.Autostart
	}
	if w.ManualTrigger != nil {
		task.ManualTrigger = *w.ManualTrigger
	}
	if w.LogOnFull != "" {
		task.LogOnFull = w.LogOnFull
	}
	if w.RestartBackoff != "" {
		task.RestartBackoff = w.RestartBackoff
	}
	if w.KeepRuns != nil {
		task.KeepRuns = w.KeepRuns
	}
}

// applyComposeOverrideEnv copies the override's env / secrets fields onto the
// task, leaving unset values at their compose-import default. The notify_on_*
// lists are handled separately (see composeNotifySugar): they never land on the
// Task — like [services.*] they desugar into synthetic notify routes keyed by
// the generated task name.
func applyComposeOverrideEnv(task *model.Task, w *composeServiceOverrideWire) {
	if len(w.Env) > 0 {
		task.Env = w.Env
	}
	if w.EnvFile != "" {
		task.EnvFile = w.EnvFile
	}
	if len(w.Secrets) > 0 {
		task.Secrets = w.Secrets
	}
	if w.SecretsFile != "" {
		task.SecretsFile = w.SecretsFile
	}
}

// applyComposeOverrideParsed parses the override's duration / byte-size string
// fields with the same parsers as the regular service path (so error messages
// match) and writes them onto the task. Empty strings are left untouched.
func applyComposeOverrideParsed(task *model.Task, w *composeServiceOverrideWire, svcName string) error {
	if err := parseOverrideDuration(w.Timeout, svcName, "timeout", &task.Timeout); err != nil {
		return err
	}
	if err := parseOverrideDurationPtr(w.GracefulStop, svcName, "graceful_stop", &task.GracefulStop); err != nil {
		return err
	}
	if err := parseOverrideDurationPtr(w.RestartDelay, svcName, "restart_delay", &task.RestartDelay); err != nil {
		return err
	}
	if err := parseOverrideDurationPtr(w.HealthyAfter, svcName, "healthy_after", &task.HealthyAfter); err != nil {
		return err
	}
	if w.KeepFor != "" {
		d, err := parseKeepFor(w.KeepFor)
		if err != nil {
			return fmt.Errorf("service %q override: invalid keep_for: %w", svcName, err)
		}
		task.KeepFor = d
	}
	if w.LogMaxSize != "" {
		n, err := parseLogMaxSize(w.LogMaxSize)
		if err != nil {
			return fmt.Errorf("service %q override: invalid log_max_size: %w", svcName, err)
		}
		task.LogMaxSize = n
	}
	if w.Failures != nil {
		spec, err := model.ParseFailures(w.Failures)
		if err != nil {
			return fmt.Errorf("service %q override: invalid failures: %w", svcName, err)
		}
		task.FailureSpec = spec
	}
	return nil
}

// parseOverrideDuration parses one duration-valued override field into dst,
// leaving dst untouched when raw is empty. field names the key in the error so
// the message matches the regular service path.
func parseOverrideDuration(raw, svcName, field string, dst *time.Duration) error {
	if raw == "" {
		return nil
	}
	d, err := parseDuration(raw)
	if err != nil {
		return fmt.Errorf("service %q override: invalid %s: %w", svcName, field, err)
	}
	*dst = d
	return nil
}

// parseOverrideDurationPtr parses one duration-valued override field for a
// pointer-typed task field (RestartDelay, HealthyAfter), leaving dst
// untouched when raw is empty. Unlike parseOverrideDuration, an explicit
// "0s" override is preserved literally rather than colliding with the
// "not overridden" zero value — a fresh pointer is written on any non-empty
// raw, including one that parses to zero.
func parseOverrideDurationPtr(raw, svcName, field string, dst **time.Duration) error {
	if raw == "" {
		return nil
	}
	d, err := parseDuration(raw)
	if err != nil {
		return fmt.Errorf("service %q override: invalid %s: %w", svcName, field, err)
	}
	*dst = &d
	return nil
}

func applyNameFormat(format, alias, service string) string {
	out := strings.ReplaceAll(format, "{alias}", alias)
	out = strings.ReplaceAll(out, "{service}", service)
	return out
}

func selectComposeServices(available, include, exclude []string) []string {
	if len(include) > 0 {
		out := make([]string, 0, len(include))
		for _, n := range available {
			if slices.Contains(include, n) {
				out = append(out, n)
			}
		}
		return out
	}
	if len(exclude) == 0 {
		return available
	}
	skip := make(map[string]struct{}, len(exclude))
	for _, n := range exclude {
		skip[n] = struct{}{}
	}
	out := make([]string, 0, len(available))
	for _, n := range available {
		if _, drop := skip[n]; drop {
			continue
		}
		out = append(out, n)
	}
	return out
}

func validateComposeNameSet(names []string, available map[string]struct{}, scope string) error {
	for _, n := range names {
		if _, ok := available[n]; !ok {
			return fmt.Errorf("%s names service %q which is not in the compose file", scope, n)
		}
	}
	return nil
}

// resolveComposeFile turns a declared (or auto-discovered) compose path into an
// absolute path. Absolute is essential: the file path becomes `docker compose
// -f <file>` while the invocation cwd is set to working_dir, so a relative path
// would be resolved twice (once against baseDir at load, again against
// working_dir at exec) and miss the file.
func resolveComposeFile(declared, baseDir string) (string, error) {
	if declared != "" {
		path, err := resolvePath(baseDir, declared)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("compose file %s: %w", path, err)
		}
		return filepath.Abs(path)
	}
	for _, name := range ComposeAutoDiscoveryFilenames {
		candidate := filepath.Join(baseDir, name)
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Abs(candidate)
		}
	}
	return "", fmt.Errorf("no compose file found in %s (searched: %s)",
		baseDir, strings.Join(ComposeAutoDiscoveryFilenames, ", "))
}
