// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDependsOn_ParsedOnService(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.db]
run = "sleep 1"

[services.web]
run = "sleep 1"
depends_on = ["db"]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"db"}, findTask(t, cfg, "web").DependsOn)
}

func TestDependsOn_RejectedOnTask(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
depends_on = ["other"]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends_on")
}

func TestDependsOn_RejectsSelfReference(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.web]
run = "sleep 1"
depends_on = ["web"]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "itself")
}

func TestDependsOn_RejectsUnknownReference(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.web]
run = "sleep 1"
depends_on = ["nope"]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown name")
}

func TestDependsOn_RejectsTaskReference(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.migrate]
run = "echo migrate"

[services.web]
run = "sleep 1"
depends_on = ["migrate"]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "may only reference services")
}

func TestDependsOn_RejectsCycleNamingThePath(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.a]
run = "sleep 1"
depends_on = ["b"]

[services.b]
run = "sleep 1"
depends_on = ["a"]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
	// The reported path names both edges of the cycle.
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "b")
}

// TestDependsOn_DiamondIsValid: A→B, A→C, B→D, C→D is acyclic and must load.
func TestDependsOn_DiamondIsValid(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.a]
run = "sleep 1"
depends_on = ["b", "c"]

[services.b]
run = "sleep 1"
depends_on = ["d"]

[services.c]
run = "sleep 1"
depends_on = ["d"]

[services.d]
run = "sleep 1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"b", "c"}, findTask(t, cfg, "a").DependsOn)
}
