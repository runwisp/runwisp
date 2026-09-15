// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server/logstream"
	"github.com/runwisp/runwisp/internal/storage"
)

// ---------- Path / query inputs ----------

type TaskNameInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._:-]+$" doc:"Task name"`
}

// TriggerWaitTimeoutMax and TriggerWaitTimeoutDefault back the waitTimeout
// field's huma `maximum`/`default` struct tags below. Struct tags are string
// literals, so a tag edit can't reference these constants directly — they're
// kept in sync by hand, guarded by TestTriggerRunInput_WaitTimeoutBounds.
const (
	// TriggerWaitTimeoutMax must stay safely under the HTTP server's 5-minute
	// WriteTimeout (internal/server/server.go), with real margin for request/
	// dispatch overhead. The wait=true handler makes exactly one JSON write,
	// after blocking for up to waitTimeout seconds, on a plain
	// (non-streaming) response whose write deadline is set once — when
	// headers finish being read — and never refreshed afterward. Wait too
	// close to (or past) WriteTimeout and that final write fails with an i/o
	// timeout even though the triggered run completed successfully.
	TriggerWaitTimeoutMax = 240
	// TriggerWaitTimeoutDefault is a shorter, comfortable default for a
	// "trigger and wait a bit" convenience call.
	TriggerWaitTimeoutDefault = 120
)

// TriggerRunInput drives POST /api/tasks/{taskName}/run. With wait=false
// (default) it returns immediately with the pending run; with wait=true the
// request blocks until the run finishes so a single call yields its exit code.
// The body carries optional per-execution parameter values for a manual
// trigger; it is a pointer so a zero-param POST (the common case) still works
// with no payload. Flag/option names are never accepted here — only values
// keyed by the parameter identities declared in runwisp.toml.
type TriggerRunInput struct {
	TaskName    string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._:-]+$" doc:"Task name"`
	Wait        bool   `query:"wait" doc:"Block until the run finishes and return the completed run (with exitCode and endReason). Best for short tasks; long runs may exceed reverse-proxy timeouts — follow the log stream or poll instead."`
	WaitTimeout int    `query:"waitTimeout" minimum:"1" maximum:"240" default:"120" doc:"With wait=true, the maximum seconds to hold the request open. Capped below the server's 5-minute write timeout (with margin for request/dispatch overhead) since the response is a single write made after the full wait elapses. On timeout the run keeps running and the response returns it in its current (non-terminal) state."`
	// Via lets a first-party caller (Web UI, TUI, CLI) declare its own
	// provenance so the run history can tell "someone clicked Run Now" apart
	// from a raw REST call. Purely a label — a scripted caller can set this
	// too, so nothing is authorized off it. Empty (the default, and the only
	// option for a plain REST caller) records `api`.
	Via string `query:"via" enum:"ui,cli," doc:"Declares the caller for run provenance: 'ui' (Web UI / TUI Run Now) or 'cli' (runwisp run). Omit for a plain API call."`
	// Body is a pointer so it is optional — a zero-param trigger can POST with
	// no payload at all.
	Body *struct {
		Params map[string]*string `json:"params,omitempty" doc:"Values for the task's declared parameters, keyed by parameter identity. A null value omits that parameter (overriding its default); an empty string passes an empty value; an absent key uses the declared default."`
	}
}

// RunIDInput drives the per-run endpoints (GET/DELETE /api/runs/{runId},
// .../stop, .../log*) — a run ULID is globally unique, so addressing one needs
// no task name. Lets the cross-task /runs view restore a deep-linked run that
// isn't on the loaded page.
type RunIDInput struct {
	RunID string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
}

