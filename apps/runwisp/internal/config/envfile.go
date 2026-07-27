// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"

	"github.com/joho/godotenv"
)

// loadEnvFile resolves path relative to baseDir, parses it with godotenv, and
// runs the same key/value validation rules as inline `env` blocks. The
// returned map is suitable for direct merging into Task.Env / Task.Secrets.
//
// godotenv.Read returns the file's KEY=VALUE pairs as a map without touching
// os.Environ; there is no shell-style expansion or interpolation. Operators
// who want a value to look like a shell variable should write it literally.
func loadEnvFile(baseDir, path string) (map[string]string, error) {
	resolved, err := resolvePath(baseDir, path)
	if err != nil {
		return nil, err
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
