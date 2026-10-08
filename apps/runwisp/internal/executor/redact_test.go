// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"os"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSecretRedactor_NilWhenNothingToRedact(t *testing.T) {
	assert.Nil(t, newSecretRedactor(nil))
	assert.Nil(t, newSecretRedactor(map[string]string{}))
	// Empty values can't leak and would make Replacer match everywhere.
	assert.Nil(t, newSecretRedactor(map[string]string{"TOKEN": ""}))
}

func TestSecretRedactor_NilIsNoOp(t *testing.T) {
	var r *secretRedactor // nil
	assert.Equal(t, "s3cr3t", r.text("s3cr3t"))
	rows := []string{"s3cr3t"}
	assert.Equal(t, rows, r.rows(rows)) // same slice, not a copy
}

func TestSecretRedactor_MasksValues(t *testing.T) {
	r := newSecretRedactor(map[string]string{"TOKEN": "s3cr3t", "PW": "hunter2"})
	require.NotNil(t, r)
	assert.Equal(t, "auth="+redactMask+" pw="+redactMask, r.text("auth=s3cr3t pw=hunter2"))
	assert.Equal(t, []string{redactMask}, r.rows([]string{"s3cr3t"}))
	assert.Equal(t, [][]string{{redactMask}}, r.frames([][]string{{"s3cr3t"}}))
}

// TestCommitGroup_RedactsSecretOnBothSinks is the bug-first guard: a secret
// value printed by a run must not survive to either the on-disk log file or
// the published event, since both feed downstream consumers (SSE/REST/station).
func TestCommitGroup_RedactsSecretOnBothSinks(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	redact := newSecretRedactor(map[string]string{"TOKEN": "s3cr3t"})
	var published []string
	commitGroup(w, logutil.StreamStdout,
		[]committedLine{{text: "TOKEN=s3cr3t"}},
		nil, redact,
		func(text string, _ int64, _ bool, _ int) { published = append(published, text) },
	)
	require.NoError(t, w.Close())

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "s3cr3t")
	assert.Contains(t, string(data), redactMask)
	assert.Equal(t, []string{"TOKEN=" + redactMask}, published)
}

// TestCommitGroup_NoSecretsPassesThrough proves the nil-redactor path leaves
// output byte-identical.
func TestCommitGroup_NoSecretsPassesThrough(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	var published []string
	commitGroup(w, logutil.StreamStdout,
		[]committedLine{{text: "plain line"}},
		nil, newSecretRedactor(nil),
		func(text string, _ int64, _ bool, _ int) { published = append(published, text) },
	)
	require.NoError(t, w.Close())

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "plain line")
	assert.Equal(t, []string{"plain line"}, published)
}

// One secret being a prefix of another must not leave the longer one partly
// visible, whatever order the map iterates in.
func TestSecretRedactor_PrefixSecretMasksLongerWhole(t *testing.T) {
	for range 50 {
		r := newSecretRedactor(map[string]string{"SHORT": "abc", "LONG": "abcdef", "OTHER": "abcd"})
		assert.Equal(t, "x [redacted] y", r.text("x abcdef y"))
	}
}

// A shorter secret overlapping the start of a longer one must not leave the
// rest of the longer one in clear.
func TestSecretRedactor_OverlappingSecretsMaskBoth(t *testing.T) {
	r := newSecretRedactor(map[string]string{"A": "tok", "B": "ken-9f8e7d6c5b4a"})
	assert.Equal(t, "x [redacted] y", r.text("x token-9f8e7d6c5b4a y"))
	assert.Equal(t, "[redacted] [redacted]", r.text("tok ken-9f8e7d6c5b4a"))
}
