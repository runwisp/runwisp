// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaCmd_PrintsEmbeddedSchema asserts `runwisp schema` writes the
// embedded schema to the command's stdout byte-for-byte and that it parses as
// JSON — the offline counterpart to the published config.schema.json.
func TestSchemaCmd_PrintsEmbeddedSchema(t *testing.T) {
	var buf bytes.Buffer
	schemaCmd.SetOut(&buf)
	t.Cleanup(func() { schemaCmd.SetOut(nil) })

	require.NoError(t, schemaCmd.RunE(schemaCmd, nil))

	assert.Equal(t, config.SchemaJSON(), buf.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc))
	assert.Contains(t, doc, "$defs")
}

// TestWriteAgentHelpPointer asserts the non-interactive --help pointer: piped
// (non-TTY) output points agents at the machine-readable docs, while an
// interactive terminal gets nothing extra.
func TestWriteAgentHelpPointer(t *testing.T) {
	var piped bytes.Buffer
	writeAgentHelpPointer(&piped, false)
	assert.Contains(t, piped.String(), "https://docs.runwisp.com/llms.txt")

	var tty bytes.Buffer
	writeAgentHelpPointer(&tty, true)
	assert.Empty(t, tty.String())
}
