// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// This file is package importer_test, not package importer, and that is
// load-bearing rather than stylistic: internal/config imports internal/importer
// (the cron loader reads crontabs through it), so an in-package test that
// imported config would make `go test ./internal/importer` fail to build with
// "import cycle not allowed in test". Every config.Load assertion about the
// importer's output lives here.
package importer_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/importer"
	"github.com/runwisp/runwisp/internal/model"
)

// goldensThatMustNotLoad names the golden files whose whole point is that the
// operator has something to fix. Everything else under testdata must load.
//
// Stated as an exception list rather than an opt-in `validate: true` flag,
// because the flag made the check silently skippable: a fixture added with
// validate off was simply never round-tripped, and "the emitted TOML loads" is
// the import command's central promise. Inverted, a new fixture is checked by
// default and a genuinely-unloadable one has to be written down here.
var goldensThatMustNotLoad = map[string]string{
	"testdata/cron/invalid.golden.toml": "an unparseable cron expression becomes a # TODO",
	"testdata/cron/badtz.golden.toml":   "CRON_TZ names a zone RunWisp cannot load",
}

// TestGoldenTOMLLoadBehaviour round-trips every committed golden through
// config.Load — the same round-trip the CLI performs after an import.
//
// It reads the golden files rather than freshly-generated output on purpose: the
// golden file is what a reviewer approved and what the command promises, so it
// is the artifact whose loadability matters.
func TestGoldenTOMLLoadBehaviour(t *testing.T) {
	goldens := findGoldenTOML(t)
	if len(goldens) == 0 {
		t.Fatal("no *.golden.toml fixtures found — this test would pass by vacuum")
	}
	for _, path := range goldens {
		t.Run(path, func(t *testing.T) {
			toml, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			reason, mustFail := goldensThatMustNotLoad[path]
			if mustFail {
				assertConfigDoesNotLoad(t, string(toml), reason)
				return
			}
			assertConfigLoads(t, string(toml))
		})
	}
}

// findGoldenTOML walks testdata for *.golden.toml so a new fixture is covered
// without anyone remembering to list it.
func findGoldenTOML(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir("testdata", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".golden.toml") {
			out = append(out, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk testdata: %v", err)
	}
	return out
}

// TestExpectedFailuresStillExist guards the exception list against rot: a golden
// that gets renamed or fixed would otherwise leave a stale entry behind, and the
// next fixture to land under that name would be exempted from the load check by
// accident.
func TestExpectedFailuresStillExist(t *testing.T) {
	found := map[string]bool{}
	for _, path := range findGoldenTOML(t) {
		found[path] = true
	}
	for path := range goldensThatMustNotLoad {
		if !found[path] {
			t.Errorf("goldensThatMustNotLoad names %q, which no longer exists — drop the entry", path)
		}
	}
}

// assertConfigLoads writes the TOML where config.Load can read it and requires
// it to parse and validate.
func assertConfigLoads(t *testing.T, toml string) {
	t.Helper()
	if _, err := loadTOML(t, toml); err != nil {
		t.Fatalf("generated TOML failed to validate via config.Load: %v\n--- toml ---\n%s", err, toml)
	}
}

// assertConfigDoesNotLoad is the inverse: it proves the import flagged a real
// load failure rather than a hypothetical one. reason is echoed on failure so a
// golden that starts loading says why it was ever expected not to.
func assertConfigDoesNotLoad(t *testing.T, toml, reason string) {
	t.Helper()
	if _, err := loadTOML(t, toml); err == nil {
		t.Fatalf("expected this config NOT to load (%s)\n--- toml ---\n%s", reason, toml)
	}
}

func loadTOML(t *testing.T, toml string) (*config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return config.Load(path)
}

// TestCronSubstitutionSyntaxSurvivesTheRoundTrip is the other half of the
// escaping fix, and the half only this package can assert: it proves the escape
// the importer writes is the escape config.Load unescapes, so a `${...}` in a
// crontab arrives at the daemon as the same bytes cron would have handed the
// shell.
//
// The variables are deliberately *set* to a canary. Leaving them unset would
// make an unescaped `${DB}` fail the load, which the test would notice — but it
// would notice for the wrong reason, and it would say nothing about the far worse
// case where the variable happens to exist in whatever shell launched the daemon
// and the value silently changes.
func TestCronSubstitutionSyntaxSurvivesTheRoundTrip(t *testing.T) {
	t.Setenv("DB", "LEAKED")
	t.Setenv("BAR", "LEAKED")
	t.Setenv("DEST", "LEAKED")

	const in = "FOO=${BAR}\n# nightly ${DB} dump\n0 3 * * * /bin/dump --to ${DEST}\n"
	res, err := importer.ParseCrontab(strings.NewReader(in), importer.CronOptions{})
	if err != nil {
		t.Fatalf("ParseCrontab: %v", err)
	}
	cfg, err := loadTOML(t, res.TOML())
	if err != nil {
		t.Fatalf("load: %v\n--- toml ---\n%s", err, res.TOML())
	}
	if len(cfg.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(cfg.Tasks))
	}
	task := cfg.Tasks[0]
	for _, tc := range []struct{ field, got, want string }{
		{"description", task.Description, "nightly ${DB} dump"},
		{"env[FOO]", task.Env["FOO"], "${BAR}"},
		{"run", task.Run, "/bin/dump --to ${DEST}"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q — cron does no ${} substitution, so neither may the import",
				tc.field, tc.got, tc.want)
		}
	}
}

