// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// TestRestartRefusesAnAppDaemon guards an SDK app's daemon: restarted from
// the CLI it would come back reading runwisp.toml, not the app's config.
func TestRestartRefusesAnAppDaemon(t *testing.T) {
	assert.Error(t, appDaemonRestartError(&model.InstanceInfo{ConfigSource: "app"}))
	assert.NoError(t, appDaemonRestartError(&model.InstanceInfo{ConfigSource: "file"}))
	assert.NoError(t, appDaemonRestartError(nil))
}
