// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoColorsOutsidePalette keeps theme.go the single source of truth for
// every colour RunWisp paints: a lipgloss.Color literal anywhere else in the
// daemon's Go source fails it. Reference a palette entry instead, or add one.
func TestNoColorsOutsidePalette(t *testing.T) {
	root, err := filepath.Abs("../../..") // apps/runwisp
	require.NoError(t, err)
	colorLit := regexp.MustCompile(`\.Color\("`)
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "/internal/tui/uikit/theme.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if colorLit.Match(src) {
			offenders = append(offenders, strings.TrimPrefix(path, root+"/"))
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, offenders, "declare colours in internal/tui/uikit/theme.go only")
}
