// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package autostart

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func TestHostDescription_AllBranches(t *testing.T) {
	cases := map[string]string{
		"":          "loopback only",
		"127.0.0.1": "loopback only",
		"localhost": "loopback only",
		"0.0.0.0":   "ALL INTERFACES — accessible from the network",
		"::":        "ALL INTERFACES — accessible from the network",
		"10.0.0.5":  "10.0.0.5",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, hostDescription(in))
		})
	}
}

func TestRenderInstallBanner_PrintsStepsAndSettings(t *testing.T) {
	var buf bytes.Buffer
	plan := Plan{
		Kind:     PlanInstall,
		Steps:    []Step{{Action: ActionWriteUnit, Description: "Write unit file"}},
		Binary:   "/usr/local/bin/runwisp",
		Config:   "/etc/runwisp.toml",
		DataDir:  "/var/lib/runwisp",
		Port:     9477,
		Host:     "0.0.0.0",
		UnitPath: "/etc/systemd/system/runwisp.service",
	}
	renderInstallBanner(&buf, plan)
	out := buf.String()
	assert.Contains(t, out, "Write unit file")
	assert.Contains(t, out, "/usr/local/bin/runwisp")
	assert.Contains(t, out, "9477")
	assert.Contains(t, out, "ALL INTERFACES")
}

func TestRenderInstallBanner_UpdateShowsDiff(t *testing.T) {
	var buf bytes.Buffer
	plan := Plan{
		Kind:  PlanUpdate,
		Steps: []Step{{Description: "Update unit"}},
		Diff:  "--- a\n+++ b\n@@\n-old\n+new",
	}
	renderInstallBanner(&buf, plan)
	out := buf.String()
	assert.Contains(t, out, "drifted")
	assert.Contains(t, out, "+new")
}

func TestRenderUninstallBanner_AppendsPurgeStep(t *testing.T) {
	var buf bytes.Buffer
	plan := Plan{Steps: []Step{{Description: "Stop service"}, {Description: "Remove unit"}}}
	renderUninstallBanner(&buf, plan, UninstallOptions{Purge: true, DataDir: "/data"})
	out := buf.String()
	assert.Contains(t, out, "Stop service")
	assert.Contains(t, out, "Permanently delete data dir: /data")
}

func TestRenderUninstallBanner_NoPurge(t *testing.T) {
	var buf bytes.Buffer
	plan := Plan{Steps: []Step{{Description: "Remove unit"}}}
	renderUninstallBanner(&buf, plan, UninstallOptions{Purge: false})
	out := buf.String()
	assert.Contains(t, out, "Remove unit")
	assert.NotContains(t, out, "Permanently delete")
}

func TestIsDirWritable_TrueForTempDir(t *testing.T) {
	assert.True(t, isDirWritable(t.TempDir()))
}

func TestIsDirWritable_FalseForMissingDir(t *testing.T) {
	assert.False(t, isDirWritable(filepath.Join(t.TempDir(), "absent")))
}

func TestIsDirWritable_FalseWhenReadOnly(t *testing.T) {
	// On systems where running as root, mode bits won't be respected. Detect
	// and skip — the syscall already handles permission failures correctly.
	if os.Geteuid() == 0 {
		t.Skip("read-only test requires non-root user")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	assert.False(t, isDirWritable(dir))
}

func TestPlanKind_String(t *testing.T) {
	cases := map[PlanKind]string{
		PlanNoop:      "noop",
		PlanInstall:   "install",
		PlanUpdate:    "update",
		PlanConflict:  "conflict",
		PlanUninstall: "uninstall",
		PlanKind(99):  "unknown",
	}
	for kind, expected := range cases {
		assert.Equal(t, expected, kind.String())
	}
}

func TestNewRunner_RealCommand(t *testing.T) {
	r := NewRunner()
	require.NotNil(t, r)
	// /bin/true is universally available on Linux/macOS and returns 0.
	stdout, stderr, err := r.Run(testCtx(t), "true")
	assert.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestNewRunner_FailingCommand(t *testing.T) {
	r := NewRunner()
	_, _, err := r.Run(testCtx(t), "false")
	assert.Error(t, err)
}

func TestNewRunner_Missing(t *testing.T) {
	r := NewRunner()
	_, _, err := r.Run(testCtx(t), "no-such-binary-7f9a8b6c")
	assert.Error(t, err)
}

func TestNewOSFileSystem_WriteReadStatRemove(t *testing.T) {
	fs := NewOSFileSystem()
	require.NotNil(t, fs)

	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.txt")
	require.NoError(t, fs.WriteFile(path, []byte("hello"), 0o600))

	got, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), got)

	info, err := fs.Stat(path)
	require.NoError(t, err)
	assert.False(t, info.IsDir())

	require.NoError(t, fs.MkdirAll(filepath.Join(dir, "another"), 0o755))
	require.NoError(t, fs.Remove(path))

	_, err = fs.Stat(path)
	assert.Error(t, err)
}

func TestDefaultDeps_RealEnvironment(t *testing.T) {
	d, err := DefaultDeps(io.Discard, io.Discard, nil, true)
	require.NoError(t, err)
	assert.NotEmpty(t, d.Home, "home directory present")
	assert.NotEmpty(t, d.User, "user present")
	assert.NotEmpty(t, d.Fingerprint, "fingerprint generated")
	assert.True(t, d.AutoOK)
	assert.NotNil(t, d.FS)
	assert.NotNil(t, d.Cmd)
	assert.NotNil(t, d.Prompter)
}

func TestFakeRunner_LogAndExhaustion(t *testing.T) {
	r := NewFakeRunner()
	r.Expect("systemctl", []string{"is-active"}, []byte("active\n"), nil, nil)

	stdout, _, err := r.Run(testCtx(t), "systemctl", "is-active")
	require.NoError(t, err)
	assert.Equal(t, "active\n", string(stdout))

	// One expectation consumed → Remaining == 0; another call errors.
	assert.Equal(t, 0, r.Remaining())
	_, _, err = r.Run(testCtx(t), "systemctl", "is-active")
	assert.Error(t, err)

	log := r.Log()
	assert.Len(t, log, 2)
	assert.Equal(t, "systemctl", log[0].Name)
}

func TestArgsEqual(t *testing.T) {
	assert.True(t, argsEqual(nil, nil))
	assert.True(t, argsEqual([]string{}, []string{}))
	assert.True(t, argsEqual([]string{"a", "b"}, []string{"a", "b"}))
	assert.False(t, argsEqual([]string{"a"}, []string{"a", "b"}))
	assert.False(t, argsEqual([]string{"a", "b"}, []string{"a", "c"}))
}
