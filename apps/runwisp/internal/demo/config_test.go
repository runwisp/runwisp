// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package demo

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// TestDemoConfig pins the embedded demo file to a working schema and a
// meaningful surface area, guarding it against silent schema drift and against
// gut-outs that would make `runwisp demo` and screenshots look threadbare.
func TestDemoConfig(t *testing.T) {
	cfg := loadDemoConfig(t)

	var services, cronTasks, paramTasks int
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Kind == model.KindService {
			services++
		}
		if cfg.Tasks[i].Cron != "" {
			cronTasks++
		}
		if len(cfg.Tasks[i].Parameters) > 0 {
			paramTasks++
		}
	}

	assert.GreaterOrEqual(t, len(cfg.Tasks), 8, "demo should exercise a meaningful number of tasks+services")
	assert.GreaterOrEqual(t, services, 1, "demo should include at least one [services.*] entry")
	assert.GreaterOrEqual(t, cronTasks, 1, "demo should include at least one cron-scheduled task")
	assert.GreaterOrEqual(t, paramTasks, 1, "demo should include at least one task that declares per-execution params")
	assert.GreaterOrEqual(t, len(cfg.Notify.Notifiers), 3, "demo should declare at least three notifiers")
	assert.GreaterOrEqual(t, len(cfg.Notify.Routes), 3, "demo should expose at least three notification routes")
	assert.NotEmpty(t, cfg.Daemon.ExternalURL, "demo should set [daemon] external_url so notifications carry deep-links")
}
