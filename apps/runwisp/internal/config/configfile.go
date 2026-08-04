// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "strings"

// This file holds the config templates RunWisp scaffolds from. They are content,
// not I/O: internal/configedit owns writing them to disk, so the loader package
// stays a pure reader.

// StarterConfig returns the minimal, self-documenting runwisp.toml that
// `runwisp init` / first-run scaffolds. The full schema reference lives in the
// docs rather than in the template.
func StarterConfig() string {
	return starterConfig
}

// ComposeStarterConfig returns the runwisp.toml that imports an adjacent
// docker-compose file. composeFilename is the basename of the discovered compose
// file (e.g. "docker-compose.yml"); alias is the [compose.<alias>] block name
// (usually the parent directory name, sanitized).
func ComposeStarterConfig(composeFilename, alias string) string {
	r := strings.NewReplacer("{{compose}}", composeFilename, "{{alias}}", alias)
	return r.Replace(composeStarterConfig)
}

// CronStarterConfig returns the runwisp.toml that reads real crontabs live via
// `[daemon] include_cron`. patterns are the globs to bake in — normally
// CronScan.Globs from the first-run detection that decided to offer this.
func CronStarterConfig(patterns []string) string {
	return SchemaDirective + `# runwisp.toml
# Docs: https://docs.runwisp.com/replacing-cron/take-over-from-cron/
#
# RunWisp found cron jobs on this box and scaffolded this file to read them
# live. Nothing is converted or rewritten: crontab -e keeps working, and
# ` + "`runwisp reload`" + ` picks up edits. Run ` + "`runwisp promote <task>`" + ` when
# you're ready to move one into native TOML.

` + cronIncludeBlock(patterns)
}

// ComposeAndCronStarterConfig combines ComposeStarterConfig and
// CronStarterConfig into one file, for the first run where an adjacent
// compose file and a readable crontab are both detected — the interactive
// scaffold asks a single yes/no question, so a "yes" has to cover both.
func ComposeAndCronStarterConfig(composeFilename, alias string, patterns []string) string {
	r := strings.NewReplacer("{{compose}}", composeFilename, "{{alias}}", alias)
	body := SchemaDirective + `# runwisp.toml
# Docs: https://docs.runwisp.com/configuration/compose/ and
#       https://docs.runwisp.com/replacing-cron/take-over-from-cron/
#
# RunWisp detected {{compose}} alongside, and found cron jobs it can read
# live. Neither is converted or rewritten.

[compose.{{alias}}]
file = "./{{compose}}"

` + cronIncludeBlock(patterns)
	return r.Replace(body)
}

// cronIncludeBlock renders a whole `[daemon]` table whose only key is
// include_cron — what a scaffolded config needs, where no [daemon] exists yet.
func cronIncludeBlock(patterns []string) string {
	return "[daemon]\n" + CronIncludeArray(patterns)
}

// CronIncludeArray renders just the `include_cron = [...]` key, one pattern per
// line, with no table header. Scaffolding wraps it in a fresh `[daemon]`;
// internal/configedit inserts it under an existing one. Both go through here so
// a wired config and a scaffolded one are formatted identically.
func CronIncludeArray(patterns []string) string {
	var b strings.Builder
	b.WriteString("include_cron = [\n")
	for _, p := range patterns {
		b.WriteString("  \"")
		b.WriteString(p)
		b.WriteString("\",\n")
	}
	b.WriteString("]\n")
	return b.String()
}

// TwoTierRootConfig returns the root runwisp.toml `import`/`adopt` scaffold when
// no config exists yet: it wires in the machine-owned runwisp.d staging directory
// and explains the two-tier layout, while staying a file the operator owns and
// keeps in git. Imported jobs land in runwisp.d/imported.toml; `runwisp promote`
// graduates one into this file.
func TwoTierRootConfig() string {
	return twoTierRootConfig
}

const twoTierRootConfig = SchemaDirective + `# runwisp.toml
# Docs: https://docs.runwisp.com/replacing-cron/converting-crontabs/
#
# Your imported jobs live in ` + ImportedStagingSubdir + `/` + ImportedStagingBase + ` (machine-managed by
# ` + "`runwisp import`" + `). This root file is yours: add native [tasks.*] here,
# and ` + "`runwisp promote <name>`" + ` graduates an imported job into it. Keep
# this file in version control.

[daemon]
include = ["` + StagingIncludeGlob + `"]
`

const starterConfig = SchemaDirective + `# runwisp.toml
# Docs: https://docs.runwisp.com/configuration/overview/

[tasks.hello]
description = "Example task. Trigger it from the TUI (press r) or the Web UI."
run = "echo hello from runwisp"

# Schedule a task with cron:
# [tasks.heartbeat]
# cron = "* * * * *"
# run  = "date"

# Long-running service (auto-restart, supports multiple instances):
# [services.worker]
# instances = 1
# run       = "node ./worker.js"
`

const composeStarterConfig = SchemaDirective + `# runwisp.toml
# Docs: https://docs.runwisp.com/configuration/compose/
#
# RunWisp detected {{compose}} next to this file and scaffolded a
# compose import. Every service in the compose file becomes an observable
# RunWisp service — logs, restart policies, notifications, trigger/stop.

[compose.{{alias}}]
file = "./{{compose}}"

# Per-service overrides go in their own sub-table:
# [compose.{{alias}}.web]
# restart           = "always"
# notify_on_failure = ["slack-prod"]
`
