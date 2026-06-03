// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
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

// composeReservedKeys is the set of [compose.<alias>] scalar/array keys that
// are NOT per-service override sub-tables. A compose file with a service
// named identically to one of these reserved keys is rejected at expansion
// time with a helpful rename hint.
var composeReservedKeys = map[string]struct{}{
	"file":         {},
	"include":      {},
	"exclude":      {},
	"mode":         {},
	"group":        {},
	"project_name": {},
	"profiles":     {},
	"env_file":     {},
	"working_dir":  {},
	"with_deps":    {},
	"pull":         {},
	"name_format":  {},
}

var validComposeMode = []string{model.ComposeModeServices, model.ComposeModeStack}
var validComposePull = []string{
	model.ComposePullMissing,
	model.ComposePullAlways,
	model.ComposePullNever,
	"", // unset == default (missing) — handled at the backend
}

// expandComposeBlocks consumes cfg.pendingComposeBlocks, enumerates each
// referenced compose file via composespec.Load, and appends a model.Task
// per imported service (services mode) or per project (stack mode). The
// generated tasks flow through resolveEnvLayers, ApplyDefaults and Validate
// like any hand-written [services.*] or [tasks.*] entry.
func expandComposeBlocks(cfg *Config, baseDir string) error {
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

	for _, alias := range aliases {
		if err := model.ValidateTaskName(alias); err != nil {
			return fmt.Errorf("invalid compose alias %q: %w", alias, err)
		}
		newTasks, err := expandComposeAlias(alias, blocks[alias], baseDir, existingNames)
		if err != nil {
			return fmt.Errorf("compose.%s: %w", alias, err)
		}
		for i := range newTasks {
			existingNames[newTasks[i].Name] = struct{}{}
		}
		cfg.Tasks = append(cfg.Tasks, newTasks...)
	}
	return nil
}

// composeBlock is the destructured form of one [compose.<alias>] table.
type composeBlock struct {
	Alias       string
	File        string
	Include     []string
	Exclude     []string
	Mode        string
	Group       string
	ProjectName string
	Profiles    []string
	EnvFile     []string
	WorkingDir  string
	WithDeps    bool
	Pull        string
	NameFormat  string

	// Overrides keyed by compose-service name (post-include/post-exclude).
	Overrides map[string]*composeServiceOverrideWire
}

// composeServiceOverrideWire is the per-service override surface inside a
// [compose.<alias>.<svc>] sub-table. It deliberately *includes* Restart
// (unlike [services.*], which always restarts) and *excludes* Run /
// ComposeFile / ComposeService (an override never specifies its own
// execution backend; that comes from the parent compose block).
type composeServiceOverrideWire struct {
	Group       string `toml:"group,omitempty"`
	Description string `toml:"description,omitempty"`

	APITrigger *bool `toml:"api_trigger,omitempty"`

	Timeout      string                  `toml:"timeout,omitempty"`
	GracefulStop string                  `toml:"graceful_stop,omitempty"`
	OnOverlap    model.ConcurrencyPolicy `toml:"on_overlap,omitempty"`
	Restart      model.RestartPolicy     `toml:"restart,omitempty"`
	Instances    int                     `toml:"instances,omitempty"`

	RestartDelay      string `toml:"restart_delay,omitempty"`
	RestartBackoff    string `toml:"restart_backoff,omitempty"`
	BackoffResetAfter string `toml:"backoff_reset_after,omitempty"`

	LogMaxSize string `toml:"log_max_size,omitempty"`
	LogOnFull  string `toml:"log_on_full,omitempty"`

	KeepRuns int    `toml:"keep_runs,omitempty"`
	KeepFor  string `toml:"keep_for,omitempty"`

	Env         map[string]string `toml:"env,omitempty"`
	EnvFile     string            `toml:"env_file,omitempty"`
	Secrets     map[string]string `toml:"secrets,omitempty"`
	SecretsFile string            `toml:"secrets_file,omitempty"`

	NotifyOnFailure []string `toml:"notify_on_failure,omitempty"`
	NotifyOnSuccess []string `toml:"notify_on_success,omitempty"`
}

