// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/runwisp/runwisp/internal/model"
)

// loadWithIncludes loads the root config and any files pulled in via
// [daemon].include, returning the merged result as a Config plus an entrySources
// map so later path resolution honors each entry's origin file.
//
// Merge semantics:
//   - collections ([tasks.*], [services.*], [compose.*], [[notifier]],
//     [[notification_route]]) accumulate — root first, then matched globs in
//     lexicographic order;
//   - a task / service / compose-alias name defined in two files is a hard
//     error naming both files;
//   - singleton tables ([daemon], [storage], [defaults], [scheduler],
//     [notify]) may appear only in the root — setting one in an included file
//     is a hard error;
//   - included files may not themselves include (flat-only).
func loadWithIncludes(path string) (*Config, entrySources, error) {
	rootDir := filepath.Dir(path)
	rootAbs, err := filepath.Abs(path)
	if err != nil {
		rootAbs = path
	}
	rootData, err := os.ReadFile(path)
	if err != nil {
		return nil, entrySources{}, fmt.Errorf("failed to read config file: %w", err)
	}
	root, err := parseWire(rootData, rootDir)
	if err != nil {
		return nil, entrySources{}, err
	}

	src := entrySources{root: rootDir, byName: map[string]string{}}
	for _, name := range entryNames(root) {
		src.byName[name] = rootAbs
	}

	globs, matched, err := resolveIncludes(root.Daemon.Include, rootDir, path)
	if err != nil {
		return nil, entrySources{}, err
	}

	for _, incPath := range matched {
		if err := mergeIncludeFile(root, incPath, path, src.byName); err != nil {
			return nil, entrySources{}, err
		}
	}

	// Cron sources merge after the TOML includes and before buildConfig, so
	// defaults, validation, and the reload diff treat a cron-sourced task exactly
	// like a hand-written one. Doing it after buildConfig would need a second,
	// differently-behaved path from crontab to running task.
	cron, err := mergeCronSources(root, root.Daemon.IncludeCron, rootDir, path, src.byName, matched)
	if err != nil {
		return nil, entrySources{}, err
	}

	cfg, err := buildConfig(root)
	if err != nil {
		return nil, entrySources{}, err
	}
	cfg.origins = src.byName
	markProvenance(cfg, rootDir, cron.originSet())
	cfg.includeFiles = matched
	cfg.includeGlobs = append(globs, cron.globs...)
	cfg.cronFiles = cron.files
	cfg.CronFindings = cron.findings
	cfg.cronBlocks = cron.blocks
	return cfg, src, nil
}

// markProvenance stamps Task.Source and Task.SourceFile from each task's origin
// file, so the API/UI can surface the "staged"/"cron" badge and the Promote
// affordance, and so the TUI can name the file a definition actually lives in.
//
// Derived by exact path, not by basename, so a stray file named imported.toml
// elsewhere is not mistaken for the staging file. Entries with no recorded origin
// — compose-generated tasks — stay native. Re-derived every load, so promoting a
// task into the root flips it to native on its own.
func markProvenance(cfg *Config, rootDir string, cronFiles map[string]bool) {
	staging := StagingFilePath(rootDir)
	for i := range cfg.Tasks {
		origin := cfg.OriginFile(cfg.Tasks[i].Name)
		switch {
		case origin == "":
			continue
		case cronFiles[origin]:
			cfg.Tasks[i].Source = model.SourceCron
		case origin == staging:
			cfg.Tasks[i].Source = model.SourceStaged
		default:
			continue // the operator's own TOML
		}
		cfg.Tasks[i].SourceFile = origin
	}
}

// mergeIncludeFile reads and parses one included file, enforces the flat-only
// (no nested include) and root-only-singleton rules, then folds its entries
// into root. rootPath names the including file in error messages.
func mergeIncludeFile(root *tomlConfig, incPath, rootPath string, byName map[string]string) error {
	incDir := filepath.Dir(incPath)
	data, err := os.ReadFile(incPath)
	if err != nil {
		return fmt.Errorf("failed to read included config %s: %w", incPath, err)
	}
	inc, err := parseWire(data, incDir)
	if err != nil {
		return fmt.Errorf("included config %s: %w", incPath, err)
	}
	if len(inc.Daemon.Include) > 0 {
		return fmt.Errorf("included config %s sets [daemon].include; includes may not be nested (included from %s)", incPath, rootPath)
	}
	if err := assertNoSingletons(inc, incPath); err != nil {
		return err
	}
	return mergeWire(root, inc, incPath, byName)
}

// entryNames returns the task, service, and compose-alias names declared in a
// wire — the shared namespace that must stay collision-free across files.
func entryNames(w *tomlConfig) []string {
	names := make([]string, 0, len(w.Tasks)+len(w.Services)+len(w.Compose))
	for n := range w.Tasks {
		names = append(names, n)
	}
	for n := range w.Services {
		names = append(names, n)
	}
	for n := range w.Compose {
		names = append(names, n)
	}
	return names
}

