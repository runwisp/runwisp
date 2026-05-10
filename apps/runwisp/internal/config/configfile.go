// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"strings"
)

// WriteInit creates a starter runwisp.toml at path. It errors if the file
// already exists. The contents are a self-documenting template that makes
// adding new tasks obvious.
func WriteInit(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	return os.WriteFile(path, []byte(renderStarterConfig(ResolveSystemTimezone())), 0644)
}

// WriteInitForce creates a starter runwisp.toml at path, overwriting any
// existing file. Used by `runwisp init --force`.
func WriteInitForce(path string) error {
	return os.WriteFile(path, []byte(renderStarterConfig(ResolveSystemTimezone())), 0644)
}

// renderStarterConfig stamps the detected system timezone into the template
// so the operator doesn't have to pick a zone before the daemon is even up.
func renderStarterConfig(tz string) string {
	if tz == "" {
		tz = "UTC"
	}
	return strings.ReplaceAll(starterConfig, "{{TIMEZONE}}", tz)
}

const starterConfig = `# RunWisp configuration.
# Docs: https://github.com/runwisp/runwisp
#
# Every task needs a name (the table key) and a "run" command.
# Everything else has sensible defaults — add a key only when you need it.

# Cron expressions are evaluated in this timezone. RunWisp filled it in from
# the host on first run; change it to any IANA name or set TZ before
# 'runwisp init' to override. Per-task 'timezone = "..."' wins for that task.
[scheduler]
timezone = "{{TIMEZONE}}"

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
# Full list of per-task keys ([tasks.*]):
#   group, description
#   cron, api_trigger, catch_up          # latest | all | skip
#   max_catch_up_runs                    # positive integer, default 100
#   timeout, graceful_stop               # graceful_stop = SIGTERM-to-SIGKILL window
#   max_concurrent, queue_max, on_overlap # restart=always is rejected on tasks
#   retry_attempts, retry_delay, retry_backoff   # constant | linear | exponential
#   log_max_size, log_on_full            # drop_new | drop_old | kill_task
#   keep_runs, keep_for
#
# Per-service keys ([services.*] only):
#   group, description, run, instances, on_overlap, timeout, graceful_stop
#   restart_delay, restart_backoff       # constant | linear | exponential
#   backoff_reset_after                  # replica-uptime that resets restart attempts
#   log_max_size, log_on_full, keep_runs, keep_for
#
# Global sections:
#
# [storage]
# max_size       = "5gb"
# min_free_space = "500mb"
#
# [defaults]
# timeout              = "1h"
# log_max_size         = "100mb"
# log_on_full          = "drop_old"
# keep_runs            = 50
# keep_for             = "30d"
# backoff_reset_after  = "60s"
#
# [daemon]
# shutdown_timeout = "10s"
`
