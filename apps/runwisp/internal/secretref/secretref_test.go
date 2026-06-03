// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package secretref

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContains(t *testing.T) {
	assert.True(t, Contains("${X}"))
	assert.True(t, Contains("prefix-${X}-suffix"))
	assert.False(t, Contains("plain"))
	assert.False(t, Contains("bare$dollar"))
}

func TestResolve_InlineLiteralUnchanged(t *testing.T) {
	got, refs, err := Resolve("https://hooks.slack.test/T/B/Z", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.test/T/B/Z", got)
	assert.Nil(t, refs)
}

func TestResolve_LiteralDollarStaysIntact(t *testing.T) {
	// A password with a bare "$" must pass through untouched — only "${" triggers.
	got, refs, err := Resolve("p@ss$word$123", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "p@ss$word$123", got)
	assert.Nil(t, refs)
}

func TestResolve_EnvVar(t *testing.T) {
	t.Setenv("RUNWISP_TEST_INTERP", "the-secret")
	got, refs, err := Resolve("${RUNWISP_TEST_INTERP}", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "the-secret", got)
	assert.Equal(t, []Ref{{Name: "RUNWISP_TEST_INTERP", Value: "the-secret"}}, refs)
}

func TestResolve_MultipleTokensRecordEachRef(t *testing.T) {
	t.Setenv("RUNWISP_TEST_A", "aa")
	t.Setenv("RUNWISP_TEST_B", "bb")
	got, refs, err := Resolve("${RUNWISP_TEST_A}-${RUNWISP_TEST_B}", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "aa-bb", got)
	assert.Equal(t, []Ref{{Name: "RUNWISP_TEST_A", Value: "aa"}, {Name: "RUNWISP_TEST_B", Value: "bb"}}, refs)
}

func TestResolve_EnvVarMissing(t *testing.T) {
	require.NoError(t, os.Unsetenv("RUNWISP_TEST_INTERP_MISSING"))
	_, _, err := Resolve("${RUNWISP_TEST_INTERP_MISSING}", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUNWISP_TEST_INTERP_MISSING")
}

func TestResolve_DefaultFallbackWhenUnset(t *testing.T) {
	require.NoError(t, os.Unsetenv("RUNWISP_TEST_INTERP_DEFAULT"))
	got, refs, err := Resolve("${RUNWISP_TEST_INTERP_DEFAULT:-fallback}", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "fallback", got)
	// The ref is recorded even when the default is used, so visibility decisions
	// are independent of the current env state.
	assert.Equal(t, []Ref{{Name: "RUNWISP_TEST_INTERP_DEFAULT", Value: "fallback"}}, refs)
}

func TestResolve_DefaultIgnoredWhenSet(t *testing.T) {
	t.Setenv("RUNWISP_TEST_INTERP_DEFAULT", "real")
	got, _, err := Resolve("${RUNWISP_TEST_INTERP_DEFAULT:-fallback}", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "real", got)
}

func TestResolve_EmptyDefaultMakesOptional(t *testing.T) {
	require.NoError(t, os.Unsetenv("RUNWISP_TEST_INTERP_EMPTY"))
	got, _, err := Resolve("${RUNWISP_TEST_INTERP_EMPTY:-}", t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestResolve_FileAbsolute(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(abs, []byte("  file-secret\n"), 0o600))
	got, refs, err := Resolve("${file:"+abs+"}", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "file-secret", got, "file contents are TrimSpace'd")
	require.Len(t, refs, 1)
	assert.True(t, refs[0].FromFile)
}

func TestResolve_FileRelativeToDataDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rel.secret"), []byte("relative-secret\n"), 0o600))
	got, _, err := Resolve("${file:rel.secret}", dir)
	require.NoError(t, err)
	assert.Equal(t, "relative-secret", got)
}

func TestResolve_FileMissing(t *testing.T) {
	_, _, err := Resolve("${file:/nonexistent/runwisp/secret}", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read secret file")
}

func TestResolve_Unterminated(t *testing.T) {
	_, _, err := Resolve("${RUNWISP_TEST_UNTERMINATED", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated")
}
