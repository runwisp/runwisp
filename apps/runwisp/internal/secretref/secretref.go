// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package secretref resolves ${...} placeholders embedded in runwisp.toml
// values. It reads environment variables and local files only — never the
// network — so resolution works fully offline. The engine is deliberately a
// dependency-free leaf so config, executor, and notify can all share it
// without import cycles.
package secretref

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// token is the trigger sequence; only "${...}" is interpreted. A bare "$" — as
// in a password like "p@ss$word" — passes through untouched.
const token = "${"

// Ref records a single placeholder a value depended on. FromFile is true for
// ${file:...} refs (whose Name is the path); otherwise Name is the env var
// name. Value is what that single placeholder resolved to. Callers use Name and
// FromFile to decide display visibility (reveal_vars) and Value to seed
// redaction with the exact secret substring — not the whole interpolated string.
type Ref struct {
	Name     string
	FromFile bool
	Value    string
}

// Contains reports whether s has any ${...} placeholder. It is the fast-path
// callers use to skip resolution (and the redaction/visibility machinery) for
// plain literals.
func Contains(s string) bool {
	return strings.Contains(s, token)
}

// Resolve interpolates every ${...} placeholder in s and returns the resolved
// string alongside the references it used. Three forms are recognised:
//
//   - ${NAME}           → value of env var NAME; error when unset or empty.
//   - ${NAME:-default}  → env var NAME, falling back to default when unset/empty.
//   - ${file:/abs/path} → file contents, TrimSpace'd. Relative paths resolve
//     against dataDir.
//
// A value with no "${" is returned unchanged with no refs. An unterminated
// "${" (no closing "}") is an error so nothing silently fails. Multiple
// placeholders in one value are each resolved in order.
func Resolve(s, dataDir string) (string, []Ref, error) {
	if !Contains(s) {
		return s, nil, nil
	}
	var b strings.Builder
	var refs []Ref
	for {
		start := strings.Index(s, token)
		if start < 0 {
			b.WriteString(s)
			return b.String(), refs, nil
		}
		b.WriteString(s[:start])
		rest := s[start+len(token):]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return "", nil, fmt.Errorf("unterminated %q in value", token)
		}
		val, ref, err := resolveToken(rest[:end], dataDir)
		if err != nil {
			return "", nil, err
		}
		b.WriteString(val)
		refs = append(refs, ref)
		s = rest[end+1:]
	}
}

// Reveal resolves s for display only when every placeholder it uses is
// revealable — an env reference whose name is in reveal, never a ${file:...}
// reference. Otherwise it returns s unchanged so the raw ${...} placeholder is
// what the API/UI shows. A resolution error (e.g. a var that went missing)
// likewise falls back to the raw string: display must never fail or leak. This
// is the display-side mirror of the boot-time hidden-secret classification.
func Reveal(s, dataDir string, reveal map[string]bool) string {
	if !Contains(s) {
		return s
	}
	resolved, refs, err := Resolve(s, dataDir)
	if err != nil {
		return s
	}
	for _, ref := range refs {
		if ref.FromFile || !reveal[ref.Name] {
			return s
		}
	}
	return resolved
}

// resolveToken resolves the text between "${" and "}" into its value and the
// reference it represents. A "file:" prefix reads a file; otherwise the token
// is an env var name with an optional ":-default" fallback.
func resolveToken(tok, dataDir string) (string, Ref, error) {
	if path, ok := strings.CutPrefix(tok, "file:"); ok {
		v, err := readSecretFile(path, dataDir)
		return v, Ref{Name: path, FromFile: true, Value: v}, err
	}
	name, def, hasDefault := strings.Cut(tok, ":-")
	if v := os.Getenv(name); v != "" {
		return v, Ref{Name: name, Value: v}, nil
	}
	if hasDefault {
		return def, Ref{Name: name, Value: def}, nil
	}
	return "", Ref{Name: name}, fmt.Errorf("env var %s is not set", name)
}

// readSecretFile reads a secret from disk, trimming surrounding whitespace.
// Relative paths resolve against dataDir.
func readSecretFile(file, dataDir string) (string, error) {
	path := file
	if !filepath.IsAbs(path) && dataDir != "" {
		path = filepath.Join(dataDir, file)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}
