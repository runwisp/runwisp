// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// unknownKeyInfo walks the wire-struct tree along a strict-mode error's key
// path and pinpoints the segment that failed to match, plus the valid keys at
// that level. Candidates come from reflecting `toml:` tags, so they can never
// drift from the actual decode surface. ok is false when the path runs
// through free-form territory (env maps, [compose.*] blocks) or matches
// fully — no suggestion is possible there.
func unknownKeyInfo(key toml.Key) (segment string, candidates []string, ok bool) {
	current := reflect.TypeFor[tomlConfig]()
	for _, seg := range key {
		current = unwrap(current)
		switch current.Kind() {
		case reflect.Struct:
			field, found := fieldByTag(current, seg)
			if !found {
				return seg, tomlTags(current), true
			}
			current = field
		case reflect.Map:
			// Map keys are operator-chosen (task names, env keys) — any
			// segment matches; descend into the element type.
			current = current.Elem()
		default:
			// Interface (free-form compose blocks) or scalar with leftover
			// path segments — nothing to suggest.
			return "", nil, false
		}
	}
	return "", nil, false
}

// unwrap strips pointers and slices: array-of-table paths ([notifier]) carry
// no index segment, so the element type is matched directly.
func unwrap(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t
}

// tomlTags returns the TOML key names declared on a wire struct,
// recursing into anonymous (embedded) fields whose promoted keys are
// part of the decode surface.
func tomlTags(t reflect.Type) []string {
	var tags []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			tags = append(tags, tomlTags(unwrap(f.Type))...)
			continue
		}
		if name := tomlTagName(f); name != "" {
			tags = append(tags, name)
		}
	}
	return tags
}

// fieldByTag resolves a TOML key name to the corresponding field type,
// recursing into anonymous (embedded) fields.
func fieldByTag(t reflect.Type, name string) (reflect.Type, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			if ft, ok := fieldByTag(unwrap(f.Type), name); ok {
				return ft, true
			}
			continue
		}
		if tomlTagName(f) == name {
			return f.Type, true
		}
	}
	return nil, false
}

func tomlTagName(f reflect.StructField) string {
	tag := f.Tag.Get("toml")
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	return name
}
