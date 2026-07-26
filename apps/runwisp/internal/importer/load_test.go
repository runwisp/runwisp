// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
