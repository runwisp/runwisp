// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net/http"
	"time"

	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/go-chi/chi/v5"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/storage"
)

type PaginationParams struct {
	Limit, Offset                 int
	Status, TaskName, SearchQuery string
	SortField                     storage.SortColumn
	SortDirection                 storage.SortDirection
}

func (srv *Server) registerProtectedHumaRoutes(r chi.Router) {
	cfg := huma.DefaultConfig("", "")
	cfg.OpenAPI = srv.api.OpenAPI()
	protectedAPI := humachi.New(r, cfg)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getInfo",
		Method:      http.MethodGet,
		Path:        "/api/info",
		Summary:     "Get daemon info",
		Tags:        []string{"System"},
	}, srv.humaGetInfo)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getSystemStats",
		Method:      http.MethodGet,
		Path:        "/api/system",
		Summary:     "Get system statistics",
		Tags:        []string{"System"},
	}, srv.humaGetSystemStats)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "reload",
		Method:      http.MethodPost,
		Path:        "/api/reload",
		Summary:     "Reload runwisp.toml",
		Description: "Re-reads the config file and reconciles the live task set (added/changed/removed). Validate-first: a config that fails to load or changes a restart-only setting is rejected and nothing is applied.",
		Tags:        []string{"System"},
	}, srv.humaReload)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getMetricsHistory",
		Method:      http.MethodGet,
		Path:        "/api/system/history",
		Summary:     "Get historical system metrics",
		Tags:        []string{"System"},
	}, srv.humaGetMetricsHistory)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getTasks",
		Method:      http.MethodGet,
		Path:        "/api/tasks",
		Summary:     "List all tasks",
		Tags:        []string{"Tasks"},
	}, srv.humaGetTasks)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getAllRuns",
		Method:      http.MethodGet,
		Path:        "/api/runs",
		Summary:     "List all runs",
		Tags:        []string{"Runs"},
	}, srv.humaGetAllRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getRunSummary",
		Method:      http.MethodGet,
		Path:        "/api/runs/summary",
		Summary:     "Get aggregate run statistics",
		Tags:        []string{"Runs"},
	}, srv.humaGetRunSummary)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getTaskRuns",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/runs",
		Summary:     "List runs for a task",
		Tags:        []string{"Runs"},
	}, srv.humaGetTaskRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getRun",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/runs/{runId}",
		Summary:     "Get a single run",
		Tags:        []string{"Runs"},
	}, srv.humaGetRun)

	huma.Register(protectedAPI, huma.Operation{
		OperationID:   "triggerRun",
		Method:        http.MethodPost,
		Path:          "/api/tasks/{taskName}/run",
		Summary:       "Trigger a new run",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusCreated,
	}, srv.humaTriggerRun)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "restartService",
		Method:      http.MethodPost,
		Path:        "/api/tasks/{taskName}/restart",
		Summary:     "Restart all instances of a service",
		Tags:        []string{"Runs"},
	}, srv.humaRestartService)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "stopService",
		Method:      http.MethodPost,
		Path:        "/api/tasks/{taskName}/stop",
		Summary:     "Stop a service for the daemon's lifetime",
		Description: "Cancels every live instance and marks the service stopped. The supervisor stops refilling slots until a restart is issued or the daemon is restarted.",
		Tags:        []string{"Runs"},
	}, srv.humaStopService)

	huma.Register(protectedAPI, huma.Operation{
		OperationID:   "deleteRun",
		Method:        http.MethodDelete,
		Path:          "/api/tasks/{taskName}/runs/{runId}",
		Summary:       "Delete a run",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaDeleteRun)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "stopRun",
		Method:      http.MethodPost,
		Path:        "/api/tasks/{taskName}/runs/{runId}/stop",
		Summary:     "Stop a running task",
		Tags:        []string{"Runs"},
	}, srv.humaStopRun)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "bulkDeleteRuns",
		Method:      http.MethodPost,
		Path:        "/api/runs/bulk/delete",
		Summary:     "Soft-delete every run matched by the selector",
		Description: "Marks matching terminal runs as deleted. Rows remain on disk for a short undo window before the purger reclaims them.",
		Tags:        []string{"Runs"},
	}, srv.humaBulkDeleteRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "bulkRestoreRuns",
		Method:      http.MethodPost,
		Path:        "/api/runs/bulk/restore",
		Summary:     "Restore soft-deleted runs matching the selector",
		Tags:        []string{"Runs"},
	}, srv.humaBulkRestoreRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "bulkCancelRuns",
		Method:      http.MethodPost,
		Path:        "/api/runs/bulk/cancel",
		Summary:     "Cancel every running run matched by the selector",
		Tags:        []string{"Runs"},
	}, srv.humaBulkCancelRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "bulkRerunRuns",
		Method:      http.MethodPost,
		Path:        "/api/runs/bulk/rerun",
		Summary:     "Re-run the unique tasks behind the selector's runs",
		Tags:        []string{"Runs"},
	}, srv.humaBulkRerunRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getLogPage",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/runs/{runId}/log",
		Summary:     "Get a page of log lines",
		Description: "Returns a JSON page of absolute-line-numbered log entries. Use `from` (negative for tail) and `limit` to window the result.",
		Tags:        []string{"Logs"},
	}, srv.humaGetLogPage)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getLogRaw",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/runs/{runId}/log/raw",
		Summary:     "Download the run's full log as text/plain",
		Description: "Concatenates the rotated-away segment (`.log.prev`) and current segment so a single download captures the operator-visible byte stream.",
		Tags:        []string{"Logs"},
	}, srv.humaGetLogRaw)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "searchLogs",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/log/search",
		Summary:     "Search log lines across runs of a task",
		Description: "Streams on disk through the task's runs newest-first and returns matching lines. Pure on-demand scan; no index is maintained. Use `cursor` to paginate beyond the per-request hit/run budget.",
		Tags:        []string{"Logs"},
	}, srv.humaSearchLogs)

	srv.registerLocalCredentialsRoute(protectedAPI)
	srv.registerRunsSSE(protectedAPI)
	srv.registerLogSSE(protectedAPI)
	srv.registerDaemonLogSSE(protectedAPI)
	srv.registerNotificationsRoutes(protectedAPI)
}

