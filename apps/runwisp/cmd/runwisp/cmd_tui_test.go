// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestBuildStartupInfoFromDaemon_NilReturnsEmpty(t *testing.T) {
	si := buildStartupInfoFromDaemon(nil)
	assert.Empty(t, si.Version)
	assert.Nil(t, si.Tasks)
}

func TestBuildStartupInfoFromDaemon_PopulatesAllFields(t *testing.T) {
	info := &model.DaemonInfo{
		Version:          "1.2.3",
		Fingerprint:      "fp-xyz",
		Port:             9477,
		CloudEnabled:     true,
		ResolvedTimezone: "Europe/Berlin",
		TimezoneSource:   "system",
		Tasks: []model.TaskBrief{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	si := buildStartupInfoFromDaemon(info)
	assert.Equal(t, "1.2.3", si.Version)
	assert.Equal(t, "fp-xyz", si.Fingerprint)
	assert.Equal(t, 9477, si.Port)
	assert.True(t, si.CloudEnabled)
	assert.Equal(t, "Europe/Berlin", si.Timezone)
	assert.Equal(t, "system", si.TimezoneSource)
	assert.Len(t, si.Tasks, 2)
	assert.Equal(t, "alpha", si.Tasks[0].Name)
}

func TestRunTUIClient_DaemonUnreachable(t *testing.T) {
	t.Parallel()
	err := runTUIClient(Flags{DataDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error when no daemon is reachable")
	}
}
