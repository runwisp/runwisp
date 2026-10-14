// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/version"
	"github.com/spf13/cobra"
)

var mcpRemote remoteFlags

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve RunWisp to AI agents over MCP (stdio)",
	Long: `Run a Model Context Protocol server on stdin/stdout so an AI agent can see
what ran, why it failed and what it printed, and trigger runs.

Every tool returns the same JSON document its CLI counterpart prints with
--json. Tasks are still defined only in runwisp.toml: no tool edits config.
After changing the file, the agent calls validate and reload.

By default the server talks to the local daemon over its socket. With --url
(or RUNWISP_URL) it controls a remote daemon instead; list and validate read
the local runwisp.toml, so they are only offered for the local daemon. To
reach a daemon over SSH, run this command there:
  claude mcp add runwisp -- ssh host runwisp mcp`,
	Example: `  claude mcp add runwisp -- runwisp mcp
  claude mcp add runwisp -- runwisp mcp --url https://runwisp.example.com`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return newMCPServer(flags, mcpRemote).Run(cmd.Context(), &mcp.StdioTransport{})
	},
}

func init() {
	mcpCmd.Flags().StringVar(&mcpRemote.URL, "url", "", "control a remote daemon at this base URL (env: RUNWISP_URL)")
	mcpCmd.Flags().StringVar(&mcpRemote.Password, "password", "", "remote daemon password for --url (env: RUNWISP_PASSWORD)")
}

const (
	mcpDefaultLogLines  = 100
	mcpMaxLogLines      = 1000
	mcpDefaultRunsLimit = 20
	mcpDefaultWait      = 300 * time.Second
)

// mcpServer binds the tools to one daemon target. Each call connects afresh
// (a health check on the socket or a cached session), so a daemon restart or
// an expired remote session between calls never leaves the agent stuck.
type mcpServer struct {
	f        Flags
	rf       remoteFlags
	baseURL  string
	password string
}

func newMCPServer(f Flags, rf remoteFlags) *mcp.Server {
	m := &mcpServer{f: f, rf: rf}
	m.baseURL, m.password = rf.resolve()

	s := mcp.NewServer(&mcp.Implementation{Name: "runwisp", Version: version.Version}, nil)
	read := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(s, &mcp.Tool{Name: "status", Description: "Daemon health plus every task with its last run, next fire time and failed flag. Start here.", Annotations: read}, m.status)
	mcp.AddTool(s, &mcp.Tool{Name: "runs", Description: "Run history, newest first, optionally filtered by task or status.", Annotations: read}, m.runs)
	mcp.AddTool(s, &mcp.Tool{Name: "logs", Description: "Captured output of a task's most recent run (or of a run by ID), as one JSON object per line.", Annotations: read}, m.logs)
	mcp.AddTool(s, &mcp.Tool{Name: "search_logs", Description: "Search a task's log files across runs (substring or regex). Use it to find when an error first appeared.", Annotations: read}, m.searchLogs)
	mcp.AddTool(s, &mcp.Tool{Name: "run_task", Description: "Trigger a task now and, by default, wait for its outcome (status, exit code, failed). Read the output with logs."}, m.runTask)
	mcp.AddTool(s, &mcp.Tool{Name: "stop_run", Description: "Stop an active run by its run ID."}, m.stopRun)
	mcp.AddTool(s, &mcp.Tool{Name: "reload", Description: "Apply runwisp.toml to the running daemon (validate-first: an invalid file changes nothing). Call after editing the file."}, m.reload)
	if m.baseURL == "" {
		mcp.AddTool(s, &mcp.Tool{Name: "list", Description: "Tasks and schedules as defined in the local runwisp.toml. Works without a daemon.", Annotations: read}, m.list)
		mcp.AddTool(s, &mcp.Tool{Name: "validate", Description: "Validate the local runwisp.toml; errors carry key, line and column. Works without a daemon.", Annotations: read}, m.validate)
	}
	return s
}

