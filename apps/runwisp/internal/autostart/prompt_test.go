// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package autostart

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrompter_AutoOKConfirms(t *testing.T) {
	p := NewStdioPrompter(strings.NewReader(""), &bytes.Buffer{}, false, true)
	ok, err := p.Confirm("Proceed?", false)
	require.NoError(t, err)
	assert.True(t, ok, "--yes must skip the prompt and return true")
}

func TestPrompter_NonTTYWithoutYesErrors(t *testing.T) {
	p := NewStdioPrompter(strings.NewReader(""), &bytes.Buffer{}, false, false)
	_, err := p.Confirm("Proceed?", false)
	assert.True(t, errors.Is(err, ErrNeedsYes))
}

func TestPrompter_DefaultsApply(t *testing.T) {
	t.Run("default-yes-bare-enter", func(t *testing.T) {
		p := NewStdioPrompter(strings.NewReader("\n"), &bytes.Buffer{}, true, false)
		ok, err := p.Confirm("Use it?", true)
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("default-no-bare-enter", func(t *testing.T) {
		p := NewStdioPrompter(strings.NewReader("\n"), &bytes.Buffer{}, true, false)
		ok, err := p.Confirm("Use it?", false)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("explicit-y", func(t *testing.T) {
		p := NewStdioPrompter(strings.NewReader("y\n"), &bytes.Buffer{}, true, false)
		ok, err := p.Confirm("Use it?", false)
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("explicit-n", func(t *testing.T) {
		p := NewStdioPrompter(strings.NewReader("n\n"), &bytes.Buffer{}, true, false)
		ok, err := p.Confirm("Use it?", true)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestPrompter_LiteralWordIgnoresAutoOK(t *testing.T) {
	// --yes (autoOK) must NOT skip ConfirmLiteral — that's the
	// whole point of the footgun guard for --purge.
	p := NewStdioPrompter(strings.NewReader("nope\n"), &bytes.Buffer{}, true, true)
	err := p.ConfirmLiteral("Type 'delete'", "delete")
	assert.True(t, errors.Is(err, ErrAborted))
}

func TestPrompter_LiteralWordExactMatch(t *testing.T) {
	p := NewStdioPrompter(strings.NewReader("delete\n"), &bytes.Buffer{}, true, false)
	err := p.ConfirmLiteral("Type 'delete'", "delete")
	assert.NoError(t, err)
}

func TestPrompter_LiteralNonTTYErrors(t *testing.T) {
	p := NewStdioPrompter(strings.NewReader("delete\n"), &bytes.Buffer{}, false, false)
	err := p.ConfirmLiteral("Type 'delete'", "delete")
	assert.True(t, errors.Is(err, ErrNeedsYes))
}
