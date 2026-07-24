// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
