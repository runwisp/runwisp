// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"fmt"
	"strings"
)

// This file moves whole entries between config files as text. `runwisp promote`
// graduates an imported task out of the machine-owned staging file into the
// operator's own runwisp.toml, and it does that by cutting the task's bytes out
// of one file and appending those exact bytes to the other — never by
// re-rendering a parsed document. That is the point: the importer's inline
// `# TODO:` notes, the operator's own comments, and their formatting all survive
// the move, because nothing here looks at values at all.
//
// Every boundary decision walks lines through scanTOMLLines, so a `[`-line
// inside a `run = """ … """` body is never mistaken for the start of the next
// table.

// entryTables are the two table families an entry can live under. A staged entry
// is always one or the other; [compose.*] tables generate tasks but are never
// written by `import`, so they are not promotable (see HasEntries, which counts
// them for the "is this file still needed" question).
var entryTables = []string{"tasks", "services"}

// Block is one entry's TOML text, cut verbatim out of a document.
type Block struct {
	// Name is the entry name, e.g. "backup".
	Name string
	// Table is the family it was declared under: "tasks" or "services".
	Table string
	// Text is the block's bytes — its lead comments, its header, its fields, and
	// any child tables such as [tasks.backup.env] — with no trailing blank lines.
	Text string
}

// EntryNotFoundError reports that a document declares no [tasks.NAME] or
// [services.NAME] table.
type EntryNotFoundError struct{ Name string }

func (e *EntryNotFoundError) Error() string {
	return fmt.Sprintf("no [tasks.%s] or [services.%s] table found", e.Name, e.Name)
}

// AmbiguousEntryError reports a name declared as both a task and a service in
// one document. A config in that state doesn't load, so this is unreachable via
// the CLI — but the extractor refuses rather than picking one at random.
type AmbiguousEntryError struct{ Name string }

func (e *AmbiguousEntryError) Error() string {
	return fmt.Sprintf("%q is declared as both a task and a service", e.Name)
}

// ExtractBlocks cuts the named entries out of doc and returns what is left plus
// the extracted blocks, in the order they appeared in the document. Every range
// is located before anything is spliced, so extracting several names in one call
// can't invalidate its own offsets.
//
// The remainder is the caller's file minus those bytes, with runs of blank lines
// collapsed so the hole a block left behind doesn't accumulate. That tidying is
// only ever applied to the *source* of a move — the machine-owned staging file —
// never to a file the operator maintains.
func ExtractBlocks(doc []byte, names []string) (remaining []byte, blocks []Block, err error) {
	text := string(doc)
	lines := scanTOMLLines(text)

	spans := make([]blockSpan, 0, len(names))
	for _, name := range names {
		span, err := findBlock(lines, name)
		if err != nil {
			return nil, nil, err
		}
		spans = append(spans, span)
	}
	sortSpans(spans)

	var kept strings.Builder
	cursor := 0
	for _, span := range spans {
		if span.start < cursor {
			continue // a duplicate name in the request; already taken
		}
		kept.WriteString(text[cursor:span.start])
		blocks = append(blocks, Block{
			Name:  span.name,
			Table: span.table,
			Text:  strings.TrimRight(text[span.start:span.end], "\n"),
		})
		cursor = span.end
	}
	kept.WriteString(text[cursor:])

	return []byte(collapseBlankLines(kept.String())), blocks, nil
}

// AppendBlocks appends blocks to the end of doc, separated by one blank line and
// otherwise byte-for-byte. Appending a table at the end of a document is always
// structurally safe in TOML: it can't land inside another table's key list.
func AppendBlocks(doc []byte, blocks []Block) []byte {
	var sb strings.Builder
	sb.WriteString(ensureTrailingNewline(string(doc)))
	for _, b := range blocks {
		sb.WriteString("\n")
		sb.WriteString(strings.TrimRight(b.Text, "\n"))
		sb.WriteString("\n")
	}
	return []byte(sb.String())
}

// HasEntries reports whether doc still declares any task, service, or compose
// table. `promote` uses it to decide whether the staging file has any reason to
// exist once the promoted blocks are gone.
func HasEntries(doc []byte) bool {
	for _, ln := range scanTOMLLines(string(doc)) {
		if !ln.Code {
			continue
		}
		name, _, ok := tableHeader(ln.Text)
		if !ok {
			continue
		}
		segs, ok := splitDottedKey(name)
		if !ok || len(segs) < 2 {
			continue
		}
		switch segs[0] {
		case "tasks", "services", "compose":
			return true
		}
	}
	return false
}