func expandComposeAlias(alias string, raw map[string]any, baseDir string, existingNames map[string]struct{}) ([]model.Task, error) {
	block, err := parseComposeBlock(alias, raw)
	if err != nil {
		return nil, err
	}

	resolvedFile, err := resolveComposeFile(block.File, baseDir)
	if err != nil {
		return nil, err
	}
	block.File = resolvedFile

	if block.WorkingDir == "" {
		block.WorkingDir = filepath.Dir(resolvedFile)
	} else {
		if !filepath.IsAbs(block.WorkingDir) {
			block.WorkingDir = filepath.Join(baseDir, block.WorkingDir)
		}
		block.WorkingDir, err = filepath.Abs(block.WorkingDir)
		if err != nil {
			return nil, err
		}
	}

	// Absolutize env-file paths and write them back onto the block so the same
	// resolved paths flow into ComposeExecution.EnvFile — like the compose file,
	// they are passed to a CLI whose cwd is working_dir, not baseDir.
	for i, p := range block.EnvFile {
		if !filepath.IsAbs(p) {
			abs, absErr := filepath.Abs(filepath.Join(baseDir, p))
			if absErr != nil {
				return nil, absErr
			}
			block.EnvFile[i] = abs
		}
	}

	project, err := composespec.Load(resolvedFile, block.Profiles, block.EnvFile, block.WorkingDir)
	if err != nil {
		return nil, err
	}

	switch block.Mode {
	case model.ComposeModeStack:
		return expandComposeStack(block, project, existingNames)
	default:
		return expandComposeServices(block, project, existingNames)
	}
}

// parseComposeBlock destructures the raw map into scalar fields + per-service
// override sub-tables. Returns clean error messages naming the offending key.
func parseComposeBlock(alias string, raw map[string]any) (*composeBlock, error) {
	block := &composeBlock{
		Alias:      alias,
		Mode:       model.ComposeModeServices,
		Pull:       model.ComposePullMissing,
		NameFormat: "{alias}.{service}",
		Overrides:  make(map[string]*composeServiceOverrideWire),
	}

	for key, value := range raw {
		if _, reserved := composeReservedKeys[key]; reserved {
			if err := setComposeScalar(block, key, value); err != nil {
				return nil, err
			}
			continue
		}
		// Non-reserved key — must be a sub-table (per-service override).
		subTable, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unknown key %q (not a per-service override table; reserved keys: %s)",
				key, strings.Join(sortedKeys(composeReservedKeys), ", "))
		}
		override, err := decodeServiceOverride(key, subTable)
		if err != nil {
			return nil, err
		}
		block.Overrides[key] = override
	}

	if block.File == "" && block.Mode != model.ComposeModeStack {
		// File auto-discovery happens later. Stack mode is allowed without file.
	}
	if len(block.Include) > 0 && len(block.Exclude) > 0 {
		return nil, fmt.Errorf("`include` and `exclude` are mutually exclusive")
	}
	if !slices.Contains(validComposeMode, block.Mode) {
		return nil, fmt.Errorf("invalid mode %q: must be one of %s", block.Mode, strings.Join(validComposeMode, ", "))
	}
	if !slices.Contains(validComposePull, block.Pull) {
		return nil, fmt.Errorf("invalid pull %q: must be one of %s", block.Pull, strings.Join(validComposePull[:3], ", "))
	}
	if !strings.Contains(block.NameFormat, "{service}") && block.Mode == model.ComposeModeServices {
		return nil, fmt.Errorf("name_format %q must contain {service}", block.NameFormat)
	}
	if block.Mode == model.ComposeModeStack {
		if len(block.Overrides) > 0 {
			return nil, fmt.Errorf("per-service overrides are not allowed in mode=\"stack\"")
		}
		if len(block.Include) > 0 || len(block.Exclude) > 0 {
			return nil, fmt.Errorf("include/exclude are not allowed in mode=\"stack\"")
		}
	}

	if block.Group == "" {
		block.Group = alias
	}
	if block.ProjectName == "" {
		block.ProjectName = alias
	}
	return block, nil
}