// client connects to the target daemon; withSession retries once when a cached
// remote session has expired.
func (m *mcpServer) client(ctx context.Context) (*apiclient.Client, error) {
	return controlClient(ctx, m.f, m.baseURL, m.password)
}

func (m *mcpServer) withSession(ctx context.Context, c *apiclient.Client, call func() error) error {
	return withSessionRetry(ctx, c, m.baseURL, m.password, call)
}

// jsonResult is the tool result for a JSON-serialisable doc.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(b), false), nil, nil
}

func textResult(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isError}
}

// docResult returns the document a CLI helper wrote to buf. A non-nil err
// marks the result as an error but keeps the document, which carries the
// structured failure (validate's key/line/column, run's error field).
func docResult(buf *bytes.Buffer, err error) (*mcp.CallToolResult, any, error) {
	if buf.Len() == 0 {
		return nil, nil, err
	}
	return textResult(buf.String(), err != nil), nil, nil
}

type noInput struct{}

func (m *mcpServer) status(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	c, err := m.client(ctx)
	if err != nil {
		// Same unhealthy document `status --json` emits, so the agent reads the
		// failure from the result rather than from a protocol error.
		res, _, _ := jsonResult(statusJSONDoc{SchemaVersion: jsonSchemaVersion, Error: err.Error(), Tasks: []statusTaskJSON{}})
		res.IsError = true
		return res, nil, nil
	}
	return jsonResult(buildStatusDoc(ctx, c))
}

type runsInput struct {
	Task   string `json:"task,omitempty" jsonschema:"only runs of this task"`
	Status string `json:"status,omitempty" jsonschema:"only runs in this status, as the runs API accepts it"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max runs to return (default 20)"`
}

// runItem is a run in the history list: the shared last-run document plus the
// task it belongs to.
type runItem struct {
	Task string `json:"task"`
	lastRunJSON
}

func (m *mcpServer) runs(ctx context.Context, _ *mcp.CallToolRequest, in runsInput) (*mcp.CallToolResult, any, error) {
	c, err := m.client(ctx)
	if err != nil {
		return nil, nil, err
	}
	params := apiclient.RunsParams{TaskName: in.Task, Status: in.Status, Limit: in.Limit}
	if params.Limit <= 0 {
		params.Limit = mcpDefaultRunsLimit
	}
	var (
		runs  []model.Run
		total int64
	)
	if err := m.withSession(ctx, c, func() (err error) {
		runs, total, err = c.ListRuns(ctx, params)
		return err
	}); err != nil {
		return nil, nil, err
	}
	items := make([]runItem, 0, len(runs))
	for i := range runs {
		items = append(items, runItem{Task: runs[i].TaskName, lastRunJSON: newLastRunJSON(&runs[i])})
	}
	return jsonResult(struct {
		SchemaVersion int       `json:"schemaVersion"`
		Total         int64     `json:"total"`
		Runs          []runItem `json:"runs"`
	}{jsonSchemaVersion, total, items})
}

type logsInput struct {
	Target string `json:"target" jsonschema:"task name, glob such as 'backup-*', or run ID"`
	Lines  int    `json:"lines,omitempty" jsonschema:"last N lines of each run (default 100, max 1000)"`
}

func (m *mcpServer) logs(ctx context.Context, _ *mcp.CallToolRequest, in logsInput) (*mcp.CallToolResult, any, error) {
	n := mcpDefaultLogLines
	if in.Lines > 0 {
		n = min(in.Lines, mcpMaxLogLines)
	}
	opts, err := parseLogsOptions(false, strconv.Itoa(n), true)
	if err != nil {
		return nil, nil, err
	}
	var out bytes.Buffer
	if err := runLogs(ctx, &out, io.Discard, m.f, m.rf, []string{in.Target}, opts); err != nil {
		return nil, nil, err
	}
	return textResult(out.String(), false), nil, nil
}

type searchLogsInput struct {
	Task          string `json:"task"`
	Query         string `json:"query"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	RunID         string `json:"runId,omitempty" jsonschema:"search only this run"`
	Limit         int    `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous result"`
}

