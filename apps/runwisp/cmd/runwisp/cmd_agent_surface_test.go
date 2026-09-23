// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
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

// TestAgentHelpPointer_SurvivesFang runs --help through the real fang.Execute
// (as main does). fang replaces the root help func, which once silently
// dropped a pointer hooked on the root; subcommands and their children must
// still get it when piped, and not at a terminal.
func TestAgentHelpPointer_SurvivesFang(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		tty  bool
		want bool
	}{
		{"child piped", []string{"child", "--help"}, false, true},
		{"grandchild piped", []string{"child", "leaf", "--help"}, false, true},
		{"help subcommand piped", []string{"help", "child"}, false, true},
		{"child at terminal", []string{"child", "--help"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "root"}
			child := &cobra.Command{Use: "child", Short: "child cmd", Run: func(*cobra.Command, []string) {}}
			child.AddCommand(&cobra.Command{Use: "leaf", Short: "leaf cmd", Run: func(*cobra.Command, []string) {}})
			root.AddCommand(child)
			installAgentHelpPointer(root, func() bool { return tc.tty })

			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(tc.args)
			require.NoError(t, fang.Execute(context.Background(), root))

			assert.Contains(t, out.String(), "child", "fang help should still render")
			if tc.want {
				assert.Contains(t, out.String(), "https://docs.runwisp.com/llms.txt")
			} else {
				assert.NotContains(t, out.String(), "llms.txt")
			}
		})
	}
}
