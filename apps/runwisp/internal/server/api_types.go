// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"strconv"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// ---------- Path / query inputs ----------

type TaskNameInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._:-]+$" doc:"Task name"`
}

// TriggerRunInput drives POST /api/tasks/{taskName}/run. With wait=false
// (default) it returns immediately with the pending run; with wait=true the
// request blocks until the run finishes so a single call yields its exit code.
// The body carries optional per-execution parameter values for a manual
// trigger; it is a pointer so a zero-param POST (the common case) still works
// with no payload. Flag/option names are never accepted here — only values
// keyed by the parameter identities declared in runwisp.toml.
type TriggerRunInput struct {
	TaskName    string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._:-]+$" doc:"Task name"`
	Wait        bool   `query:"wait" doc:"Block until the run finishes and return the completed run (with exit_code and end_reason). Best for short tasks; long runs may exceed reverse-proxy timeouts — follow the log stream or poll instead."`
	WaitTimeout int    `query:"wait_timeout" minimum:"1" maximum:"3600" default:"300" doc:"With wait=true, the maximum seconds to hold the request open. On timeout the run keeps running and the response returns it in its current (non-terminal) state."`
	// Body is a pointer so it is optional — a zero-param trigger can POST with
	// no payload at all.
	Body *struct {
		Params map[string]*string `json:"params,omitempty" doc:"Values for the task's declared parameters, keyed by parameter identity. A null value omits that parameter (overriding its default); an empty string passes an empty value; an absent key uses the declared default."`
	}
}

type TaskRunInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._:-]+$" doc:"Task name"`
	RunID    string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
}

// RunIDInput drives GET /api/runs/{runId} — a run ULID is globally unique, so
// fetching one needs no task name. Lets the cross-task /runs view restore a
// deep-linked run that isn't on the loaded page.
type RunIDInput struct {
	RunID string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
}

type RunsQueryInput struct {
	Limit         int       `query:"limit" minimum:"1" maximum:"1000" default:"50" doc:"Max results per page"`
	Offset        int       `query:"offset" minimum:"0" default:"0" doc:"Pagination offset"`
	Status        string    `query:"status" doc:"Comma-separated run statuses (phase or end reason); a run matches any listed value"`
	TaskName      string    `query:"task_name" doc:"Filter by task name"`
	TriggeredBy   string    `query:"triggered_by" enum:"cron,api,cloud,service,startup," doc:"Filter by what triggered the run"`
	CreatedAfter  time.Time `query:"created_after" doc:"Only runs created at or after this RFC3339 time"`
	CreatedBefore time.Time `query:"created_before" doc:"Only runs created at or before this RFC3339 time"`
	ExitCodeMin   string    `query:"exit_code_min" pattern:"^-?[0-9]+$" doc:"Only runs whose exit code is >= this (inclusive)"`
	ExitCodeMax   string    `query:"exit_code_max" pattern:"^-?[0-9]+$" doc:"Only runs whose exit code is <= this (inclusive)"`
	RetriesOnly   bool      `query:"retries_only" doc:"Only runs that are a retry (retry_attempt > 0)"`
	SortField     string    `query:"sort_field" enum:"task_name,status,start_at,exit_code,duration,created_at," doc:"Field to sort by"`
	SortDirection string    `query:"sort_direction" enum:"asc,desc," doc:"Sort direction"`
	Search        string    `query:"search" doc:"Search query"`
}

// toPaginationParams parses the raw query strings into a typed RunFilter plus
// the storage sort handles. Malformed values are silently coerced to the open
// gate / safe default: the huma enum and pattern tags on RunsQueryInput
// already reject out-of-range values at the HTTP boundary, so any error here
// means a programming bug (e.g. enum drift) and the field is left unset.
func (q *RunsQueryInput) toPaginationParams() PaginationParams {
	sortCol, _ := storage.ParseSortColumn(q.SortField)
	sortDir, _ := storage.ParseSortDirection(q.SortDirection)

	filter := model.RunFilter{
		Status:      q.Status,
		TaskName:    q.TaskName,
		Search:      q.Search,
		TriggeredBy: q.TriggeredBy,
		RetriesOnly: q.RetriesOnly,
	}
	// time.Time query params parse as RFC3339; an absent param stays zero.
	if !q.CreatedAfter.IsZero() {
		after := q.CreatedAfter
		filter.CreatedAfter = &after
	}
	if !q.CreatedBefore.IsZero() {
		before := q.CreatedBefore
		filter.CreatedBefore = &before
	}
	// The exit-code bounds are strings (not ints) so an absent filter is
	// distinguishable from an explicit bound of 0; the pattern tag guarantees
	// each is a valid integer when present.
	if q.ExitCodeMin != "" {
		if code, err := strconv.Atoi(q.ExitCodeMin); err == nil {
			filter.ExitCodeMin = &code
		}
	}
	if q.ExitCodeMax != "" {
		if code, err := strconv.Atoi(q.ExitCodeMax); err == nil {
			filter.ExitCodeMax = &code
		}
	}

	return PaginationParams{
		Limit:         q.Limit,
		Offset:        q.Offset,
		Filter:        filter,
		SortField:     sortCol,
		SortDirection: sortDir,
	}
}

type TaskRunsQueryInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._:-]+$" doc:"Task name"`
	RunsQueryInput
}

// ---------- Response outputs ----------

type TasksOutput struct {
	Body []model.TaskResponse
}

type RunsOutput struct {
	Body RunsResponseBody
}

type RunsResponseBody struct {
	Runs  []model.Run `json:"runs" doc:"List of runs"`
	Total int64       `json:"total" doc:"Total matching runs"`
}

type RunOutput struct {
	Body model.Run
}

type TriggerRunOutput struct {
	Body model.Run
}

type StopRunOutput struct {
	Body struct {
		Message string `json:"message" doc:"Result message"`
	}
}

// BulkRunSelectorInput is the shared request body for every /api/runs/bulk/*
// endpoint. The selector is the same object the UI builds locally and any
// other client could reuse — one shape, four operations.
type BulkRunSelectorInput struct {
	Body model.RunSelector
}

type BulkAffectedOutput struct {
	Body BulkAffectedBody
}

type BulkAffectedBody struct {
	Affected int `json:"affected" doc:"Number of rows the operation touched"`
}

type BulkRerunOutput struct {
	Body BulkRerunBody
}

type BulkRerunBody struct {
	Triggered []TriggeredRunRef `json:"triggered" doc:"New runs spawned by the rerun, keyed by task"`
}

type TriggeredRunRef struct {
	TaskName string `json:"task_name"`
	RunID    string `json:"run_id"`
}

type DaemonInfoOutput struct {
	Body model.DaemonInfo
}

type SystemStatsOutput struct {
	Body model.SystemStats
}

type MetricsHistoryOutput struct {
	Body []model.MetricsSample
}

type RunSummaryOutput struct {
	Body model.RunSummary
}

type ReloadOutput struct {
	Body model.ReloadResult
}

// ---------- Auth types ----------

type AuthStatusOutput struct {
	Body AuthStatusBody
}

type AuthStatusBody struct {
	AuthRequired  bool `json:"auth_required" doc:"Whether authentication is required"`
	Authenticated bool `json:"authenticated" doc:"Whether the current request is already authenticated via cookie"`
}

type AuthChallengeOutput struct {
	Body AuthChallengeBody
}

type AuthChallengeBody struct {
	Nonce string `json:"nonce" doc:"Challenge nonce (hex)"`
}

// ---------- SSE event wrapper types ----------
// huma/sse dispatches event names by Go type, so each SSE event needs a
// distinct type even though the payload shape is identical. The wire shape
// embeds model.Run (not events.RunEvent's *model.Run) so the SSE stream
// matches the REST response shape exactly.

type RunEventBody struct {
	Run   *model.Run `json:"run"`
	Error string     `json:"error,omitempty"`
}

type RunCreatedEvent RunEventBody
type RunStartedEvent RunEventBody
type RunCompletedEvent RunEventBody
type RunFailedEvent RunEventBody
type RunUpdatedEvent RunEventBody
type RunDeletedSSEEvent events.RunDeletedEvent
type PingEvent struct{}

// SystemSampleSSEEvent is the periodic resource snapshot for the app event
// stream. Mirrors events.SystemSampleEvent; aliased so huma/sse's reverse-type
// lookup maps it to the `system` event name.
type SystemSampleSSEEvent struct {
	Sample model.MetricsSample `json:"sample" doc:"Resource snapshot, same shape as a metrics-history entry"`
	Uptime string              `json:"uptime" doc:"Human-readable daemon uptime"`
}

// ConfigStaleSSEEvent fires when config-staleness flips; maps to the
// `config.stale` event name.
type ConfigStaleSSEEvent struct {
	Stale bool `json:"stale" doc:"True when runwisp.toml changed on disk but isn't applied yet"`
}

// LogLineSSEEvent is the per-line payload for the run-log stream. Identical
// shape to LogLineEntry; aliased so huma/sse's reverse-type lookup can map it
// to the `line` event name.
type LogLineSSEEvent LogLineEntry

// LogRegionSSEEvent is the live-region snapshot payload for the run-log stream.
// Identical shape to logstream.RegionEvent; aliased so huma/sse's reverse-type
// lookup can map it to the `region` event name.
type LogRegionSSEEvent struct {
	Stream string   `json:"stream" doc:"Stream identifier (stdout/stderr)"`
	Epoch  int      `json:"epoch" doc:"Region generation; bumps on screen reset so stale frames can be discarded"`
	Rows   []string `json:"rows" doc:"Current frame of the region, one entry per row; empty clears the overlay"`
}

type LogRotatedEvent struct {
	FirstAvailable int64 `json:"first_available" doc:"Lowest line number still on disk after rotation"`
}

type LogDroppedEvent struct {
	After int64 `json:"after" doc:"Highest line number observed before drops occurred"`
	Count int64 `json:"count" doc:"Number of line events dropped due to overflow"`
}

type LogDoneEvent struct {
	FinalLine int64  `json:"final_line" doc:"Last line number emitted before the run terminated"`
	Status    string `json:"status" doc:"Reason the stream is closing (e.g. 'ended')"`
}

type DaemonLogLineEvent struct {
	Line string `json:"line" doc:"One captured daemon log line"`
}
