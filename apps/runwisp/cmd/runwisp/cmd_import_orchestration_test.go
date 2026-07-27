// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/importer"
)

// tempFile writes content to a new file in t.TempDir() and returns its path.
func tempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// openTempFile creates a regular file holding content and returns it positioned
// at the start. A regular file is never a TTY, so it stands in for piped stdin.
func openTempFile(t *testing.T, content string) *os.File {
	t.Helper()
	path := tempFile(t, "stdin", content)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestRunImportCronStdout(t *testing.T) {
	src := tempFile(t, "crontab", "# nightly backup\n0 0 * * * /usr/bin/backup.sh\n")
	var stdout, stderr bytes.Buffer
	err := runImportCron(&stdout, &stderr, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{}, importOpts{})
	if err != nil {
		t.Fatalf("runImportCron: %v", err)
	}
	if !strings.Contains(stdout.String(), "[tasks.backup]") {
		t.Errorf("stdout missing task block:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Imported crontab") {
		t.Errorf("stderr missing summary:\n%s", stderr.String())
	}
}

func TestRunImportCronOpenError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runImportCron(&stdout, &stderr, openTempFile(t, ""),
		filepath.Join(t.TempDir(), "nope.crontab"), importer.CronOptions{}, Flags{}, importOpts{})
	if err == nil {
		t.Fatal("expected an error opening a missing file")
	}
	if _, ok := isUserFacing(err); !ok {
		t.Errorf("expected a userFacingError, got %T: %v", err, err)
	}
}

func TestRunImportCronWriteAndForce(t *testing.T) {
	src := tempFile(t, "crontab", "0 0 * * * /usr/bin/backup.sh\n")
	target := filepath.Join(t.TempDir(), "out.toml")
	opts := importOpts{output: target}

	var stdout, stderr bytes.Buffer
	if err := runImportCron(&stdout, &stderr, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{}, opts); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "[tasks.backup]") {
		t.Errorf("target file missing task block:\n%s", got)
	}
	if !strings.Contains(stderr.String(), "Wrote "+target) {
		t.Errorf("summary should report the written path:\n%s", stderr.String())
	}

	// Re-writing an existing target without --force on a non-terminal must error
	// rather than clobber silently.
	stdout.Reset()
	stderr.Reset()
	err = runImportCron(&stdout, &stderr, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{}, opts)
	if err == nil {
		t.Fatal("expected an error overwriting without --force")
	}
	if u, _ := isUserFacing(err); u == nil || !strings.Contains(u.title, "already exists") {
		t.Errorf("expected an 'already exists' userFacingError, got %v", err)
	}

	// With --force it overwrites.
	opts.force = true
	if err := runImportCron(&stdout, &stderr, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{}, opts); err != nil {
		t.Fatalf("forced overwrite: %v", err)
	}
}

func TestRunImportCronWriteToConfigPath(t *testing.T) {
	src := tempFile(t, "crontab", "0 0 * * * /usr/bin/backup.sh\n")
	cfg := filepath.Join(t.TempDir(), "runwisp.toml")
	var stdout, stderr bytes.Buffer
	err := runImportCron(&stdout, &stderr, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{CfgFile: cfg}, importOpts{write: true})
	if err != nil {
		t.Fatalf("runImportCron --write: %v", err)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Errorf("--write should have written the config path: %v", err)
	}
}

func TestRunImportCronInvalidGeneratesNote(t *testing.T) {
	// An unparseable cron expression still imports, but the generated config
	// doesn't validate — the summary must say so instead of claiming success.
	src := tempFile(t, "crontab", "99 99 * * * /usr/bin/broken.sh\n")
	var stdout, stderr bytes.Buffer
	if err := runImportCron(&stdout, &stderr, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{}, importOpts{}); err != nil {
		t.Fatalf("runImportCron: %v", err)
	}
	if !strings.Contains(stderr.String(), "didn't validate yet") {
		t.Errorf("summary should flag the validation failure:\n%s", stderr.String())
	}
}