func (srv *Server) humaGetTasks(ctx context.Context, input *struct{}) (*TasksOutput, error) {
	return &TasksOutput{Body: srv.runService.ListTasks()}, nil
}

func (srv *Server) humaGetAllRuns(ctx context.Context, input *RunsQueryInput) (*RunsOutput, error) {
	p := input.toPaginationParams()
	result, err := srv.runService.ListRuns(ctx, "", p)
	if err != nil {
		return nil, mapDomainError(err, "Failed to get runs")
	}
	return &RunsOutput{Body: RunsResponseBody{Runs: result.Runs, Total: result.Total}}, nil
}

func (srv *Server) humaGetRunSummary(ctx context.Context, input *struct{}) (*RunSummaryOutput, error) {
	summary, err := srv.db.GetRunSummary(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get run summary")
	}
	return &RunSummaryOutput{Body: *summary}, nil
}

func (srv *Server) humaGetTaskRuns(ctx context.Context, input *TaskRunsQueryInput) (*RunsOutput, error) {
	p := input.toPaginationParams()
	result, err := srv.runService.ListRuns(ctx, input.TaskName, p)
	if err != nil {
		return nil, mapDomainError(err, "Failed to get runs")
	}
	return &RunsOutput{Body: RunsResponseBody{Runs: result.Runs, Total: result.Total}}, nil
}

func (srv *Server) humaTriggerRun(ctx context.Context, input *TaskNameInput) (*TriggerRunOutput, error) {
	run, err := srv.runService.TriggerRun(ctx, input.TaskName)
	if err != nil {
		return nil, mapDomainError(err, "Failed to trigger run")
	}
	return &TriggerRunOutput{Body: *run}, nil
}

func (srv *Server) humaRestartService(ctx context.Context, input *TaskNameInput) (*StopRunOutput, error) {
	if err := srv.runService.RestartService(input.TaskName); err != nil {
		return nil, mapDomainError(err, "Failed to restart service")
	}
	out := &StopRunOutput{}
	out.Body.Message = "Service instances restarting"
	return out, nil
}

func (srv *Server) humaStopService(ctx context.Context, input *TaskNameInput) (*StopRunOutput, error) {
	if err := srv.runService.StopService(input.TaskName); err != nil {
		return nil, mapDomainError(err, "Failed to stop service")
	}
	out := &StopRunOutput{}
	out.Body.Message = "Service stopped"
	return out, nil
}

func (srv *Server) humaGetRun(ctx context.Context, input *TaskRunInput) (*RunOutput, error) {
	run, err := srv.runService.GetRun(ctx, input.RunID)
	if err != nil {
		return nil, mapDomainError(err, "Failed to fetch run")
	}
	return &RunOutput{Body: *run}, nil
}

func (srv *Server) humaDeleteRun(ctx context.Context, input *TaskRunInput) (*struct{}, error) {
	if err := srv.runService.DeleteRun(ctx, input.RunID); err != nil {
		return nil, mapDomainError(err, "Failed to delete run")
	}
	return nil, nil
}

func (srv *Server) humaStopRun(ctx context.Context, input *TaskRunInput) (*StopRunOutput, error) {
	if err := srv.runService.StopRun(ctx, input.RunID); err != nil {
		return nil, mapDomainError(err, "Failed to stop run")
	}
	out := &StopRunOutput{}
	out.Body.Message = "Run stop signal sent"
	return out, nil
}

func (srv *Server) humaBulkDeleteRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkAffectedOutput, error) {
	n, err := srv.runService.bulkSoftDelete(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(err, "Failed to delete runs")
	}
	return &BulkAffectedOutput{Body: BulkAffectedBody{Affected: n}}, nil
}

