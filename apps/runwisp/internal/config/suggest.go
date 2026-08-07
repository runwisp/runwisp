// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// misplacedKeyHints maps "<table>.<key>" to guidance for keys operators
// commonly put in the wrong section. Same-level did-you-mean (unknownKeyInfo)
// can't catch these because the key is valid *somewhere else* in the schema.
// Keyed on the failing key's immediate table + leaf name.
var misplacedKeyHints = map[string]string{
	"defaults.on_overlap":  "on_overlap is a per-task setting — move it under [tasks.<name>] or [services.<name>]",
	"scheduler.on_overlap": "on_overlap is a per-task setting — move it under [tasks.<name>] or [services.<name>]",
	"defaults.timezone":    "set the daemon-wide timezone in [scheduler] timezone, or a per-task timezone under [tasks.<name>]",
	"daemon.timezone":      "the daemon-wide timezone lives in [scheduler] timezone, not [daemon]",
	"daemon.host":          "host is set with the --host flag (or RUNWISP_HOST), not in [daemon]",
	"daemon.port":          "port is set with the --port flag (or RUNWISP_PORT), not in [daemon]",
}

// sectionHint returns curated cross-section guidance for a misplaced key, or ""
// when none applies. It matches on the failing key's immediate table and leaf
// name (e.g. "defaults"+"on_overlap"), so it only fires for the specific
// table/key combinations operators trip on — a correctly-placed key never has a
// strict-mode error to begin with.
func sectionHint(key toml.Key) string {
	if len(key) < 2 {
		return ""
	}
	table := key[len(key)-2]
	leaf := key[len(key)-1]
	return misplacedKeyHints[table+"."+leaf]
}

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