func setComposeScalar(block *composeBlock, key string, value any) error {
	switch key {
	case "file":
		s, err := asString(key, value)
		if err != nil {
			return err
		}
		block.File = s
	case "include":
		ss, err := asStringSlice(key, value)
		if err != nil {
			return err
		}
		block.Include = ss
	case "exclude":
		ss, err := asStringSlice(key, value)
		if err != nil {
			return err
		}
		block.Exclude = ss
	case "mode":
		s, err := asString(key, value)
		if err != nil {
			return err
		}
		block.Mode = s
	case "group":
		s, err := asString(key, value)
		if err != nil {
			return err
		}
		block.Group = s
	case "project_name":
		s, err := asString(key, value)
		if err != nil {
			return err
		}
		block.ProjectName = s
	case "profiles":
		ss, err := asStringSlice(key, value)
		if err != nil {
			return err
		}
		block.Profiles = ss
	case "env_file":
		ss, err := asStringSlice(key, value)
		if err != nil {
			return err
		}
		block.EnvFile = ss
	case "working_dir":
		s, err := asString(key, value)
		if err != nil {
			return err
		}
		block.WorkingDir = s
	case "with_deps":
		b, ok := value.(bool)
		if !ok {
			return fmt.Errorf("`with_deps` must be a bool, got %T", value)
		}
		block.WithDeps = b
	case "pull":
		s, err := asString(key, value)
		if err != nil {
			return err
		}
		block.Pull = s
	case "name_format":
		s, err := asString(key, value)
		if err != nil {
			return err
		}
		block.NameFormat = s
	}
	return nil
}

// decodeServiceOverride re-marshals the raw sub-table to TOML and decodes it
// into a composeServiceOverrideWire. This buys us full type validation for
// the override without re-implementing every duration/byte-size parser by
// hand. Strict decode catches typos and disallowed keys (run, compose_file)
// with the standard go-toml error shape.
func decodeServiceOverride(svcName string, raw map[string]any) (*composeServiceOverrideWire, error) {
	buf, err := toml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("service %q override: %w", svcName, err)
	}
	var w composeServiceOverrideWire
	dec := toml.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("service %q override: %w", svcName, err)
	}
	return &w, nil
}

