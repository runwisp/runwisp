// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package composespec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_BasicEnumeratesActiveProfileServices(t *testing.T) {
	p, err := Load("testdata/basic.yml", nil, nil, "")
	require.NoError(t, err)

	// db is profile-gated and absent without the profile.
	names := p.ServiceNames()
	assert.Equal(t, []string{"web", "worker"}, names)

	web := p.Service("web")
	require.NotNil(t, web)
	assert.Equal(t, 30*time.Second, web.StopGracePeriod)

	worker := p.Service("worker")
	require.NotNil(t, worker)
	assert.Zero(t, worker.StopGracePeriod, "no stop_grace_period set in fixture")

	assert.Nil(t, p.Service("missing"))
}

func TestLoad_ProfileGatedServicesAppearWhenEnabled(t *testing.T) {
	p, err := Load("testdata/basic.yml", []string{"heavy"}, nil, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"web", "worker", "db"}, p.ServiceNames())
}

func TestLoad_RejectsEmptyPath(t *testing.T) {
	_, err := Load("", nil, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestLoad_MissingFileSurfacesError(t *testing.T) {
	_, err := Load("testdata/does-not-exist.yml", nil, nil, "")
	require.Error(t, err)
}
