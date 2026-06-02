// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// TestDemoConfig pins the embedded demo file (internal/demo/runwisp.toml) to a
// working schema and a meaningful surface area. The demo backs `runwisp demo`
// and marketing screenshots; this test guards it against silent schema drift
// and against accidental gut-outs that would make screenshots look threadbare.
func TestDemoConfig(t *testing.T) {
	cfg := loadDemoConfig(t)

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
