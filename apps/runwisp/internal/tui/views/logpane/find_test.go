// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logpane

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestLogPane_FindMarksMatchingRows(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "all quiet")
	p.AppendLine(1, "stdout", "\x1b[31merror\x1b[0m: disk full")
	re := regexp.MustCompile("error: disk")
	p.FindRanges = func(text string) [][]int { return re.FindAllStringIndex(ansi.Strip(text), -1) }

	var b strings.Builder
	p.RenderLines(&b, false, false)
	out := b.String()

	assert.Contains(t, out, findMatchStyle.Render("error: disk"), "the match is tinted")
	assert.Contains(t, ansi.Strip(out), "error: disk full", "the rest of the row follows it")
	rows := strings.Split(ansi.Strip(out), "\n")
	var quiet, hit string
	for _, r := range rows {
		switch {
		case strings.Contains(r, "all quiet"):
			quiet = r
		case strings.Contains(r, "disk full"):
			hit = r
		}
	}
	assert.Contains(t, hit, "2•", "a matching row gets a gutter marker")
	assert.NotContains(t, quiet, "•")
}
