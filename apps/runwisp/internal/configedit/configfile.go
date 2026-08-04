// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
)

// WriteInit creates a starter runwisp.toml at path. It errors if the file
// already exists. The contents are a minimal, self-documenting template; the
// full schema reference lives in the docs.
func WriteInit(path string) error {
	return writeNew(path, config.StarterConfig())
}

// WriteInitWithCompose creates a runwisp.toml that imports an adjacent
// docker-compose file. composeFilename is the basename of the discovered
// compose file (e.g. "docker-compose.yml"); alias is the [compose.<alias>]
// block name (usually the parent directory name, sanitized).
func WriteInitWithCompose(path, composeFilename, alias string) error {
	return writeNew(path, config.ComposeStarterConfig(composeFilename, alias))
}

// WriteInitWithCron creates a runwisp.toml that reads real crontabs live via
// [daemon] include_cron. patterns are the include_cron globs to bake in —
// normally CronScan.Globs from the detection that decided to offer this.
func WriteInitWithCron(path string, patterns []string) error {
	return writeNew(path, config.CronStarterConfig(patterns))
}

// WriteInitWithComposeAndCron creates a runwisp.toml combining an adjacent
// compose import with a live include_cron — the first-run scaffold's
// both-detected case, one file for one yes/no answer.
func WriteInitWithComposeAndCron(path, composeFilename, alias string, patterns []string) error {
	return writeNew(path, config.ComposeAndCronStarterConfig(composeFilename, alias, patterns))
}

// writeNew creates path with the given contents, refusing to clobber an
// existing file. The write goes through a Txn so a scaffold is never left
// half-written; there is no gate, because a freshly rendered template has
// nothing to conflict with.
func writeNew(path, contents string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	txn := New()
	txn.Write(path, []byte(contents), DefaultPerm)
	return txn.Apply(nil)
}
