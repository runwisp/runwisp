// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolvePath turns an operator-written path into an absolute one using a
// single set of semantics shared by ${file:...} substitution, env_file /
// secrets_file loading, and compose file resolution: absolute paths pass
// through, "~" / "~/..." expands to the user's home directory, and anything
// else joins onto baseDir (the runwisp.toml directory).
func resolvePath(baseDir, path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: %w", path, err)
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
	}
	return filepath.Join(baseDir, path), nil
}
