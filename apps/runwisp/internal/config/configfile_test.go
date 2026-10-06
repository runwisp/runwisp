// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Docker image can't run Go at build time, so it ships a copy of the
// starter that its entrypoint drops into an empty config mount. Keep the two
// identical so a container's first start matches `runwisp` on a fresh box.
func TestDockerStarterMatchesStarterConfig(t *testing.T) {
	got, err := os.ReadFile("../../../../docker/runwisp.toml")
	require.NoError(t, err)
	require.Equal(t, StarterConfig, string(got))
}