func (srv *Server) humaBulkRestoreRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkAffectedOutput, error) {
	n, err := srv.runService.bulkRestore(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(err, "Failed to restore runs")
	}
	return &BulkAffectedOutput{Body: BulkAffectedBody{Affected: n}}, nil
}

func (srv *Server) humaBulkCancelRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkAffectedOutput, error) {
	n, err := srv.runService.bulkCancel(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(err, "Failed to cancel runs")
	}
	return &BulkAffectedOutput{Body: BulkAffectedBody{Affected: n}}, nil
}

func (srv *Server) humaBulkRerunRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkRerunOutput, error) {
	triggered, err := srv.runService.bulkRerun(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(err, "Failed to rerun")
	}
	return &BulkRerunOutput{Body: BulkRerunBody{Triggered: triggered}}, nil
}

func (srv *Server) registerRunsSSE(api huma.API) {
	sse.Register(api, huma.Operation{
		OperationID: "streamRuns",
		Method:      http.MethodGet,
		Path:        "/api/runs/stream",
		Summary:     "Stream run lifecycle events",
		Description: "Server-Sent Events stream of run creation, start, completion, failure and update events.",
		Tags:        []string{"Runs"},
	}, map[string]any{
		"run.created":   RunCreatedEvent{},
		"run.started":   RunStartedEvent{},
		"run.completed": RunCompletedEvent{},
		"run.failed":    RunFailedEvent{},
		"run.updated":   RunUpdatedEvent{},
		"run.deleted":   RunDeletedSSEEvent{},
		"ping":          PingEvent{},
	}, srv.streamRunsHandler)
}

// streamRunsHandler is the SSE callback for the run lifecycle stream. It admits
// the client (subject to the stream limit), flushes headers, then relays run
// events and periodic pings until the client disconnects.
func (srv *Server) streamRunsHandler(ctx context.Context, _ *struct{}, send sse.Sender) {
	release, ok := srv.streams.acquire(streamClientIPFromCtx(ctx))
	if !ok {
		// huma owns the response writer here, so we communicate refusal via
		// the SSE channel and return; the client will see a single ping then
		// EOF, which the UI already handles as a closed stream.
		return
	}
	defer release()

	// Flush response headers immediately so SSE clients (e.g. EventSource
	// polyfill using fetch) receive the 200 + text/event-stream header
	// without waiting for the first real event.
	if err := send(sse.Message{Data: PingEvent{}}); err != nil {
		return
	}

	eventChan := make(chan events.Event, 10)
	unsubscribe := srv.eventBus.SubscribeAll(newRunStreamForwarder(eventChan))
	defer unsubscribe()

	srv.pumpRunStream(ctx, eventChan, send)
}

// newRunStreamForwarder returns an event-bus subscriber that forwards run
// lifecycle events onto eventChan, dropping (with a warning) when the buffer is
// full so a slow client never blocks the bus.
func newRunStreamForwarder(eventChan chan<- events.Event) func(events.Event) {
	return func(event events.Event) {
		switch event.Type {
		case events.EventRunCreated, events.EventRunStarted, events.EventRunCompleted, events.EventRunFailed, events.EventRunUpdated, events.EventRunDeleted:
			select {
			case eventChan <- event:
			default:
				slog.Warn("Run stream channel full", "event", event.Type)
			}
		}
	}
}

// pumpRunStream relays buffered run events and periodic keepalive pings to the
// SSE client, returning when the context is cancelled or a send fails.
func (srv *Server) pumpRunStream(ctx context.Context, eventChan <-chan events.Event, send sse.Sender) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-eventChan:
			if err := send(sse.Message{Data: toSSEEventData(event)}); err != nil {
				return
			}
		case <-ticker.C:
			if err := send(sse.Message{Data: PingEvent{}}); err != nil {
				return
			}
		}
	}
}

// toSSEEventData converts an internal event to the correct SSE wrapper type
// so that huma/sse emits the right event name. The run payload is projected
// from model.Run onto model.Run so the SSE wire shape matches the REST
// response (row-internal fields like deleted_at stay invisible).
func toSSEEventData(event events.Event) any {
	if event.Type == events.EventRunDeleted {
		if de, ok := event.Data.(events.RunDeletedEvent); ok {
			return RunDeletedSSEEvent(de)
		}
		return RunDeletedSSEEvent{}
	}
	re, ok := event.Data.(events.RunEvent)
	if !ok {
		return RunUpdatedEvent{}
	}
	body := RunEventBody{Run: re.Run, Error: re.Error}
	switch event.Type {
	case events.EventRunCreated:
		return RunCreatedEvent(body)
	case events.EventRunStarted:
		return RunStartedEvent(body)
	case events.EventRunCompleted:
		return RunCompletedEvent(body)
	case events.EventRunFailed:
		return RunFailedEvent(body)
	case events.EventRunUpdated:
		return RunUpdatedEvent(body)
	default:
		return RunUpdatedEvent(body)
	}
}