func (m *mcpServer) searchLogs(ctx context.Context, _ *mcp.CallToolRequest, in searchLogsInput) (*mcp.CallToolResult, any, error) {
	c, err := m.client(ctx)
	if err != nil {
		return nil, nil, err
	}
	opts := apiclient.SearchLogsOptions{Query: in.Query, Regex: in.Regex, CaseSensitive: in.CaseSensitive, RunID: in.RunID, Limit: in.Limit, Cursor: in.Cursor}
	var res any
	if err := m.withSession(ctx, c, func() (err error) {
		res, err = c.SearchLogs(ctx, in.Task, opts)
		return err
	}); err != nil {
		return nil, nil, err
	}
	return jsonResult(res)
}

type runTaskInput struct {
	Task           string            `json:"task"`
	Params         map[string]string `json:"params,omitempty" jsonschema:"task parameters; unset ones use their declared default"`
	NoWait         bool              `json:"noWait,omitempty" jsonschema:"return as soon as the run is triggered instead of waiting for it to end"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait for the run (default 300); the run keeps going after a timeout"`
}

func (m *mcpServer) runTask(ctx context.Context, _ *mcp.CallToolRequest, in runTaskInput) (*mcp.CallToolResult, any, error) {
	var out bytes.Buffer
	run, err := m.trigger(ctx, in)
	if err != nil {
		_ = writeJSON(&out, newExecErrorJSONDoc(in.Task, err))
		return docResult(&out, err)
	}
	if in.NoWait {
		_ = writeJSON(&out, newExecJSONDoc(in.Task, run))
		return docResult(&out, nil)
	}
	timeout := mcpDefaultWait
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := m.client(ctx)
	if err != nil {
		return nil, nil, err
	}
	// Output is read through the logs tool, and stdout is the protocol channel,
	// so the live lines are discarded here (stderr-stream lines go to stderr).
	// After a timeout followRun reports the run's persisted state, still
	// "running", and the run carries on.
	_, final, err := followRun(waitCtx, c, in.Task, run.ID, io.Discard)
	if err != nil && waitCtx.Err() != nil && ctx.Err() == nil {
		final, err = c.GetRun(ctx, run.ID)
	}
	if err != nil {
		return nil, nil, err
	}
	_ = writeJSON(&out, newExecJSONDoc(in.Task, final))
	return docResult(&out, nil)
}

func (m *mcpServer) trigger(ctx context.Context, in runTaskInput) (*model.Run, error) {
	c, err := m.client(ctx)
	if err != nil {
		return nil, err
	}
	params := make(map[string]*string, len(in.Params))
	for k, v := range in.Params {
		params[k] = &v
	}
	return triggerRemote(ctx, c, in.Task, m.baseURL, m.password, params)
}

type stopRunInput struct {
	RunID string `json:"runId"`
}

func (m *mcpServer) stopRun(ctx context.Context, _ *mcp.CallToolRequest, in stopRunInput) (*mcp.CallToolResult, any, error) {
	c, err := m.client(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := m.withSession(ctx, c, func() error { return c.StopRun(ctx, in.RunID) }); err != nil {
		return nil, nil, controlError(err, m.baseURL, "stop", in.RunID, true)
	}
	return textResult(fmt.Sprintf("stop requested for run %s", in.RunID), false), nil, nil
}

func (m *mcpServer) reload(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	c, err := m.client(ctx)
	if err != nil {
		return nil, nil, err
	}
	var res *model.ReloadResult
	if err := m.withSession(ctx, c, func() (err error) {
		res, err = c.Reload(ctx)
		return err
	}); err != nil {
		return nil, nil, err
	}
	return jsonResult(res)
}

func (m *mcpServer) list(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	var out bytes.Buffer
	return docResult(&out, runList(&out, m.f, true))
}

func (m *mcpServer) validate(_ context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
	var out bytes.Buffer
	return docResult(&out, runValidate(&out, m.f, true))
}
