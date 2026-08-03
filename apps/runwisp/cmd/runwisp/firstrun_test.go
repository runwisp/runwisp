// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cutover"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain stubs the two host-probing seams the first-run prompt reaches for, so
// no test in this package depends on the machine it runs on: scanForCron touches
// the real filesystem (see its doc comment) and offerFirstRunCutover reaches for
// the real systemd and euid. A CI image with crontabs under /etc/cron.d, or one
// running as root, would otherwise make these tests machine-dependent. Tests
// that exercise either behaviour override the var locally and restore it.
func TestMain(m *testing.M) {
	scanForCron = func(string) (config.CronScan, bool) { return config.CronScan{}, false }
	offerFirstRunCutover = func(Flags, io.Writer, func(string, []string) error) *firstRunCutover { return nil }
	os.Exit(m.Run())
}

func stubCronScan(t *testing.T, scan config.CronScan, ok bool) {
	t.Helper()
	prev := scanForCron
	scanForCron = func(string) (config.CronScan, bool) { return scan, ok }
	t.Cleanup(func() { scanForCron = prev })
}

func TestPromptAndScaffoldAcceptsYes(t *testing.T) {
	cases := []string{"y\n", "Y\n", "yes\n", "YES\n", "\n", "  y  \n"}
	for _, answer := range cases {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runwisp.toml")
			var out bytes.Buffer

			installed, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader(answer), &out)
			require.NoError(t, err)
			assert.False(t, installed)

			assert.FileExists(t, path)
			assert.Contains(t, out.String(), "No runwisp.toml at "+filepath.Dir(path))
			assert.Contains(t, out.String(), "Created "+path)
		})
	}
}

func TestPromptAndScaffoldDeclines(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "N\n", "anything-else\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runwisp.toml")
			var out bytes.Buffer

			_, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader(answer), &out)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "no runwisp.toml at "+filepath.Dir(path))
			assert.NoFileExists(t, path)
		})
	}
}

func TestScaffoldIfMissingNoopWhenFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte("[tasks.x]\nrun = \"true\"\n"), 0644))

	installed, err := scaffoldIfMissing(Flags{CfgFile: path})
	assert.NoError(t, err)
	assert.False(t, installed)
}

func TestPromptAndScaffoldDetectsAdjacentCompose(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web: {image: nginx}\n"), 0644))
	path := filepath.Join(dir, "runwisp.toml")

	var out bytes.Buffer
	_, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("\n"), &out)
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "[compose.")
	assert.Contains(t, string(body), "file = \"./docker-compose.yml\"")
	assert.Contains(t, out.String(), "Detected docker-compose.yml")
}

func TestComposeAliasFromDir(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"simple", "/var/myapp", "myapp"},
		{"hyphenated", "/var/my-app", "my-app"},
		{"sanitized punctuation", "/var/my app!", "my-app"},
		{"root falls back to myapp", "/", "myapp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, composeAliasFromDir(tc.in))
		})
	}
}

func TestScaffoldIfMissing_NoTTYReturnsNilWithoutWritingFile(t *testing.T) {
	// go test invocation has no TTY on stdin, so missing config path
	// short-circuits to nil without creating a file.
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	_, err := scaffoldIfMissing(Flags{CfgFile: path})
	require.NoError(t, err)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file created, got %v", err)
	}
}

func TestScaffoldIfMissing_StatErrorBubblesUp(t *testing.T) {
	// Create a dir, then point scaffoldIfMissing at a path *through* a regular
	// file — that yields a non-ErrNotExist Stat error (ENOTDIR).
	dir := t.TempDir()
	regular := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(regular, []byte("x"), 0600))
	bad := filepath.Join(regular, "subdir", "runwisp.toml")
	if _, err := scaffoldIfMissing(Flags{CfgFile: bad}); err == nil {
		t.Fatal("expected ENOTDIR-style error to bubble up")
	}
}

