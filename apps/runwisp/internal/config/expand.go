// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
)

// expandConfig applies ${VAR} / ${file:path} substitution to every string
// value in the decoded wire config, in place. It runs once, right after TOML
// decoding — the restart-only reload invariant means values never re-expand
// during the daemon's lifetime.
//
// Rules:
//   - ${VAR} resolves through lookupEnv; an unset variable is a hard error
//     naming the variable and the TOML path. Set-but-empty substitutes "".
//   - ${file:path} reads the file (strings.TrimSpace'd); relative paths
//     resolve against baseDir (the runwisp.toml directory), "~/" against the
//     user's home. An unreadable file is a hard error.
//   - $${ escapes to a literal ${. Any other $ passes through verbatim.
//   - Struct fields tagged expand:"-" (task/service `run`) are skipped: the
//     shell expands those at runtime with the full process env.
//   - Map keys are never substituted — only values.
func expandConfig(raw *tomlConfig, baseDir string, lookupEnv func(string) (string, bool)) error {
	e := &expander{baseDir: baseDir, lookupEnv: lookupEnv}
	return e.walkValue(reflect.ValueOf(raw).Elem(), "")
}

type expander struct {
	baseDir   string
	lookupEnv func(string) (string, bool)
}

// walkValue dispatches on kind. String values must be addressable here; map
// values (not addressable) are handled by walkMap / expandAny instead.
func (e *expander) walkValue(v reflect.Value, path string) error {
	switch v.Kind() {
	case reflect.String:
		s, err := e.substitute(v.String(), path)
		if err != nil {
			return err
		}
		v.SetString(s)
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return e.walkValue(v.Elem(), path)
		}
	case reflect.Struct:
		return e.walkStruct(v, path)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := e.walkValue(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		return e.walkMap(v, path)
	default:
		// Scalars (bool, ints) carry nothing to substitute.
	}
	return nil
}

func (e *expander) walkStruct(v reflect.Value, path string) error {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			// Skip unexported fields — except anonymous embedded structs
			// (the shared task/service wire core): reflect drops the
			// read-only flag on their exported sub-fields, so they expand
			// like fields declared on the outer struct.
			if !f.Anonymous || v.Field(i).Kind() != reflect.Struct {
				continue
			}
		}
		if f.Tag.Get("expand") == "-" {
			continue
		}
		fieldPath := path
		if !f.Anonymous {
			// Embedded wire cores surface their fields at the parent level,
			// so only named fields extend the path.
			fieldPath = joinPath(path, tomlFieldName(f))
		}
		if err := e.walkValue(v.Field(i), fieldPath); err != nil {
			return err
		}
	}
	return nil
}

// walkMap substitutes map values in place. Keys are never touched: env var
// names, task names, and compose service names stay literal.
func (e *expander) walkMap(m reflect.Value, path string) error {
	for _, key := range m.MapKeys() {
		val := m.MapIndex(key)
		keyPath := joinPath(path, fmt.Sprintf("%v", key.Interface()))
		switch val.Kind() {
		case reflect.String:
			s, err := e.substitute(val.String(), keyPath)
			if err != nil {
				return err
			}
			m.SetMapIndex(key, reflect.ValueOf(s).Convert(val.Type()))
		case reflect.Interface:
			// Free-form [compose.<alias>] blocks decode to map[string]any.
			expanded, err := e.expandAny(val.Interface(), keyPath)
			if err != nil {
				return err
			}
			m.SetMapIndex(key, reflect.ValueOf(expanded))
		default:
			// Pointer / map / slice values mutate through their references.
			if err := e.walkValue(val, keyPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// expandAny handles the dynamically-typed values inside [compose.<alias>]
// blocks: strings substitute, containers recurse, everything else (bool,
// numbers) passes through untouched.
func (e *expander) expandAny(v any, path string) (any, error) {
	switch t := v.(type) {
	case string:
		return e.substitute(t, path)
	case map[string]any:
		for k, val := range t {
			expanded, err := e.expandAny(val, joinPath(path, k))
			if err != nil {
				return nil, err
			}
			t[k] = expanded
		}
		return t, nil
	case []any:
		for i, val := range t {
			expanded, err := e.expandAny(val, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			t[i] = expanded
		}
		return t, nil
	default:
		return v, nil
	}
}

// substitute applies ${...} substitution to a single string with one
// left-to-right scan. An unterminated ${ is an error; "$${" emits a literal
// "${"; a lone $ passes through.
func (e *expander) substitute(s, path string) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		rest := s[i:]
		switch {
		case strings.HasPrefix(rest, "$${"):
			b.WriteString("${")
			i += 3
		case strings.HasPrefix(rest, "${"):
			end := strings.IndexByte(rest[2:], '}')
			if end < 0 {
				return "", fmt.Errorf("%s: unterminated ${ in %q", path, s)
			}
			value, err := e.resolveRef(rest[2:2+end], path)
			if err != nil {
				return "", err
			}
			b.WriteString(value)
			i += 2 + end + 1
		default:
			b.WriteByte('$')
			i++
		}
	}
	return b.String(), nil
}

// resolveRef resolves the inside of one ${...}: either a file read
// ("file:path") or an environment variable name.
func (e *expander) resolveRef(ref, path string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("%s: empty ${} substitution", path)
	}
	if filePath, ok := strings.CutPrefix(ref, "file:"); ok {
		if filePath == "" {
			return "", fmt.Errorf("%s: ${file:} has no path", path)
		}
		resolved, err := resolvePath(e.baseDir, filePath)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return "", fmt.Errorf("%s: read ${file:%s}: %w", path, filePath, err)
		}
		return strings.TrimSpace(string(content)), nil
	}
	value, ok := e.lookupEnv(ref)
	if !ok {
		return "", fmt.Errorf("%s: environment variable %s is not set", path, ref)
	}
	return value, nil
}

// tomlFieldName returns the TOML key for a struct field, falling back to the
// Go field name when no tag is present.
func tomlFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("toml")
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return f.Name
	}
	return name
}

func joinPath(base, elem string) string {
	if base == "" {
		return elem
	}
	return base + "." + elem
}
