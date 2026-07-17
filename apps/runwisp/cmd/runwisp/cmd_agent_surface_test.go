// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

// TestAgentGuideCmd_PrintsSnippet asserts `runwisp agent-guide` emits the
// paste-ready block, including the schema URL it points agents at.
func TestAgentGuideCmd_PrintsSnippet(t *testing.T) {
	var buf bytes.Buffer
	agentGuideCmd.SetOut(&buf)
	t.Cleanup(func() { agentGuideCmd.SetOut(nil) })

	require.NoError(t, agentGuideCmd.RunE(agentGuideCmd, nil))

	out := buf.String()
	assert.Equal(t, agentGuideSnippet, out)
	assert.Contains(t, out, "## RunWisp")
	assert.Contains(t, out, config.SchemaURL)
}
