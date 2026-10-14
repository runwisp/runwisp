// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mcpSession connects an in-memory MCP client to a server built exactly as
// `runwisp mcp` builds it.
func mcpSession(t *testing.T, f Flags, rf remoteFlags) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	_, err := newMCPServer(f, rf).Connect(t.Context(), st, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callMCP(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text, res.IsError
}

func mcpToolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestMCP_ToolsFollowTarget: list and validate read the local runwisp.toml, so
// a remote target must not offer them.
func TestMCP_ToolsFollowTarget(t *testing.T) {
	assert.Contains(t, mcpToolNames(t, mcpSession(t, Flags{}, remoteFlags{})), "validate")
	remote := mcpToolNames(t, mcpSession(t, Flags{}, remoteFlags{URL: "https://runwisp.invalid"}))
	assert.Contains(t, remote, "run_task")
	assert.NotContains(t, remote, "list")
	assert.NotContains(t, remote, "validate")
}

func TestMCP_ValidateReturnsErrorDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte("[[task]]\nname = \n"), 0o600))
	s := mcpSession(t, Flags{CfgFile: path}, remoteFlags{})

	text, isErr := callMCP(t, s, "validate", nil)

	assert.True(t, isErr)
	var doc validateJSONDoc
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	assert.False(t, doc.Valid)
	assert.NotEmpty(t, doc.Errors)
}

func TestMCP_StatusUnreachableIsUnhealthyDocument(t *testing.T) {
	s := mcpSession(t, Flags{DataDir: t.TempDir()}, remoteFlags{})

	text, isErr := callMCP(t, s, "status", nil)

	assert.True(t, isErr)
	var doc statusJSONDoc
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	assert.False(t, doc.Healthy)
	assert.NotEmpty(t, doc.Error)
}

// TestMCP_RunTaskWaitsAndKeepsStdoutClean: stdout is the protocol channel, so
// a run's log lines must never reach it, and the result is the `run --json`
// document with the failure bit set.
func TestMCP_RunTaskWaitsAndKeepsStdoutClean(t *testing.T) {
	reason := model.ReasonFailed
	run := model.Run{ID: "run-1", TaskName: "alpha", Status: model.PhaseEnded, EndReason: &reason, ExitCode: 7, IsFailure: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("/api/tasks/alpha/run", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(run)
	})
	mux.HandleFunc("/api/tasks/alpha/runs/run-1/log/stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: line\ndata: {\"n\":0,\"stream\":\"stdout\",\"text\":\"boom-line\"}\n\n" +
			"event: done\ndata: {\"final_line\":0,\"status\":\"ended\"}\n\n"))
	})
	mux.HandleFunc("/api/runs/run-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(run)
	})
	f := serveStatusSocket(t, mux)
	writeLivePidFile(t, f.DataDir)
	s := mcpSession(t, f, remoteFlags{})

	var text string
	var isErr bool
	stdout := captureStdout(t, func() {
		text, isErr = callMCP(t, s, "run_task", map[string]any{"task": "alpha"})
	})

	assert.Empty(t, stdout)
	assert.False(t, isErr)
	var doc runJSONDoc
	require.NoError(t, json.Unmarshal([]byte(text), &doc))
	assert.Equal(t, "run-1", doc.RunID)
	assert.True(t, doc.Failed)
	require.NotNil(t, doc.ExitCode)
	assert.Equal(t, 7, *doc.ExitCode)
	assert.False(t, strings.Contains(text, "boom-line"))
}

// mcpDaemonSession serves the fake log daemon plus the control endpoints the
// MCP tools use beyond `runwisp logs`, and connects a session to it.
func mcpDaemonSession(t *testing.T, d *fakeLogDaemon) *mcp.ClientSession {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/", d.handler())
	mux.HandleFunc("POST /api/tasks/alpha/run", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(model.Run{ID: "run-new", TaskName: "alpha", Status: model.PhaseRunning})
	})
	mux.HandleFunc("GET /api/tasks/{name}/log/search", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "boom", r.URL.Query().Get("q"))
		_, _ = w.Write([]byte(`{"items":[],"exhausted":true,"scannedRuns":1}`))
	})
	mux.HandleFunc("POST /api/runs/{id}/stop", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("POST /api/daemon/reload", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"added":["beta"]}`))
	})
	f, _, _ := serveServiceSocket(t, mux)
	return mcpSession(t, f, remoteFlags{})
}

func TestMCP_ReadAndControlTools(t *testing.T) {
	d := newFakeLogDaemon()
	d.addTask("alpha", model.KindTask, 1)
	d.addRun("run-1", "alpha", 0, model.ReasonFailed, 7, "first", "err:boom")
	s := mcpDaemonSession(t, d)

	runs, isErr := callMCP(t, s, "runs", map[string]any{"task": "alpha"})
	assert.False(t, isErr)
	assert.Contains(t, runs, `"run-1"`)

	logs, isErr := callMCP(t, s, "logs", map[string]any{"target": "alpha", "lines": 5})
	assert.False(t, isErr)
	assert.Contains(t, logs, "boom")

	search, isErr := callMCP(t, s, "search_logs", map[string]any{"task": "alpha", "query": "boom"})
	assert.False(t, isErr)
	assert.Contains(t, search, `"scannedRuns": 1`)

	stop, isErr := callMCP(t, s, "stop_run", map[string]any{"runId": "run-1"})
	assert.False(t, isErr)
	assert.Equal(t, "stop requested for run run-1", stop)

	reload, isErr := callMCP(t, s, "reload", nil)
	assert.False(t, isErr)
	assert.Contains(t, reload, "beta")

	started, isErr := callMCP(t, s, "run_task", map[string]any{"task": "alpha", "noWait": true})
	assert.False(t, isErr)
	assert.Contains(t, started, "run-new")
}

func TestMCP_RunTaskTriggerFailureIsErrorDocument(t *testing.T) {
	d := newFakeLogDaemon()
	d.addTask("alpha", model.KindTask, 1)
	d.control = func(string, string) int { return http.StatusNotFound }
	s := mcpDaemonSession(t, d)

	text, isErr := callMCP(t, s, "run_task", map[string]any{"task": "missing"})

	assert.True(t, isErr)
	assert.Contains(t, text, "missing")
}

func TestMCP_ListReadsLocalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte("[tasks.nightly]\nrun = \"true\"\ncron = \"0 3 * * *\"\n"), 0o600))
	s := mcpSession(t, Flags{CfgFile: path}, remoteFlags{})

	text, isErr := callMCP(t, s, "list", nil)

	assert.False(t, isErr)
	assert.Contains(t, text, "nightly")
}