func TestConfigLocation_DefaultNameReturnsDir(t *testing.T) {
	dir := t.TempDir()
	got := configLocation(filepath.Join(dir, "runwisp.toml"))
	if got != dir {
		t.Fatalf("configLocation default name = %q, want dir %q", got, dir)
	}
}

func TestConfigLocation_CustomNameReturnsAbsPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.toml")
	got := configLocation(path)
	if got != path {
		t.Fatalf("configLocation custom = %q, want %q", got, path)
	}
}

// TestPromptAndScaffoldOffersDetectedCron covers the host that cannot retire cron
// itself (no offer): the jobs are read, and the prompt says out loud that cron
// keeps running them meanwhile. Saying nothing there is what made the original
// prompt misleading.
func TestPromptAndScaffoldOffersDetectedCron(t *testing.T) {
	stubCronScan(t, config.CronScan{
		Globs: []string{"/etc/crontab"},
		Files: []string{"/etc/crontab"},
		Jobs:  3,
		Live:  3,
	}, true)

	path := filepath.Join(t.TempDir(), "runwisp.toml")
	var out bytes.Buffer
	_, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("\n"), &out)
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "include_cron")
	assert.Contains(t, string(body), `"/etc/crontab"`)
	assert.Contains(t, out.String(), "Found 3 cron job(s)")
	assert.Contains(t, out.String(), "cron keeps running them for now")
	assert.Contains(t, out.String(), "sudo runwisp takeover")
	assert.Contains(t, out.String(), "Read them as RunWisp tasks?")
}

func TestPromptAndScaffoldOffersComposeAndCronTogether(t *testing.T) {
	stubCronScan(t, config.CronScan{
		Globs: []string{"/etc/crontab"},
		Files: []string{"/etc/crontab"},
		Jobs:  1,
		Live:  1,
	}, true)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web: {image: nginx}\n"), 0644))
	path := filepath.Join(dir, "runwisp.toml")

	var out bytes.Buffer
	_, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("\n"), &out)
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "[compose.")
	assert.Contains(t, string(body), "include_cron")
	assert.Contains(t, out.String(), "imports the compose services and reads these live")
}

func TestPromptAndScaffoldFallsBackWhenCronIsBlocked(t *testing.T) {
	stubCronScan(t, config.CronScan{
		Blocked: []string{"/etc/cron.d/backup: refused to trust world-writable file"},
	}, false)

	path := filepath.Join(t.TempDir(), "runwisp.toml")
	var out bytes.Buffer
	_, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("\n"), &out)
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "include_cron")
	assert.Contains(t, out.String(), "Found a cron source RunWisp can't read yet: /etc/cron.d/backup")
	assert.Contains(t, out.String(), "Create a starter with one example task?")
}

// cronScanWithJobs is a scan that makes the first run offer the cron path.
var cronScanWithJobs = config.CronScan{
	Globs: []string{"/etc/crontab"},
	Files: []string{"/etc/crontab"},
	Jobs:  2,
	Live:  2,
}

// stubFirstRunOffer replaces the host probe with a cutover over fakes, keeping the
// WriteConfig the caller threaded in — so the scaffold the plan writes is the real
// one, compose detection and all.
func stubFirstRunOffer(t *testing.T, inst *fakeTakeoverInstaller) {
	t.Helper()
	prev := offerFirstRunCutover
	t.Cleanup(func() { offerFirstRunCutover = prev })

	offerFirstRunCutover = func(f Flags, _ io.Writer, writeConfig func(string, []string) error) *firstRunCutover {
		dir := filepath.Dir(f.CfgFile)
		crontabs := filepath.Join(dir, "crontabs")
		require.NoError(t, os.MkdirAll(crontabs, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(crontabs, "backup"),
			[]byte("17 3 * * * /usr/bin/backup\n"), 0o644))

		c := cutover.New(cutover.Deps{
			Installer: inst,
			Prompter:  &autostart.ScriptedPrompter{},
			Opts: autostart.InstallOptions{
				Binary: "/usr/local/bin/runwisp", Config: f.CfgFile,
				DataDir: dir, Host: "127.0.0.1", Port: 9477, System: true,
			},
			GOOS: "linux",
			Scan: func(_ []string, cfgPath string) config.CronScan {
				return config.ScanCronSources([]string{filepath.Join(crontabs, "*")}, cfgPath)
			},
			Trusted:       func(string) error { return nil },
			WriteConfig:   writeConfig,
			DaemonRunning: func() bool { return false },
		}, cutover.Options{})

		plan, err := c.Compute(context.Background())
		require.NoError(t, err)
		require.False(t, plan.Blocked(), "%v", plan.Blockers)
		return &firstRunCutover{c: c, plan: plan}
	}
}

