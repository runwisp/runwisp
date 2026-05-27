// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"strings"
)

// WriteInit creates a starter runwisp.toml at path. It errors if the file
// already exists. The contents are a minimal, self-documenting template;
// the full schema reference lives in the docs.
func WriteInit(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	return os.WriteFile(path, []byte(starterConfig), 0644)
}

// WriteInitForce creates a starter runwisp.toml at path, overwriting any
// existing file.
func WriteInitForce(path string) error {
	return os.WriteFile(path, []byte(starterConfig), 0644)
}

// WriteInitWithCompose creates a runwisp.toml that imports an adjacent
// docker-compose file. composeFilename is the basename of the discovered
// compose file (e.g. "docker-compose.yml"); alias is the [compose.<alias>]
// block name (usually the parent directory name, sanitized).
func WriteInitWithCompose(path, composeFilename, alias string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	return os.WriteFile(path, []byte(renderComposeStarter(composeFilename, alias)), 0644)
}

func renderComposeStarter(composeFilename, alias string) string {
	r := strings.NewReplacer("{{compose}}", composeFilename, "{{alias}}", alias)
	return r.Replace(composeStarterConfig)
}

const starterConfig = `# runwisp.toml
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

const composeStarterConfig = `# runwisp.toml
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
