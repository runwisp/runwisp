// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDemoConfig pins the checked-in demo file (apps/runwisp/demo/runwisp.toml)
// to a working schema and a meaningful surface area. The demo is used for
// marketing screenshots; this test guards it against silent schema drift and
// against accidental gut-outs that would make screenshots look threadbare.
func TestDemoConfig(t *testing.T) {
	// Test cwd is the package directory, so the path is relative from there.
	cfg, err := Load("../../demo/runwisp.toml")
	require.NoError(t, err, "demo/runwisp.toml must load cleanly under the current schema")

	var services, cronTasks int
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Kind == model.KindService {
			services++
		}
		if cfg.Tasks[i].Cron != "" {
			cronTasks++
		}
	}

	assert.GreaterOrEqual(t, len(cfg.Tasks), 8, "demo should exercise a meaningful number of tasks+services")
	assert.GreaterOrEqual(t, services, 1, "demo should include at least one [services.*] entry")
	assert.GreaterOrEqual(t, cronTasks, 1, "demo should include at least one cron-scheduled task")
	assert.GreaterOrEqual(t, len(cfg.Notify.Notifiers), 3, "demo should declare at least three notifiers")
	assert.GreaterOrEqual(t, len(cfg.Notify.Routes), 3, "demo should expose at least three notification routes")
	assert.NotEmpty(t, cfg.Daemon.ExternalURL, "demo should set [daemon] external_url so notifications carry deep-links")
}