// TestPromptAndScaffold_CutoverAcceptedScaffoldsThenInstalls pins the ordering the
// whole flow rests on: the installer's preflight refuses a unit pointing at a
// config that does not exist, and masking cron before RunWisp can read the
// crontabs would stop every job on the box.
func TestPromptAndScaffold_CutoverAcceptedScaffoldsThenInstalls(t *testing.T) {
	stubCronScan(t, cronScanWithJobs, true)
	inst := &fakeTakeoverInstaller{cronUnit: "cron.service", cronActive: true, t: t}
	stubFirstRunOffer(t, inst)

	path := filepath.Join(t.TempDir(), "runwisp.toml")
	var out bytes.Buffer
	installed, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("\n"), &out)
	require.NoError(t, err)

	assert.True(t, installed, "the caller must attach to the service, not spawn its own daemon")
	assert.FileExists(t, path)
	assert.Equal(t, 1, inst.installs)
	assert.True(t, inst.configAtInstall, "the config must exist by the time Install runs")
}

// One question, every consequence — including boot persistence. Masking cron in
// favour of a daemon that dies with the operator's terminal would trade
// double-firing for nothing firing at all.
func TestPromptAndScaffold_CutoverPromptNamesAllThreeEffects(t *testing.T) {
	stubCronScan(t, cronScanWithJobs, true)
	inst := &fakeTakeoverInstaller{cronUnit: "cron.service", cronActive: true, t: t}
	stubFirstRunOffer(t, inst)

	path := filepath.Join(t.TempDir(), "runwisp.toml")
	var out bytes.Buffer
	_, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("\n"), &out)
	require.NoError(t, err)

	text := out.String()
	assert.Contains(t, text, "crontab -e still works")
	assert.Contains(t, text, "install itself as a system service")
	assert.Contains(t, text, "stop and mask cron.service")
	assert.Contains(t, text, "Take over from cron? [Y/n]")
}

// Declining the offer declines the whole first run: no config, no unit, no mask.
func TestPromptAndScaffold_CutoverDeclinedWritesNothing(t *testing.T) {
	stubCronScan(t, cronScanWithJobs, true)
	inst := &fakeTakeoverInstaller{cronUnit: "cron.service", cronActive: true, t: t}
	stubFirstRunOffer(t, inst)

	path := filepath.Join(t.TempDir(), "runwisp.toml")
	var out bytes.Buffer
	installed, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("n\n"), &out)

	require.Error(t, err)
	assert.False(t, installed)
	assert.NoFileExists(t, path)
	assert.Zero(t, inst.installs)
}

// The cutover's config step must write the scaffold the first run would have
// written on its own — a directory with both crontabs and a compose file gets
// both, off the one question already on screen.
func TestPromptAndScaffold_CutoverScaffoldStillImportsCompose(t *testing.T) {
	stubCronScan(t, cronScanWithJobs, true)
	inst := &fakeTakeoverInstaller{cronUnit: "cron.service", cronActive: true, t: t}
	stubFirstRunOffer(t, inst)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web: {image: nginx}\n"), 0644))
	path := filepath.Join(dir, "runwisp.toml")

	var out bytes.Buffer
	_, err := promptAndScaffold(Flags{CfgFile: path}, strings.NewReader("\n"), &out)
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "[compose.")
	assert.Contains(t, string(body), "include_cron")
}
