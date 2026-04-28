// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
)

// WriteInit creates a starter runwisp.toml at path. It errors if the file
// already exists. The contents are a self-documenting template that makes
// adding new tasks obvious.
func WriteInit(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	return os.WriteFile(path, []byte(starterConfig), 0644)
}

// WriteInitForce creates a starter runwisp.toml at path, overwriting any
// existing file. Used by `runwisp init --force`.
func WriteInitForce(path string) error {
	return os.WriteFile(path, []byte(starterConfig), 0644)
}

const starterConfig = `# RunWisp configuration.
# Docs: https://github.com/runwisp/runwisp
#
# Every task needs a name (the table key) and a "run" command.
# Everything else has sensible defaults — add a key only when you need it.

[tasks.hello]
description = "A minimal example task you can trigger from the UI or CLI."
run = "echo hello from runwisp"

# Example of a scheduled task:
#
# [tasks.heartbeat]
# cron = "* * * * *"
# run  = "curl -fsS https://example.com/healthz"
#
# Example of an always-on service (one or more long-lived processes that
# RunWisp keeps alive with exponential restart backoff):
#
# [services.api-worker]
# instances = 3
# run       = "exec ./bin/worker"
#
# Full list of per-task keys:
#   group, description
#   cron, api_trigger, catch_up          # latest | all | skip
#   timeout, parallelism, on_overlap     # restart=always is rejected on tasks; use [services.*]
#   retry_attempts, retry_delay, retry_backoff
#   log_max_size, log_on_full            # drop_new | drop_old | kill_task
#   keep_runs, keep_for
#
# Per-service keys ([services.*] only):
#   group, description, run, instances
#   timeout, parallelism, on_overlap
#   restart_delay, restart_backoff       # none | exponential
#   log_max_size, log_on_full, keep_runs, keep_for
#
# Global sections:
#
# [storage]
# max_size       = "5gb"
# min_free_space = "500mb"
#
# [defaults]
# timeout      = "1h"
# log_max_size = "100mb"
# log_on_full  = "drop_old"
# keep_runs    = 50
# keep_for     = "30d"
`