func expandComposeServices(block *composeBlock, project *composespec.Project, existingNames map[string]struct{}) ([]model.Task, error) {
	available := project.ServiceNames()
	availableSet := make(map[string]struct{}, len(available))
	for _, n := range available {
		availableSet[n] = struct{}{}
	}

	// Reject service names that collide with a reserved compose-block key.
	for _, n := range available {
		if _, reserved := composeReservedKeys[n]; reserved {
			return nil, fmt.Errorf("compose service %q collides with a reserved key in [compose.%s]; rename the compose service", n, block.Alias)
		}
	}

	if err := validateComposeNameSet(block.Include, availableSet, "include"); err != nil {
		return nil, err
	}
	if err := validateComposeNameSet(block.Exclude, availableSet, "exclude"); err != nil {
		return nil, err
	}

	imported := selectComposeServices(available, block.Include, block.Exclude)
	importedSet := make(map[string]struct{}, len(imported))
	for _, n := range imported {
		importedSet[n] = struct{}{}
	}

	for name := range block.Overrides {
		if _, ok := importedSet[name]; !ok {
			if _, exists := availableSet[name]; !exists {
				return nil, fmt.Errorf("override for service %q does not exist in compose file", name)
			}
			return nil, fmt.Errorf("override for service %q does not match any imported service (filtered out by include/exclude)", name)
		}
	}

	tasks := make([]model.Task, 0, len(imported))
	for _, svcName := range imported {
		svc := project.Service(svcName)
		taskName := applyNameFormat(block.NameFormat, block.Alias, svcName)
		if err := model.ValidateTaskName(taskName); err != nil {
			return nil, fmt.Errorf("name_format %q produced invalid task name %q: %w", block.NameFormat, taskName, err)
		}
		if _, dup := existingNames[taskName]; dup {
			return nil, fmt.Errorf("name %q (from compose service %q) collides with an existing task or service", taskName, svcName)
		}

		task, err := buildComposeServiceTask(block, svc, svcName, taskName)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
		existingNames[taskName] = struct{}{}
	}
	return tasks, nil
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
		Name:       taskName,
		Kind:       model.KindService,
		Group:      block.Group,
		APITrigger: true,
		Restart:    model.RestartOnFailure,
		Instances:  1,
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
// graceful_stop=compose stop_grace_period (when set). Override values win
// over the defaults.
func buildComposeServiceTask(block *composeBlock, svc *composespec.Service, svcName, taskName string) (model.Task, error) {
	task := model.Task{
		Name:       taskName,
		Kind:       model.KindService,
		Group:      block.Group,
		APITrigger: true,
		Restart:    model.RestartOnFailure,
		Instances:  1,
	}
	if svc != nil && svc.StopGracePeriod > 0 {
		task.GracefulStop = svc.StopGracePeriod
	}
	if err := applyComposeOverride(&task, block.Overrides[svcName], svcName); err != nil {
		return model.Task{}, err
	}
	task.ExecutionDef = &model.ComposeExecution{
		File:        block.File,
		ProjectName: block.ProjectName,
		Service:     svcName,
		Mode:        model.ComposeModeServices,
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
// Empty/zero override fields leave the compose-import default intact. The
// override's APITrigger pointer distinguishes "unset" from "explicitly false".
func applyComposeOverride(task *model.Task, w *composeServiceOverrideWire, svcName string) error {
	if w == nil {
		return nil
	}
	if w.Group != "" {
		task.Group = w.Group
	}
	if w.Description != "" {
		task.Description = w.Description
	}
	if w.APITrigger != nil {
		task.APITrigger = *w.APITrigger
	}
	if w.OnOverlap != "" {
		task.OnOverlap = w.OnOverlap
	}
	if w.Restart != "" {
		task.Restart = w.Restart
	}
	if w.Instances > 0 {
		task.Instances = w.Instances
	}
	if w.LogOnFull != "" {
		task.LogOnFull = w.LogOnFull
	}
	if w.RestartBackoff != "" {
		task.RestartBackoff = w.RestartBackoff
	}
	if w.KeepRuns != 0 {
		task.KeepRuns = w.KeepRuns
	}
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
	if len(w.NotifyOnFailure) > 0 {
		// Field not on Task today; consumed by notify config at expansion time.
		// Stored on task via the same pathway as [services.*] — but the
		// compose pipeline doesn't yet propagate notify lists. Leave a hook
		// for a follow-up once notify wiring is extended.
		_ = w.NotifyOnFailure
	}
	// Duration / byte-size string fields require the same parsers as the
	// regular service path. Reuse them so error messages match.
	if w.Timeout != "" {
		d, err := parseDuration(w.Timeout)
		if err != nil {
			return fmt.Errorf("service %q override: invalid timeout: %w", svcName, err)
		}
		task.Timeout = d
	}
	if w.GracefulStop != "" {
		d, err := parseDuration(w.GracefulStop)
		if err != nil {
			return fmt.Errorf("service %q override: invalid graceful_stop: %w", svcName, err)
		}
		task.GracefulStop = d
	}
	if w.RestartDelay != "" {
		d, err := parseDuration(w.RestartDelay)
		if err != nil {
			return fmt.Errorf("service %q override: invalid restart_delay: %w", svcName, err)
		}
		task.RestartDelay = d
	}
	if w.BackoffResetAfter != "" {
		d, err := parseDuration(w.BackoffResetAfter)
		if err != nil {
			return fmt.Errorf("service %q override: invalid backoff_reset_after: %w", svcName, err)
		}
		task.BackoffResetAfter = d
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

func asString(key string, value any) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("`%s` must be a string, got %T", key, value)
	}
	return s, nil
}

func asStringSlice(key string, value any) ([]string, error) {
	raw, ok := value.([]any)
	if !ok {
		// go-toml/v2 might pass through as []string directly in some shapes.
		if ss, alt := value.([]string); alt {
			return ss, nil
		}
		return nil, fmt.Errorf("`%s` must be an array of strings, got %T", key, value)
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("`%s`[%d] must be a string, got %T", key, i, v)
		}
		out[i] = s
	}
	return out, nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
