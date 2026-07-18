// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportDemoNoTUI_PasswordToStdoutGuidanceToStderr(t *testing.T) {
	client := &fakeCredentialsClient{
		creds: &apiclient.LocalCredentials{Password: "Kj2x9pQ7mN4vL8rT5wYz1c", Ephemeral: true},
	}
	f := Flags{Host: "127.0.0.1", Port: 9477, DataDir: "/tmp/runwisp-demo-xyz/data"}
	var stdout, stderr bytes.Buffer

	code := reportDemoNoTUI(&stdout, &stderr, client, f)

	assert.Equal(t, passwordExitOK, code)
	assert.Equal(t, "Kj2x9pQ7mN4vL8rT5wYz1c\n", stdout.String(),
		"stdout must carry only the password so the command stays pipeable")
	assert.Contains(t, stderr.String(), "http://127.0.0.1:9477",
		"operator needs the Web UI URL to connect")
	assert.Contains(t, stderr.String(), "runwisp stop --data /tmp/runwisp-demo-xyz/data",
		"operator needs the shutdown command for the throwaway daemon")
}

func TestReportDemoNoTUI_WildcardBindReportsLocalhost(t *testing.T) {
	client := &fakeCredentialsClient{
		creds: &apiclient.LocalCredentials{Password: "pw", Ephemeral: true},
	}
	f := Flags{Host: "0.0.0.0", Port: 8080, DataDir: "/tmp/demo/data"}
	var stdout, stderr bytes.Buffer

	reportDemoNoTUI(&stdout, &stderr, client, f)

	assert.Contains(t, stderr.String(), "https://localhost:8080",
		"a 0.0.0.0 bind is not connectable (report localhost) and auto-TLS makes it https")
}

func TestReportDemoNoTUI_NoPasswordExitsNonZeroWithoutLeaking(t *testing.T) {
	client := &fakeCredentialsClient{err: apiclient.ErrAuthDisabled}
	f := Flags{Host: "127.0.0.1", Port: 9477, DataDir: "/tmp/demo/data"}
	var stdout, stderr bytes.Buffer

	code := reportDemoNoTUI(&stdout, &stderr, client, f)

	assert.Equal(t, passwordExitNoAuth, code)
	assert.Empty(t, stdout.String(), "stdout must stay empty when there is no password to print")
}

// setSeedOnly flips the package-global demoFlags into --seed-only mode for the
// duration of a test and restores them afterwards. demoFlags is a global, so
// these tests must not run in parallel with each other.
func setSeedOnly(t *testing.T, cloud bool) {
	t.Helper()
	prev := demoFlags
	demoFlags.SeedOnly = true
	demoFlags.Cloud = cloud
	t.Cleanup(func() { demoFlags = prev })
}

func TestRunDemoSeedOnlyWritesConfigAndSeeds(t *testing.T) {
	setSeedOnly(t, false)

	dir := t.TempDir()
	f := Flags{
		CfgFile: filepath.Join(dir, "runwisp.toml"),
		DataDir: filepath.Join(dir, "data"),
	}

	require.NoError(t, runDemo(demoCmd, f))

	// --seed-only writes the embedded demo config and seeds a real database +
	// log dir into the caller-supplied paths, then returns without spawning a
	// daemon or TUI.
	assert.FileExists(t, f.CfgFile)
	assert.FileExists(t, f.DBPath())
	assert.DirExists(t, f.LogDir())
}

func TestDemoPathFlagsRejection(t *testing.T) {
	t.Parallel()
	assert.NoError(t, demoPathFlagsRejection(false, false), "no explicit paths → demo proceeds")

	for _, tc := range []struct {
		name              string
		config, dataFlags bool
	}{
		{"config only", true, false},
		{"data only", false, true},
		{"both", true, true},
	} {
		err := demoPathFlagsRejection(tc.config, tc.dataFlags)
		require.Errorf(t, err, "%s must be rejected", tc.name)
		assert.Contains(t, err.Error(), "--seed-only")
	}
}

func TestRunDemoSeedOnlyRejectsCloud(t *testing.T) {
	setSeedOnly(t, true)

	f := Flags{
		CfgFile: filepath.Join(t.TempDir(), "runwisp.toml"),
		DataDir: filepath.Join(t.TempDir(), "data"),
	}

	err := runDemo(demoCmd, f)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--seed-only cannot be combined with --cloud")
}