// RunsQueryInput cannot simply embed model.RunFilter: huma panics at startup
// on a pointer-typed query/path/header field, but RunFilter's CreatedAfter/
// CreatedBefore/ExitCodeMin/ExitCodeMax are pointers so the JSON body (used by
// RunSelector) can omit them correctly. Every field here still names and
// documents the same filter RunFilter does — TestRunsQueryInputCoversRunFilter
// asserts the two can't silently drift.
type RunsQueryInput struct {
	Limit         int       `query:"limit" minimum:"1" maximum:"1000" default:"50" doc:"Max results per page"`
	Offset        int       `query:"offset" minimum:"0" default:"0" doc:"Pagination offset"`
	Status        string    `query:"status" doc:"Comma-separated run statuses (phase or end reason); a run matches any listed value"`
	TaskName      string    `query:"taskName" doc:"Filter by task name"`
	TriggeredBy   string    `query:"triggeredBy" enum:"cron,api,ui,cli,cloud,service,startup," doc:"Filter by what triggered the run"`
	CreatedAfter  time.Time `query:"createdAfter" doc:"Only runs created at or after this RFC3339 time"`
	CreatedBefore time.Time `query:"createdBefore" doc:"Only runs created at or before this RFC3339 time"`
	ExitCodeMin   string    `query:"exitCodeMin" pattern:"^-?[0-9]+$" doc:"Only runs whose exit code is >= this (inclusive)"`
	ExitCodeMax   string    `query:"exitCodeMax" pattern:"^-?[0-9]+$" doc:"Only runs whose exit code is <= this (inclusive)"`
	RetriesOnly   bool      `query:"retriesOnly" doc:"Only runs that are a retry (retry_attempt > 0)"`
	// IsFailure mirrors model.RunFilter.IsFailure. Equivalent to putting
	// model.FailureStatusToken in Status, spelled out as its own boolean so an
	// API client doesn't need to know the token to ask for "failures only".
	IsFailure     bool   `query:"isFailure" doc:"Also match runs classified as a failure (per-task failures policy)"`
	SortField     string `query:"sortField" enum:"taskName,status,startedAt,exitCode,duration,createdAt," doc:"Field to sort by"`
	SortDirection string `query:"sortDirection" enum:"asc,desc," doc:"Sort direction"`
	Search        string `query:"search" doc:"Search query"`
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
		IsFailure:   q.IsFailure,
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

// ---------- Response outputs ----------

type TasksOutput struct {
	Body TasksResponseBody
}

type TasksResponseBody struct {
	Items []model.TaskResponse `json:"items" doc:"List of tasks"`
}

type TaskOutput struct {
	Body model.TaskResponse
}

type RunsOutput struct {
	Body RunsResponseBody
}

type RunsResponseBody struct {
	Items []model.Run `json:"items" doc:"List of runs"`
	Total int64       `json:"total" doc:"Total matching runs"`
}

type RunOutput struct {
	Body model.Run
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
	Affected int      `json:"affected" doc:"Number of rows the operation touched"`
	Skipped  []string `json:"skipped,omitempty" doc:"Run IDs left untouched because they are still active (delete only)"`
}

type BulkRerunOutput struct {
	Body BulkRerunBody
}

type BulkRerunBody struct {
	Triggered []TriggeredRunRef `json:"triggered" doc:"New runs spawned by the rerun, keyed by task"`
}

type TriggeredRunRef struct {
	TaskName string `json:"taskName"`
	RunID    string `json:"runId"`
}

type DaemonInfoOutput struct {
	Body model.DaemonInfo
}

type SystemStatsOutput struct {
	Body model.SystemStats
}

type MetricsHistoryOutput struct {
	Body MetricsHistoryBody
}

type MetricsHistoryBody struct {
	Items []model.MetricsSample `json:"items" doc:"Resource snapshots, oldest first"`
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
	AuthRequired  bool `json:"authRequired" doc:"Whether authentication is required"`
	Authenticated bool `json:"authenticated" doc:"Whether the current request is already authenticated via cookie"`
}

type AuthChallengeOutput struct {
	Body AuthChallengeBody
}

type AuthChallengeBody struct {
	Nonce string `json:"nonce" doc:"Challenge nonce (hex)"`
}

// AuthStatusInput reads the session cookie. The tag must match
// auth.CookieName literally — huma param tags are compile-time string
// literals and can't reference the constant.
type AuthStatusInput struct {
	Token string `cookie:"runwisp_jwt"`
}

type AuthLoginInput struct {
	Body AuthLoginRequest
}

type AuthLoginRequest struct {
	Nonce    string `json:"nonce" doc:"Challenge nonce returned by GET /api/auth/challenge"`
	Response string `json:"response" doc:"chap.Response(password, nonce) — proves knowledge of the password without sending it"`
}

// AuthLoginOutput carries the session both ways: as a Set-Cookie header for
// browser clients, and in the body for TCP API clients that hold their own
// token and send it as a Bearer header instead of relying on cookies.
type AuthLoginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      AuthLoginBody
}

type AuthLoginBody struct {
	Token string `json:"token" doc:"JWT session token, also set as the runwisp_jwt cookie"`
}

type LaunchTicketBody struct {
	Ticket string `json:"ticket" doc:"Single-use, short-TTL ticket. Redeem via GET /api/auth/launch-ticket?ticket=..."`
}

type LaunchTicketMintOutput struct {
	Body LaunchTicketBody
}

type LaunchTicketRedeemInput struct {
	Ticket   string `query:"ticket" required:"true" doc:"Single-use ticket minted by POST /api/auth/launch-ticket"`
	Redirect string `query:"redirect" doc:"Same-origin absolute path to land on after the session cookie is set; defaults to /"`
}

// LaunchTicketRedeemOutput has no Body: huma skips body-writing entirely when
// no Body field is present, which is correct for a redirect response.
type LaunchTicketRedeemOutput struct {
	Status    int
	Location  string      `header:"Location"`
	SetCookie http.Cookie `header:"Set-Cookie"`
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

// The run-log stream events are named types over their logstream source types so
// huma/sse's reverse-type lookup can map each to its event name (`region`,
// `rotated`, `dropped`, `done`) while the wire shape (json + doc tags) stays
// defined in exactly one place and can never drift.
type LogRegionSSEEvent logstream.RegionEvent
type LogRotatedEvent logstream.RotatedEvent
type LogDroppedEvent logstream.DroppedEvent
type LogDoneEvent logstream.DoneEvent

type DaemonLogLineEvent struct {
	Line string `json:"line" doc:"One captured daemon log line"`
}
