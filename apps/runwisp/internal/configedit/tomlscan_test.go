// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configedit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanTOMLLines_TagsLinesInsideMultilineStrings(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		// code lists the expected Code flag per line, in order.
		code []bool
	}{
		{
			name: "plain document is all code",
			doc:  "[daemon]\nshutdown_timeout = \"10s\"\n",
			code: []bool{true, true},
		},
		{
			name: "multi-line basic string body is not code",
			doc:  "run = \"\"\"\nline one\n[daemon]\n\"\"\"\nafter = 1\n",
			code: []bool{true, false, false, false, true},
		},
		{
			name: "multi-line literal string body is not code",
			doc:  "run = '''\n[daemon]\n'''\nafter = 1\n",
			code: []bool{true, false, false, true},
		},
		{
			name: "single-line string containing triple quotes stays code",
			doc:  "a = \"say \\\"hi\\\"\"\n[daemon]\n",
			code: []bool{true, true},
		},
		{
			name: "triple quotes opened and closed on one line stay code",
			doc:  "a = \"\"\"one line\"\"\"\n[daemon]\n",
			code: []bool{true, true},
		},
		{
			name: "a commented-out triple quote does not open a string",
			doc:  "# run = \"\"\"\n[daemon]\n",
			code: []bool{true, true},
		},
		{
			name: "no trailing newline still yields the last line",
			doc:  "[daemon]\ninclude = []",
			code: []bool{true, true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := scanTOMLLines(tc.doc)
			require.Len(t, lines, len(tc.code))
			for i, want := range tc.code {
				assert.Equal(t, want, lines[i].Code, "line %d (%q)", i, lines[i].Text)
			}
		})
	}
}

// TestScanTOMLLines_OffsetsSpanTheDocument pins the offsets the surgical editors
// slice on: every line must be reconstructible from the original text, and the
// ranges must tile the document with no gap or overlap.
func TestScanTOMLLines_OffsetsSpanTheDocument(t *testing.T) {
	doc := "[daemon]\ninclude = []\n\n[tasks.x]\nrun = \"echo\""
	lines := scanTOMLLines(doc)
	require.NotEmpty(t, lines)

	prevEnd := 0
	for i, ln := range lines {
		assert.Equal(t, prevEnd, ln.Start, "line %d must start where the previous ended", i)
		assert.Equal(t, doc[ln.Start:ln.End], ln.Text, "line %d text must match its offsets", i)
		prevEnd = ln.End
	}
	assert.Equal(t, len(doc), prevEnd, "the lines must cover the whole document")
}

func TestTableHeader(t *testing.T) {
	tests := []struct {
		line      string
		wantName  string
		wantArray bool
		wantOK    bool
	}{
		{line: "[daemon]", wantName: "daemon", wantOK: true},
		{line: "  [daemon]  ", wantName: "daemon", wantOK: true},
		{line: "[daemon] # trailing comment", wantName: "daemon", wantOK: true},
		{line: "[tasks.backup]\n", wantName: "tasks.backup", wantOK: true},
		{line: "[[notifier]]", wantName: "notifier", wantArray: true, wantOK: true},
		{line: "include = [\"a\"]", wantOK: false},
		{line: "[daemon] include = []", wantOK: false},
		{line: "[]", wantOK: false},
		{line: "[daemon", wantOK: false},
		{line: "# [daemon]", wantOK: false},
		{line: "", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			name, array, ok := tableHeader(tc.line)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantName, name)
			assert.Equal(t, tc.wantArray, array)
		})
	}
}

// TestDaemonHeaderEnd_SkipsArrayOfTables makes sure a `[[daemon]]` array header
// is never mistaken for the `[daemon]` singleton table.
func TestDaemonHeaderEnd_SkipsArrayOfTables(t *testing.T) {
	_, ok := daemonHeaderEnd("[[daemon]]\n")
	assert.False(t, ok)
}
