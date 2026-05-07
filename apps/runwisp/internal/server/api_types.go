// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

// ---------- Path / query inputs ----------

type TaskNameInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
}

type TaskRunInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
	RunID    string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
}

type RunsQueryInput struct {
	Limit         int    `query:"limit" minimum:"1" maximum:"1000" default:"50" doc:"Max results per page"`
	Offset        int    `query:"offset" minimum:"0" default:"0" doc:"Pagination offset"`
	Status        string `query:"status" doc:"Filter by run status"`
	TaskName      string `query:"task_name" doc:"Filter by task name"`
	SortField     string `query:"sort_field" enum:"task_name,status,start_at,exit_code,duration,created_at," doc:"Field to sort by"`
	SortDirection string `query:"sort_direction" enum:"asc,desc," doc:"Sort direction"`
	Search        string `query:"search" doc:"Search query"`
}

func (q *RunsQueryInput) toPaginationParams() PaginationParams {
	return PaginationParams{
		Limit:         q.Limit,
		Offset:        q.Offset,
		Status:        q.Status,
		TaskName:      q.TaskName,
		SortField:     q.SortField,
		SortDirection: q.SortDirection,
		SearchQuery:   q.Search,
	}
}

type TaskRunsQueryInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
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
// distinct type even though the payload shape is identical.

type RunCreatedEvent events.RunEvent
type RunStartedEvent events.RunEvent
type RunCompletedEvent events.RunEvent
type RunFailedEvent events.RunEvent
type RunUpdatedEvent events.RunEvent
type PingEvent struct{}

// LogLineSSEEvent is the per-line payload for the run-log stream. Identical
// shape to LogLineEntry; aliased so huma/sse's reverse-type lookup can map it
// to the `line` event name.
type LogLineSSEEvent LogLineEntry

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
