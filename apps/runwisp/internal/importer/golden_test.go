// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
)

// updateGolden regenerates the *.golden.toml fixtures from the current parser
// output instead of comparing against them. Run with:
//
//	go test ./internal/importer -run Golden -update
//
// then eyeball the diff before committing — the golden files are the contract
// the import command promises its users.
var updateGolden = flag.Bool("update", false, "rewrite the .golden.toml fixtures")

// cronGoldenCases pairs a real crontab fixture with the parse options it should
// be read under. Each case's expected TOML lives next to the input with the
// .crontab suffix replaced by .golden.toml.
var cronGoldenCases = []struct {
	name        string
	file        string
	opts        CronOptions
	validate    bool     // false when the fixture deliberately emits a TODO that won't validate
	expectNotes []string // substrings that must each appear in some note
}{
	// A per-user `crontab -l` dump: descriptors, wrappers, comments, env, TZ.
	{name: "user", file: "testdata/cron/user.crontab", validate: true},
	// A system crontab (/etc/crontab) with the extra user column.
	{name: "system", file: "testdata/cron/system.crontab", opts: CronOptions{System: true}, validate: true},
	// Notes-only edge cases (MAILTO, relative SHELL, '%' command, dedupe, an
	// unparseable line) that must surface but still leave a valid config.
	{name: "messy", file: "testdata/cron/messy.crontab", validate: true, expectNotes: []string{
		"MAILTO", "absolute shell path", "contains '%'", "couldn't parse crontab line",
	}},
	// An invalid cron expression becomes a `# TODO` and a non-loadable config —
	// the import still succeeds and tells the operator exactly what to fix.
	{name: "invalid", file: "testdata/cron/invalid.crontab", validate: false, expectNotes: []string{
		"didn't parse",
	}},
}

func TestCronGolden(t *testing.T) {
	for _, tc := range cronGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			in, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			res, err := ParseCrontab(strings.NewReader(string(in)), tc.opts)
			if err != nil {
				t.Fatalf("ParseCrontab: %v", err)
			}
			checkGolden(t, goldenPath(tc.file), res.TOML(), tc.validate)
			assertNotes(t, res, tc.expectNotes)
		})
	}
}

// supervisordGoldenCases pairs a supervisord config fixture (read as a single
// file via the reader path) with its expected TOML.
var supervisordGoldenCases = []struct {
	name        string
	file        string
	expectNotes []string
}{
	// Two services with the full knob set, plus skipped daemon sections and a
	// numprocs that fans out into instances.
	{name: "full", file: "testdata/supervisord/full.conf", expectNotes: []string{
		"daemon config with no RunWisp equivalent", // [supervisord]/[unix_http_server]
		"captures stdout and stderr",               // dropped log files
		"RUNWISP_INSTANCE_INDEX",                   // numprocs=3
	}},
	// A group, an autorestart=unexpected service, a run-once task, and an
	// eventlistener RunWisp can't represent.
	{name: "mixed", file: "testdata/supervisord/mixed.conf", expectNotes: []string{
		"no program groups", // [group:site]
		"always-on service", // autorestart=unexpected
		"run-once task",     // autorestart=false → batch
		"isn't supported",   // [eventlistener:memmon]
	}},
}

func TestSupervisordGolden(t *testing.T) {
	for _, tc := range supervisordGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open(tc.file)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer f.Close()
			res, err := ParseSupervisordReader(f, SupervisordOptions{})
			if err != nil {
				t.Fatalf("ParseSupervisordReader: %v", err)
			}
			checkGolden(t, goldenPath(tc.file), res.TOML(), true)
			assertNotes(t, res, tc.expectNotes)
		})
	}
}

// TestSupervisordIncludeGolden exercises the multi-file path: a top-level
// config whose [include] pulls in conf.d/*.conf, resolved relative to the file
// on disk. This can only be tested with real files, which is the whole point of
// fixture-based tests.
func TestSupervisordIncludeGolden(t *testing.T) {
	res, err := ParseSupervisordFiles([]string{"testdata/supervisord/include/supervisord.conf"}, SupervisordOptions{})
	if err != nil {
		t.Fatalf("ParseSupervisordFiles: %v", err)
	}
	checkGolden(t, "testdata/supervisord/include/expected.golden.toml", res.TOML(), true)
}

// assertNotes checks that each wanted substring appears in at least one of the
// result's notes. The notes are the other half of an import's output — the
// human-facing "here's what needs your eyes" — so the fixtures pin them too.
func assertNotes(t *testing.T, res *Result, want []string) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, n := range res.Notes {
			if strings.Contains(n.Message, w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a note containing %q\n--- notes ---\n%s", w, formatNotes(res.Notes))
		}
	}
}

func formatNotes(notes []Note) string {
	var sb strings.Builder
	for _, n := range notes {
		level := "info"
		if n.Level == LevelAttention {
			level = "attn"
		}
		sb.WriteString("  [" + level + "] ")
		if n.Scope != "" {
			sb.WriteString(n.Scope + ": ")
		}
		sb.WriteString(n.Message + "\n")
	}
	return sb.String()
}

// goldenPath maps an input fixture path to its sibling golden file by swapping
// the extension for .golden.toml.
func goldenPath(input string) string {
	ext := filepath.Ext(input)
	return strings.TrimSuffix(input, ext) + ".golden.toml"
}

// checkGolden compares got against the golden file at path, or rewrites it when
// -update is set. Every golden output is also round-tripped through
// config.Load: the generated TOML is the import command's promise, and an
// import that doesn't even parse would be a silent failure.
func checkGolden(t *testing.T, path, got string, validate bool) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create it): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s (run with -update to refresh)\n--- got ---\n%s\n--- want ---\n%s",
			path, got, want)
	}
	if validate {
		assertGeneratedConfigLoads(t, got)
	}
}

// assertGeneratedConfigLoads proves the emitted TOML parses and validates,
// mirroring the config.Load round-trip the CLI performs after an import. The
// fixtures are curated to be clean (no unresolved TODOs), so a load failure is
// a real regression in the importer.
func assertGeneratedConfigLoads(t *testing.T, toml string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("generated TOML failed to validate via config.Load: %v\n--- toml ---\n%s", err, toml)
	}
}