// cronFixtures lists every crontab fixture with the options it should be read
// under, so a new fixture is covered by TestLiveTOMLAlwaysLoads without anyone
// remembering to add it. Discovered rather than enumerated, with the
// system-format ones named because that fact isn't in the filename.
func cronFixtures(t *testing.T) map[string]importer.CronOptions {
	t.Helper()
	system := map[string]bool{"testdata/cron/system.crontab": true}
	paths, err := filepath.Glob("testdata/cron/*.crontab")
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no crontab fixtures found — this test would pass by vacuum")
	}
	out := make(map[string]importer.CronOptions, len(paths))
	for _, p := range paths {
		p = filepath.ToSlash(p)
		out[p] = importer.CronOptions{System: system[p]}
	}
	return out
}

// TestLiveTOMLAlwaysLoads is the guard the daemon's boot depends on. `include_cron`
// renders LiveTOML and hands it to the config loader, so a crontab that produces
// TOML the loader rejects doesn't degrade one task — it takes down the whole
// reload, including every task the file has nothing to do with.
//
// It runs over *every* fixture, including `invalid` and `badtz` whose full TOML
// deliberately does not load: filtering out the jobs that can't run is exactly
// what has to make the difference.
func TestLiveTOMLAlwaysLoads(t *testing.T) {
	for path, opts := range cronFixtures(t) {
		t.Run(path, func(t *testing.T) {
			in, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			res, err := importer.ParseCrontab(strings.NewReader(string(in)), opts)
			if err != nil {
				t.Fatalf("ParseCrontab: %v", err)
			}
			live := res.LiveTOML()
			if _, err := loadTOML(t, live); err != nil {
				t.Fatalf("LiveTOML does not load: %v\n--- live toml ---\n%s\n--- skipped ---\n%+v",
					err, live, res.SkippedLive())
			}
		})
	}
}

// TestImportedJobsKeepCrondFiringSemantics asserts the two firing policies where
// RunWisp's defaults differ from crond's, on every fixture and after a real load.
//
// Asserted post-load rather than against the emitted text on purpose: what matters
// is the policy the scheduler ends up with, and that is a function of both what the
// importer writes and what applyDefaults fills in. A test that only grepped the
// TOML would keep passing if `catch_up = "skip"` stopped surviving the round trip,
// and would say nothing if RunWisp's own default changed underneath it.
func TestImportedJobsKeepCrondFiringSemantics(t *testing.T) {
	for path, opts := range cronFixtures(t) {
		t.Run(path, func(t *testing.T) {
			in, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			res, err := importer.ParseCrontab(strings.NewReader(string(in)), opts)
			if err != nil {
				t.Fatalf("ParseCrontab: %v", err)
			}
			cfg, err := loadTOML(t, res.LiveTOML())
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if len(cfg.Tasks) == 0 {
				t.Skip("fixture emits no live tasks")
			}
			for _, task := range cfg.Tasks {
				// crond has no missed-tick concept, so a restart must not re-fire
				// yesterday's ticks. The gap is still recorded either way.
				//
				// Scheduled tasks only: a @reboot job has no ticks to miss, so the key
				// is deliberately not emitted for one and it keeps RunWisp's default.
				if task.Cron != "" && task.CatchUp != model.MissedRunSkip {
					t.Errorf("task %q: catch_up = %q, want %q — a daemon restart would re-fire missed ticks crond dropped",
						task.Name, task.CatchUp, model.MissedRunSkip)
				}
				// Deliberately queue rather than crond's unbounded overlap, but it
				// has to be the policy we chose and not one inherited by accident.
				if task.OnOverlap != model.PolicyQueue {
					t.Errorf("task %q: on_overlap = %q, want %q", task.Name, task.OnOverlap, model.PolicyQueue)
				}
			}
		})
	}
}

// TestLiveTOMLOfAWhollyUnusableCrontabIsStillValid is the degenerate case: a file
// where nothing survives the filter must render an empty-but-loadable config, not
// a syntax error and not a config with a dangling table.
func TestLiveTOMLOfAWhollyUnusableCrontabIsStillValid(t *testing.T) {
	res, err := importer.ParseCrontab(strings.NewReader("99 99 * * * /bin/bad\nnot-a-cron-line\n"),
		importer.CronOptions{})
	if err != nil {
		t.Fatalf("ParseCrontab: %v", err)
	}
	cfg, err := loadTOML(t, res.LiveTOML())
	if err != nil {
		t.Fatalf("load: %v\n--- live toml ---\n%s", err, res.LiveTOML())
	}
	if len(cfg.Tasks) != 0 {
		t.Errorf("expected no tasks, got %d", len(cfg.Tasks))
	}
	if len(res.SkippedLive()) != 2 {
		t.Errorf("expected both jobs reported as skipped, got %+v", res.SkippedLive())
	}
}
