// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
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
