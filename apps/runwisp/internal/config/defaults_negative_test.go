// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A negative duration in [defaults] is rejected whether or not a unit would
// have inherited it: warn_after and restart_delay are inherited by only one
// kind of unit, so without their own check a bad value loads and is ignored.
func TestLoad_DefaultsRejectNegativeDurations(t *testing.T) {
	for _, key := range []string{"warn_after", "restart_delay", "graceful_stop"} {
		t.Run(key, func(t *testing.T) {
			path := writeTOML(t, "[defaults]\n"+key+" = \"-5m\"\n\n[tasks.t]\nrun = \"echo hi\"\n")
			_, err := Load(path)
			require.Error(t, err, "a negative defaults.%s must not load", key)
			assert.Contains(t, err.Error(), "defaults."+key)
		})
	}
}
