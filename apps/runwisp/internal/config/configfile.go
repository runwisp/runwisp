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

// TwoTierRootConfig returns the root runwisp.toml `import`/`adopt` scaffold when
// no config exists yet: it wires in the machine-owned runwisp.d staging directory
// and explains the two-tier layout, while staying a file the operator owns and
// keeps in git. Imported jobs land in runwisp.d/imported.toml; `runwisp promote`
// graduates one into this file.
func TwoTierRootConfig() string {
	return twoTierRootConfig
}

const twoTierRootConfig = SchemaDirective + `# runwisp.toml
# Docs: https://docs.runwisp.com/recipes/migrating-from-cron/
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
