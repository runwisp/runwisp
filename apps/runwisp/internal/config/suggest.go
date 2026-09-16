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
	"defaults.on_overlap":     "on_overlap is a per-task setting — move it under [tasks.<name>] or [services.<name>]",
	"defaults.timezone":       "set the daemon-wide timezone in [daemon] timezone, or a per-task timezone under [tasks.<name>]",
	"daemon.host":             "host is set with the --host flag (or RUNWISP_HOST), not in [daemon]",
	"daemon.port":             "port is set with the --port flag (or RUNWISP_PORT), not in [daemon]",
	"notify.keep_occurrences": "keep_occurrences was renamed to coalesce_limit",
	"notify.coalesce_every":   "coalesce_every was renamed to coalesce_limit",
}

// removedTableHints maps a whole removed top-level table name to guidance on
// where its keys live now. Unlike misplacedKeyHints, an entirely unknown table
// decodes as a single-segment key (the table name only — go-toml collapses
// every key inside it into one error), so it needs its own lookup rather than
// the table+leaf shape sectionHint otherwise uses.
var removedTableHints = map[string]string{
	"scheduler": "[scheduler] was folded into [daemon] — set timezone under [daemon] instead",
}

// crossKindKeyHints maps a leaf key name to guidance when it's found under the
// wrong unit kind — a [services.*]-only key on [tasks.*], or vice versa. It is
// keyed on the bare leaf name (not "<table>.<leaf>") because for a map-valued
// table like [tasks.mytask], the key segment immediately before the leaf is
// the operator-chosen task name, not the literal "tasks" — so sectionHint
// special-cases key[0] to reach this table instead.
var crossKindKeyHints = map[string]string{
	// Service-only keys, mistakenly set on a task.
	"restart":          "restart is only valid on [services.*] — to re-run a failed task use retry_attempts/retry_delay/retry_backoff",
	"restart_attempts": "restart_attempts is only valid on [services.*] — bound task re-runs with retry_attempts",
	"restart_delay":    "restart_delay is only valid on [services.*]",
	"restart_backoff":  "restart_backoff is only valid on [services.*]",
	"healthy_after":    "healthy_after is only valid on [services.*]",
	"priority":         "priority is only valid on [services.*]",
	"autostart":        "autostart is only valid on [services.*]",
	"instances":        "instances is only valid on [services.*]",
	"depends_on":       "depends_on is only valid on [services.*]",

	// Task-only keys, mistakenly set on a service.
	"cron":           "cron is only valid on [tasks.*] — services are not cron-driven",
	"jitter":         "jitter is only valid on [tasks.*]",
	"catch_up":       "catch_up is only valid on [tasks.*]",
	"run_on_start":   "run_on_start is only valid on [tasks.*]",
	"max_concurrent": "max_concurrent is only valid on [tasks.*]",
	"max_queued":     "max_queued is only valid on [tasks.*]",
	"retry_attempts": "retry_attempts is only valid on [tasks.*]",
	"retry_delay":    "retry_delay is only valid on [tasks.*]",
	"retry_backoff":  "retry_backoff is only valid on [tasks.*]",
	"on_overlap":     "on_overlap is only valid on [tasks.*] — a service never runs a second overlapping instance, instances controls parallelism",
	"params":         "params is only valid on [tasks.*] (services are not manually triggered)",
}

// sectionHint returns curated cross-section guidance for a misplaced key, or ""
// when none applies.
//
// A single-segment key means an entire top-level table failed to match (see
// removedTableHints). Otherwise, when the key path starts with "tasks" or
// "services", the segment right before the leaf is the operator's own
// task/service name rather than the literal table name, so cross-kind
// guidance is matched on the leaf alone (crossKindKeyHints). Every other case
// matches on the failing key's immediate table and leaf name (e.g.
// "defaults"+"on_overlap") via misplacedKeyHints — a correctly-placed key
// never has a strict-mode error to begin with.
func sectionHint(key toml.Key) string {
	if len(key) == 1 {
		return removedTableHints[key[0]]
	}
	if len(key) < 2 {
		return ""
	}
	leaf := key[len(key)-1]
	if key[0] == "tasks" || key[0] == "services" {
		if hint := crossKindKeyHints[leaf]; hint != "" {
			return hint
		}
	}
	table := key[len(key)-2]
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