// blockSpan is one entry's byte range in a document.
type blockSpan struct {
	name, table string
	start, end  int
}

// sortSpans orders spans by their start offset so the splice walks the document
// forward exactly once. Insertion sort: a promote request holds a handful of
// names, not thousands.
func sortSpans(spans []blockSpan) {
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].start < spans[j-1].start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
}

// findBlock locates the named entry's full byte range: its lead comments, its
// header line, its fields, and any child table (e.g. [tasks.x.env]).
func findBlock(lines []tomlLine, name string) (blockSpan, error) {
	header := -1
	span := blockSpan{name: name}
	for i, ln := range lines {
		table, ok := headerEntry(ln, name)
		if !ok {
			continue
		}
		if header >= 0 {
			return blockSpan{}, &AmbiguousEntryError{Name: name}
		}
		header, span.table = i, table
	}
	if header < 0 {
		return blockSpan{}, &EntryNotFoundError{Name: name}
	}

	span.start = lines[leadStart(lines, header)].Start
	span.end = blockEnd(lines, header, span.table, name)
	return span, nil
}

// headerEntry reports whether a line is the top-level header of the named entry,
// returning which table family it was declared under. A line inside a multi-line
// string is never structure, and an array-of-tables header ([[…]]) is never an
// entry.
func headerEntry(ln tomlLine, name string) (table string, ok bool) {
	if !ln.Code {
		return "", false
	}
	raw, array, ok := tableHeader(ln.Text)
	if !ok || array {
		return "", false
	}
	segs, ok := splitDottedKey(raw)
	if !ok || len(segs) != 2 || segs[1] != name {
		return "", false
	}
	for _, t := range entryTables {
		if segs[0] == t {
			return t, true
		}
	}
	return "", false
}

// leadStart walks back from a header over the comment lines attached to it and
// returns the index the block starts at. It stops at a blank line, at any
// non-comment line, and at the `#:schema` directive — so a block that happens to
// be first in the file never drags the file's own header comments along with it.
func leadStart(lines []tomlLine, header int) int {
	start := header
	for i := header - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i].Text)
		if !lines[i].Code || trimmed == "" || !strings.HasPrefix(trimmed, "#") {
			break
		}
		if isSchemaDirective(trimmed) {
			break
		}
		start = i
	}
	return start
}

// isSchemaDirective reports whether a comment line is the `#:schema` editor
// directive that belongs to the file, not to any one table.
func isSchemaDirective(trimmed string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(trimmed, "#")), ":schema")
}

// blockEnd returns the offset the block ends at: just past its last non-blank
// line, stopping before the next table that isn't one of this entry's own child
// tables — and before *that* table's lead comments, which belong to it and not
// to us. Trailing blank lines stay with the source document, so the block itself
// carries no padding.
func blockEnd(lines []tomlLine, header int, table, name string) int {
	limit := len(lines)
	for i := header + 1; i < len(lines); i++ {
		if lines[i].Code && isForeignHeader(lines[i], table, name) {
			limit = leadStart(lines, i)
			break
		}
	}

	end := lines[header].End
	for i := header + 1; i < limit; i++ {
		if strings.TrimSpace(lines[i].Text) != "" {
			end = lines[i].End
		}
	}
	return end
}

// isForeignHeader reports whether a line opens a table that does not belong to
// the given entry — i.e. any header other than [table.name] itself or one of its
// children [table.name.*].
func isForeignHeader(ln tomlLine, table, name string) bool {
	raw, _, ok := tableHeader(ln.Text)
	if !ok {
		return false
	}
	segs, ok := splitDottedKey(raw)
	if !ok {
		// A header we can't read is still a header: stop rather than swallow it.
		return true
	}
	return len(segs) < 2 || segs[0] != table || segs[1] != name
}

// collapseBlankLines squeezes runs of blank lines down to one and trims the
// leading and trailing padding, so a file a block was cut out of doesn't grow a
// gap where it used to be.
//
// It walks lines through the scanner rather than pattern-matching the text,
// because blank lines inside a multi-line string are part of a task's `run`
// script: collapsing those would quietly rewrite what the task executes.
func collapseBlankLines(text string) string {
	var sb strings.Builder
	prevBlank := true // start-of-file counts as blank, so leading padding is dropped
	for _, ln := range scanTOMLLines(text) {
		blank := ln.Code && strings.TrimSpace(ln.Text) == ""
		if blank && prevBlank {
			continue
		}
		sb.WriteString(ln.Text)
		prevBlank = blank
	}
	return ensureTrailingNewline(strings.TrimRight(sb.String(), "\n"))
}
