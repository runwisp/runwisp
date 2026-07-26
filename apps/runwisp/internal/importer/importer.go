// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package importer converts existing crontab and supervisord configuration
// into an annotated runwisp.toml. It is the onboarding bridge for the two
// tools RunWisp replaces.
//
// The package is deliberately pure: parsers take an io.Reader (or file paths)
// and return a *Result; nothing here reaches for the clock, the network, or a
// live daemon. The CLI layer (cmd/runwisp) owns I/O, TTY prompts, and the
// final config.Load round-trip that proves the generated TOML is valid.
//
// A conversion is never silently lossy. Anything that can't map cleanly onto a
// RunWisp key — a MAILTO line, a supervisord [eventlistener], an unparseable
// cron spec — becomes a Note (and usually an inline `# TODO:` comment in the
// emitted TOML) so the operator sees exactly what needs a human.
package importer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Result is the outcome of a conversion: the emitted TOML (built from blocks)
// and the report (rows and file-level notes, see report.go).
type Result struct {
	blocks []block
	// topComments are `#` lines emitted at the very top of the TOML, right after
	// the generated-by header. They ride the stdout pipe safely (a comment is
	// valid TOML) and land in the saved file where a reviewer will see them — the
	// right place for a "this might need a human" banner.
	topComments []string
	// items and notes are the report. An item's status is derived on read (see
	// Item.Status), never stored, so a row's mark can't drift from the notes under
	// it. Blocks are only ever appended by itemRef.emit, so a table can't reach the
	// TOML without a row here to account for it.
	items []Item
	notes []Note
}

// tableNames lists the top-level tables the emitted TOML defines — the
// [tasks.x] / [services.x] headers, without their .env children. It exists for
// the test that checks the emitted file against the report; nothing in the
// package's behavior depends on it.
func (r *Result) tableNames() []string {
	var out []string
	for _, b := range r.blocks {
		path := strings.Split(b.header, ".")
		if len(path) != 2 {
			continue // a .env child, or a table that names no job
		}
		out = append(out, path[1])
	}
	return out
}

// field is one `key = value` line. value is already TOML-formatted.
type field struct {
	key     string
	value   string
	comment string // trailing "# ..." note, without the leading "# "
}

// block is one TOML table — a [tasks.x] / [services.x] / [defaults] header, the
// comment lines that precede it, and its fields. Nothing more: what the CLI
// prints about a job is an Item (report.go), so a job that emits no block still
// gets a row.
type block struct {
	header string   // dotted path, e.g. "tasks.backup" or "defaults.env"
	lead   []string // comment lines emitted above the header, without "# "
	fields []field
	// item indexes the Result row that emitted this block. Stamped by
	// itemRef.emit, which is the only writer of Result.blocks, so every block has
	// one — that is what makes TOMLFor's filter exact rather than a guess.
	item int
}

func (b *block) set(key, value string) {
	b.fields = append(b.fields, field{key: key, value: value})
}

func (b *block) setComment(key, value, comment string) {
	b.fields = append(b.fields, field{key: key, value: value, comment: comment})
}

// TOML renders the full annotated configuration — every block, including the
// ones carrying an unresolved `# TODO`. This is what `runwisp import` writes: the
// operator is being handed a file to review, so a job that needs a fix has to be
// in it.
func (r *Result) TOML() string { return r.TOMLFor(func(Item) bool { return true }) }

// LiveTOML renders only the blocks belonging to rows that may be scheduled
// as-imported. This is what `[daemon] include_cron` loads: the daemon is being
// handed a config to *run*, so a job whose imported form isn't what the source
// would have run must not be in it. See Item.LiveEligible.
func (r *Result) LiveTOML() string { return r.TOMLFor(Item.LiveEligible) }

// SkippedLive lists the rows LiveTOML left out, so the caller can say which jobs
// aren't running and why. Dropping a job without a way to name it is the failure
// mode this exists to prevent.
func (r *Result) SkippedLive() []Item {
	var out []Item
	for _, it := range r.items {
		if !it.LiveEligible() {
			out = append(out, it)
		}
	}
	return out
}

