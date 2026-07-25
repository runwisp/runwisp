// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"flag"
	"fmt"
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
	name     string
	file     string
	opts     CronOptions
	validate bool // false when the fixture deliberately emits a TODO that won't validate
	// expectNotes is the EXACT set of note kinds the fixture must produce — not a
	// subset. An import that starts saying something new about a fixture is a
	// change to what the operator reads, so it should have to be written down
	// here. Kinds rather than prose, so rewording a message isn't a test edit.
	expectNotes []NoteKind
}{
	// A per-user `crontab -l` dump: descriptors, wrappers, comments, env, TZ.
	{name: "user", file: "testdata/cron/user.crontab", validate: true},
	// A system crontab (/etc/crontab) with the extra user column, including a
	// descriptor line that carries one.
	{name: "system", file: "testdata/cron/system.crontab", opts: CronOptions{System: true}, validate: true},
	// Notes-only edge cases (MAILTO, relative SHELL, both '%' forms, dedupe, an
	// unparseable line) that must surface but still leave a valid config.
	{name: "messy", file: "testdata/cron/messy.crontab", validate: true, expectNotes: []NoteKind{
		NoteMailto, NoteShellNotAbsolute, NotePercentStdin, NotePercentTranslated,
		NoteLineUnparseable, NoteRenamedCollision,
	}},
	// An invalid cron expression becomes a `# TODO` and a non-loadable config —
	// the import still succeeds and tells the operator exactly what to fix.
	{name: "invalid", file: "testdata/cron/invalid.crontab", validate: false, expectNotes: []NoteKind{
		NoteCronUnparseable,
	}},
	// A CRON_TZ RunWisp can't load: the expression is fine, the zone isn't, and
	// the config genuinely doesn't load — so the row must not read clean.
	{name: "badtz", file: "testdata/cron/badtz.crontab", validate: false, expectNotes: []NoteKind{
		NoteTimezoneInvalid,
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
			assertReportAccountsForTOML(t, res)
			checkGolden(t, goldenPath(tc.file), res.TOML(), tc.validate)
			checkReportGolden(t, reportGoldenPath(tc.file), res)
			assertNotes(t, res, tc.expectNotes)
		})
	}
}