// resolveIncludes expands each include pattern against the root config dir and
// returns the resolved (absolute) patterns plus the deduplicated, lexically
// sorted set of matched files. The root config is skipped if it matches its
// own glob. Zero matches is not an error — an empty conf.d/ is fine.
func resolveIncludes(patterns []string, rootDir, rootPath string) (resolvedGlobs, matched []string, err error) {
	rootAbs, _ := filepath.Abs(rootPath)
	seen := map[string]struct{}{}
	for _, pat := range patterns {
		abs, err := resolvePath(rootDir, pat)
		if err != nil {
			return nil, nil, fmt.Errorf("daemon.include %q: %w", pat, err)
		}
		resolvedGlobs = append(resolvedGlobs, abs)
		hits, err := filepath.Glob(abs)
		if err != nil {
			return nil, nil, fmt.Errorf("daemon.include %q: %w", pat, err)
		}
		matched = appendGlobHits(matched, hits, rootAbs, seen)
	}
	sort.Strings(matched)
	return resolvedGlobs, matched, nil
}

// appendGlobHits absolutizes each glob hit and appends the new ones to matched,
// skipping the root config itself and any already-seen path (seen is updated in
// place). A hit whose absolute path can't be resolved is kept as-is.
func appendGlobHits(matched, hits []string, rootAbs string, seen map[string]struct{}) []string {
	for _, h := range hits {
		ha, err := filepath.Abs(h)
		if err != nil {
			ha = h
		}
		if ha == rootAbs {
			continue
		}
		if _, dup := seen[ha]; dup {
			continue
		}
		seen[ha] = struct{}{}
		matched = append(matched, ha)
	}
	return matched
}

// assertNoSingletons rejects any singleton table set in an included file. Empty
// (absent) tables decode to zero values, so reflect.IsZero distinguishes
// "section present with content" from "section omitted". Called after the
// nested-include check, so a stray [daemon].include is reported there with a
// more specific message rather than as a generic [daemon] violation.
func assertNoSingletons(inc *tomlConfig, file string) error {
	sections := []struct {
		name string
		v    any
	}{
		{"[daemon]", inc.Daemon},
		{"[storage]", inc.Storage},
		{"[defaults]", inc.Defaults},
		{"[scheduler]", inc.Scheduler},
		{"[notify]", inc.Notify},
	}
	for _, s := range sections {
		if !reflect.ValueOf(s.v).IsZero() {
			return fmt.Errorf("included config %s sets %s; that table may only appear in the root config", file, s.name)
		}
	}
	return nil
}

// mergeWire folds an included wire into the root: it accumulates the
// collections and records each entry's origin, rejecting any task / service /
// compose-alias name already claimed by another file.
func mergeWire(root, inc *tomlConfig, incPath string, byName map[string]string) error {
	if err := recordEntryOrigins(inc, incPath, byName); err != nil {
		return err
	}
	mergeEntryTables(root, inc)
	root.Notifiers = append(root.Notifiers, inc.Notifiers...)
	root.Routes = append(root.Routes, inc.Routes...)
	return nil
}

// recordEntryOrigins records each of the included file's entry names against
// its origin file, rejecting any name already claimed by another file. A name
// repeated within the same file is left for buildConfig to report.
func recordEntryOrigins(inc *tomlConfig, incPath string, byName map[string]string) error {
	for _, name := range entryNames(inc) {
		if prev, ok := byName[name]; ok {
			if prev == incPath {
				// Two tables in this same file share a name (e.g. [tasks.x] and
				// [services.x]); let buildConfig report it with its own phrasing.
				continue
			}
			return fmt.Errorf("duplicate task/service name %q defined in both %s and %s", name, prev, incPath)
		}
		byName[name] = incPath
	}
	return nil
}

// mergeEntryTables folds the included file's task/service/compose maps into the
// root, lazily allocating each map on first use.
func mergeEntryTables(root, inc *tomlConfig) {
	if len(inc.Tasks) > 0 {
		if root.Tasks == nil {
			root.Tasks = make(map[string]*taskWire, len(inc.Tasks))
		}
		for k, v := range inc.Tasks {
			root.Tasks[k] = v
		}
	}
	if len(inc.Services) > 0 {
		if root.Services == nil {
			root.Services = make(map[string]*serviceWire, len(inc.Services))
		}
		for k, v := range inc.Services {
			root.Services[k] = v
		}
	}
	if len(inc.Compose) > 0 {
		if root.Compose == nil {
			root.Compose = make(map[string]map[string]any, len(inc.Compose))
		}
		for k, v := range inc.Compose {
			root.Compose[k] = v
		}
	}
}
