// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"path/filepath"

	"github.com/joho/godotenv"
)

// loadEnvFile resolves path relative to baseDir, parses it with godotenv, and
// runs the same key/value validation rules as inline `env` blocks. The
// returned map is suitable for direct use as Task.SecretEnv / Defaults.SecretEnv.
//
// godotenv.Read returns the file's KEY=VALUE pairs as a map without touching
// os.Environ; there is no shell-style expansion or interpolation. Operators
// who want a value to look like a shell variable should write it literally.
func loadEnvFile(baseDir, path string) (map[string]string, error) {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(baseDir, resolved)
	}
	values, err := godotenv.Read(resolved)
	if err != nil {
		return nil, fmt.Errorf("read env_file %s: %w", resolved, err)
	}
	if err := validateEnvMap(fmt.Sprintf("env_file %s", resolved), values); err != nil {
		return nil, err
	}
	return values, nil
}