// supervisordGoldenCases pairs a supervisord config fixture (read as a single
// file via the reader path) with its expected TOML.
var supervisordGoldenCases = []struct {
	name        string
	file        string
	expectNotes []NoteKind
}{
	// Two services with the full knob set, plus skipped daemon sections and a
	// numprocs that fans out into instances.
	{name: "full", file: "testdata/supervisord/full.conf", expectNotes: []NoteKind{
		NoteSectionDaemon, // [supervisord]/[unix_http_server]
		NoteLogsDropped,   // dropped log files
		NoteInstances,     // numprocs=3
	}},
	// A group, an autorestart=unexpected service, a run-once task, and an
	// eventlistener RunWisp can't represent.
	{name: "mixed", file: "testdata/supervisord/mixed.conf", expectNotes: []NoteKind{
		NoteGroup,                 // [group:site]
		NoteAutorestartUnexpected, // autorestart=unexpected
		NoteRunOnce,               // autorestart=false → batch
		NoteSectionUnsupported,    // [eventlistener:memmon]
	}},
	// A config that exercises the quiet drops: an unmapped key, a value RunWisp
	// can't read, and a purely cosmetic key that must stay silent.
	{name: "lossy", file: "testdata/supervisord/lossy.conf", expectNotes: []NoteKind{
		NoteKeysUnsupported, NoteKeyUnreadable,
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
			assertReportAccountsForTOML(t, res)
			checkGolden(t, goldenPath(tc.file), res.TOML(), true)
			checkReportGolden(t, reportGoldenPath(tc.file), res)
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
	assertReportAccountsForTOML(t, res)
	checkGolden(t, "testdata/supervisord/include/expected.golden.toml", res.TOML(), true)
	checkReportGolden(t, "testdata/supervisord/include/expected.golden.report.txt", res)
}

// assertReportAccountsForTOML is this package's central invariant, checked
// against every fixture: the set of tables in the emitted TOML and the set of
// rows that claim to have emitted something are the same set.
//
// One direction catches the bug the report was split out to fix — a table in the
// file that no row accounts for, i.e. config appearing from nowhere. The other
// catches its mirror image, a row claiming a name that never made it into the
// file, which would have the operator looking for a task the daemon won't have.
// itemRef.emit is the only path to either, so this is really a check that no
// future code path routes around it.
func assertReportAccountsForTOML(t *testing.T, res *Result) {
	t.Helper()
	inTOML := map[string]bool{}
	for _, name := range res.tableNames() {
		if inTOML[name] {
			t.Errorf("the TOML defines %q twice", name)
		}
		inTOML[name] = true
	}
	inReport := map[string]bool{}
	for _, it := range res.Items() {
		if it.Name == "" {
			continue // a row that emitted nothing, which is the point of having rows
		}
		inReport[it.Name] = true
		if !inTOML[it.Name] {
			t.Errorf("the report lists %q but the TOML defines no table for it\n--- report ---\n%s",
				it.Name, formatReport(res))
		}
	}
	for name := range inTOML {
		if !inReport[name] {
			t.Errorf("the TOML defines %q but no report row accounts for it — a silently "+
				"imported job is exactly what this report exists to prevent\n--- report ---\n%s",
				name, formatReport(res))
		}
	}
}

// assertNotes checks that the fixture produced EXACTLY the expected set of note
// kinds — nothing missing and nothing new. The notes are the other half of an
// import's output, the human-facing "here's what needs your eyes", so a fixture
// that starts saying something else should fail until someone says it's intended.
func assertNotes(t *testing.T, res *Result, want []NoteKind) {
	t.Helper()
	got := map[NoteKind]bool{}
	for _, n := range res.Notes() {
		got[n.Kind] = true
	}
	for _, it := range res.Items() {
		for _, n := range it.Notes {
			got[n.Kind] = true
		}
	}
	wanted := map[NoteKind]bool{}
	for _, k := range want {
		wanted[k] = true
		if !got[k] {
			t.Errorf("expected a %s note\n--- report ---\n%s", k, formatReport(res))
		}
	}
	for k := range got {
		if !wanted[k] {
			t.Errorf("unexpected %s note — add it to expectNotes if intended\n--- report ---\n%s",
				k, formatReport(res))
		}
	}
}

// formatReport dumps the report as data: one line per row with its mark, name,
// schedule, and command, and its notes by slug underneath. It is deliberately
// NOT a rendering — the layout lives in the CLI, and duplicating it here would
// quietly re-implement the thing this package promises not to know about.
func formatReport(res *Result) string {
	var sb strings.Builder
	for _, it := range res.Items() {
		fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\t%s\n", it.Status(), it.Source, it.Name, it.Schedule, it.Run)
		for _, n := range it.Notes {
			fmt.Fprintf(&sb, "\t%s\n", n.Kind)
		}
	}
	if notes := res.Notes(); len(notes) > 0 {
		sb.WriteString("file\n")
		for _, n := range notes {
			fmt.Fprintf(&sb, "\t%s\n", n.Kind)
		}
	}
	return sb.String()
}

// reportGoldenPath maps an input fixture to its report golden, the sibling of
// the TOML golden.
func reportGoldenPath(input string) string {
	ext := filepath.Ext(input)
	return strings.TrimSuffix(input, ext) + ".golden.report.txt"
}

// checkReportGolden pins the report itself, not just the TOML. The two are now
// separate outputs, and the report is the one that accounts for the jobs the
// TOML doesn't mention.
func checkReportGolden(t *testing.T, path string, res *Result) {
	t.Helper()
	checkGolden(t, path, formatReport(res), false)
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

// assertGeneratedConfigDoesNotLoad is the inverse assertion, for the fixtures
// whose whole point is that the operator has something to fix: it proves the
// import flagged a real load failure rather than a hypothetical one.
func assertGeneratedConfigDoesNotLoad(t *testing.T, toml string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatalf("expected this config NOT to load\n--- toml ---\n%s", toml)
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
