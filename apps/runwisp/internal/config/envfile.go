// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// loadEnvFile resolves path relative to baseDir, parses it literally, and runs
// the same key/value validation rules as inline `env` blocks. The returned map
// is suitable for direct merging into Task.Env / Task.Secrets.
//
// Values are taken literally: no shell-style expansion, interpolation, or
// inline-comment stripping. This matters for secrets_file — a password like
// `Tr0ub4dour$XYZ` or `pass#word` must reach the process unchanged. (godotenv,
// the obvious library choice, silently expands `$VAR`/`${VAR}` and strips
// trailing `#` comments, which would corrupt such secrets with no error.)
func loadEnvFile(baseDir, path string) (map[string]string, error) {
	resolved, err := resolvePath(baseDir, path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("read env_file %s: %w", resolved, err)
	}
	defer f.Close()

	values, err := parseEnvFile(f)
	if err != nil {
		return nil, fmt.Errorf("read env_file %s: %w", resolved, err)
	}
	if err := validateEnvMap(fmt.Sprintf("env_file %s", resolved), values); err != nil {
		return nil, err
	}
	return values, nil
}

// parseEnvFile reads KEY=VALUE lines literally. Blank lines and lines whose
// first non-blank rune is '#' are skipped; an optional `export ` prefix on the
// key is ignored. Surrounding whitespace is trimmed, and one pair of matching
// surrounding quotes (single or double) is stripped — everything inside is kept
// byte-for-byte, with no expansion or escape processing.
func parseEnvFile(r io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimLeft(sc.Text(), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: missing '=' separator", lineNum)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if q := val[0]; (q == '"' || q == '\'') && val[len(val)-1] == q {
				val = val[1 : len(val)-1]
			}
		}
		values[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