// TOMLFor renders the annotated configuration for the rows keep accepts. Blocks
// are emitted in the order the parser produced them; fields keep their insertion
// order so the output mirrors the source file's logical flow.
//
// One renderer with a predicate rather than two renderers: a second copy is how
// the live path and the import path would drift, and the whole reason
// include_cron reuses this package is that they must not.
func (r *Result) TOMLFor(keep func(Item) bool) string {
	var sb strings.Builder
	sb.WriteString("# Generated by `runwisp import`. Review the notes below, then\n")
	sb.WriteString("# validate with `runwisp validate`.\n")
	for _, line := range r.topComments {
		sb.WriteString("# ")
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	for i := range r.blocks {
		b := &r.blocks[i]
		if !keep(r.items[b.item]) {
			continue
		}
		sb.WriteByte('\n')
		for _, line := range b.lead {
			sb.WriteString("# ")
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		sb.WriteByte('[')
		sb.WriteString(b.header)
		sb.WriteString("]\n")
		for _, f := range b.fields {
			sb.WriteString(f.key)
			sb.WriteString(" = ")
			sb.WriteString(f.value)
			if f.comment != "" {
				sb.WriteString("  # ")
				sb.WriteString(f.comment)
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// --- TOML value formatting helpers ---

// tomlString formats an imported value as a TOML string, escaping it so the
// config loader hands it back unchanged.
//
// The escaping is the part that matters. config.Load runs ${...} substitution
// over every string field except the handful tagged expand:"-", so a `${DB}`
// that appears anywhere in a crontab or supervisord config — a comment that
// becomes a description, an environment value, a CRON_TZ — would either fail the
// load outright ("environment variable DB is not set") or, worse, substitute a
// value the original supervisor never would have. Neither program does ${...}
// substitution, so the imported config must not either: `${` is emitted as the
// `$${` the expander unescapes back to a literal `${`.
//
// This is the default on purpose. Use tomlVerbatimString only for a field the
// loader is documented to skip, so a new call site is safe by default rather
// than safe by whoever wrote it having remembered.
func tomlString(s string) string {
	return tomlVerbatimString(strings.ReplaceAll(s, "${", "$${"))
}

// tomlVerbatimString formats a Go string as a TOML basic string with no
// substitution escaping, switching to a multi-line basic string when the value
// contains newlines (the common case for multi-step `run` scripts).
//
// Only for fields config.Load tags expand:"-" — in practice `run`, where the
// shell does its own expansion and an escaped `$${` would reach it literally.
func tomlVerbatimString(s string) string {
	if !strings.Contains(s, "\n") {
		return strconv.Quote(s)
	}
	// Multi-line basic string. Escape backslashes so the body is taken
	// literally, and guard the rare case of an embedded `"""`.
	body := strings.ReplaceAll(s, `\`, `\\`)
	body = strings.ReplaceAll(body, `"""`, `""\"`)
	return "\"\"\"\n" + body + "\"\"\""
}

// tomlIntArray formats ints as a TOML array, e.g. [0, 2].
func tomlIntArray(vals []int) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// envBlock builds a child table (e.g. "tasks.web.env") with keys sorted for
// deterministic output. Returns false when env is empty.
func envBlock(header string, env map[string]string) (block, bool) {
	if len(env) == 0 {
		return block{}, false
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b := block{header: header}
	for _, k := range keys {
		b.set(k, tomlString(env[k]))
	}
	return b, true
}

// deduper assigns unique, sanitized RunWisp task names. Cron and supervisord
// both yield names that can collide once sanitized (model.SanitizeTaskName
// flattens punctuation), so every generated name flows through here.
type deduper struct {
	seen map[string]int
}

func newDeduper() *deduper { return &deduper{seen: map[string]int{}} }

// reserve claims a name up front so a later unique() for the same base skips
// straight to base-2. Used to seed names the live config already owns, so a
// re-import never emits a duplicate that would fail the merged load.
func (d *deduper) taken(name string) bool { _, ok := d.seen[name]; return ok }

func (d *deduper) reserve(name string) {
	if _, ok := d.seen[name]; !ok {
		d.seen[name] = 1
	}
}

// unique returns base if unused, otherwise base-2, base-3, … The first
// collision for a base claims "-2" so the suffixes read naturally.
func (d *deduper) unique(base string) string { return d.uniqueIn(base, "") }

// uniqueIn is unique with a stable tie-breaker: when base is taken and seed is
// non-empty, the collision resolves to base-seed rather than base-2.
//
// This exists because a positional suffix is only stable while the input order
// is. `[daemon] include_cron` reads a set of files that grows and shrinks — drop
// a lexically-earlier file into /etc/cron.d and every -2 after it renumbers,
// which detaches run history, breaks a notification route matching on the task
// name, and re-anchors catch-up. Deriving the suffix from where the job came from
// instead makes the name a function of the source, so adding an unrelated file
// leaves existing names alone.
func (d *deduper) uniqueIn(base, seed string) string {
	if _, ok := d.seen[base]; !ok {
		d.seen[base] = 1
		return base
	}
	if seed != "" {
		if candidate := base + "-" + seed; !d.taken(candidate) {
			d.seen[candidate] = 1
			return candidate
		}
		// Two jobs in the same file deriving the same name: fall through to the
		// positional suffix, which is stable *within* a file because the file's own
		// line order is.
		base += "-" + seed
		if _, ok := d.seen[base]; !ok {
			d.seen[base] = 1
			return base
		}
	}
	for {
		d.seen[base]++
		candidate := fmt.Sprintf("%s-%d", base, d.seen[base])
		if _, ok := d.seen[candidate]; !ok {
			d.seen[candidate] = 1
			return candidate
		}
	}
}