func TestRunImportCronQuiet(t *testing.T) {
	src := tempFile(t, "crontab", "0 0 * * * /usr/bin/backup.sh\n")
	var stdout, stderr bytes.Buffer
	if err := runImportCron(&stdout, &stderr, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{}, importOpts{quiet: true}); err != nil {
		t.Fatalf("runImportCron: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("quiet mode should suppress the summary, got:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[tasks.backup]") {
		t.Errorf("stdout should still carry the TOML:\n%s", stdout.String())
	}
}

func TestRunImportSupervisordFile(t *testing.T) {
	conf := tempFile(t, "supervisord.conf",
		"[program:web]\ncommand=/usr/bin/web --serve\nautostart=true\n")
	var stdout, stderr bytes.Buffer
	if err := runImportSupervisord(&stdout, &stderr, openTempFile(t, ""),
		[]string{conf}, Flags{}, importOpts{}); err != nil {
		t.Fatalf("runImportSupervisord: %v", err)
	}
	if !strings.Contains(stdout.String(), "[services.web]") {
		t.Errorf("stdout missing service block:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Imported supervisord config") {
		t.Errorf("stderr missing summary:\n%s", stderr.String())
	}
}

func TestRunImportSupervisordStdin(t *testing.T) {
	stdin := openTempFile(t, "[program:web]\ncommand=/usr/bin/web\n")
	var stdout, stderr bytes.Buffer
	if err := runImportSupervisord(&stdout, &stderr, stdin, nil, Flags{}, importOpts{}); err != nil {
		t.Fatalf("runImportSupervisord (stdin): %v", err)
	}
	if !strings.Contains(stdout.String(), "[services.web]") {
		t.Errorf("stdout missing service block:\n%s", stdout.String())
	}
}

func TestRunImportSupervisordDashStdin(t *testing.T) {
	stdin := openTempFile(t, "[program:web]\ncommand=/usr/bin/web\n")
	var stdout, stderr bytes.Buffer
	if err := runImportSupervisord(&stdout, &stderr, stdin, []string{"-"}, Flags{}, importOpts{}); err != nil {
		t.Fatalf("runImportSupervisord (-): %v", err)
	}
	if !strings.Contains(stdout.String(), "[services.web]") {
		t.Errorf("stdout missing service block:\n%s", stdout.String())
	}
}

func TestRunImportSupervisordMixStdinAndFiles(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runImportSupervisord(&stdout, &stderr, openTempFile(t, ""),
		[]string{"-", "other.conf"}, Flags{}, importOpts{})
	if err == nil {
		t.Fatal("mixing - with file paths should error")
	}
	if u, _ := isUserFacing(err); u == nil || !strings.Contains(u.title, "can't mix") {
		t.Errorf("expected a 'can't mix' userFacingError, got %v", err)
	}
}

func TestRunImportSupervisordReadError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runImportSupervisord(&stdout, &stderr, openTempFile(t, ""),
		[]string{filepath.Join(t.TempDir(), "missing.conf")}, Flags{}, importOpts{})
	if err == nil {
		t.Fatal("expected an error reading a missing supervisord file")
	}
	if u, _ := isUserFacing(err); u == nil || !strings.Contains(u.title, "failed to read") {
		t.Errorf("expected a 'failed to read' userFacingError, got %v", err)
	}
}

func TestOpenImportSourceStdin(t *testing.T) {
	stdin := openTempFile(t, "hello")
	r, closeFn, err := openImportSource("-", stdin)
	if err != nil {
		t.Fatalf("openImportSource(-): %v", err)
	}
	defer closeFn()
	if r != stdin {
		t.Error("openImportSource(-) should return stdin verbatim")
	}
}

func TestValidateGeneratedTOML(t *testing.T) {
	if err := validateGeneratedTOML("this is not = valid toml ["); err == nil {
		t.Error("expected invalid TOML to fail validation")
	}
	good := "[tasks.backup]\ncron = \"0 0 * * *\"\nrun = \"/usr/bin/backup.sh\"\n"
	if err := validateGeneratedTOML(good); err != nil {
		t.Errorf("expected valid TOML to pass, got %v", err)
	}
}

func TestPluralizeCounts(t *testing.T) {
	cases := []struct {
		tasks, services int
		want            string
	}{
		{0, 0, "nothing"},
		{1, 0, "1 task"},
		{2, 0, "2 tasks"},
		{0, 1, "1 service"},
		{2, 3, "2 tasks, 3 services"},
	}
	for _, c := range cases {
		if got := pluralizeCounts(c.tasks, c.services); got != c.want {
			t.Errorf("pluralizeCounts(%d, %d) = %q, want %q", c.tasks, c.services, got, c.want)
		}
	}
}
