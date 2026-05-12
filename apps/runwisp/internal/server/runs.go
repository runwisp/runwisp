// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/go-chi/chi/v5"
	"github.com/runwisp/runwisp/internal/events"
	"log/slog"
)

type PaginationParams struct {
	Limit, Offset                                           int
	Status, TaskName, SortField, SortDirection, SearchQuery string
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
	result, err := srv.runService.ListRuns("", p)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get runs")
	}
	return &RunsOutput{Body: RunsResponseBody{Runs: result.Runs, Total: result.Total}}, nil
}

func (srv *Server) humaGetRunSummary(ctx context.Context, input *struct{}) (*RunSummaryOutput, error) {
	summary, err := srv.db.GetRunSummary()
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get run summary")
	}
	return &RunSummaryOutput{Body: *summary}, nil
}

func (srv *Server) humaGetTaskRuns(ctx context.Context, input *TaskRunsQueryInput) (*RunsOutput, error) {
	p := input.toPaginationParams()
	result, err := srv.runService.ListRuns(input.TaskName, p)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get runs")
	}
	return &RunsOutput{Body: RunsResponseBody{Runs: result.Runs, Total: result.Total}}, nil
}

func (srv *Server) humaTriggerRun(ctx context.Context, input *TaskNameInput) (*TriggerRunOutput, error) {
	run, err := srv.runService.TriggerRun(input.TaskName)
	if err != nil {
		switch {
		case errors.Is(err, ErrTaskNotFound):
			return nil, huma.Error404NotFound(err.Error())
		case errors.Is(err, ErrAPIDisabled):
			return nil, huma.Error403Forbidden(err.Error())
		case errors.Is(err, ErrServiceNotRunnable):
			return nil, huma.Error409Conflict(err.Error())
		default:
			return nil, huma.Error500InternalServerError(err.Error())
		}
	}
	return &TriggerRunOutput{Body: *run}, nil
}

func (srv *Server) humaRestartService(ctx context.Context, input *TaskNameInput) (*StopRunOutput, error) {
	if err := srv.runService.RestartService(input.TaskName); err != nil {
		switch {
		case errors.Is(err, ErrTaskNotFound):
			return nil, huma.Error404NotFound(err.Error())
		case errors.Is(err, ErrNotAService):
			return nil, huma.Error400BadRequest(err.Error())
		default:
			return nil, huma.Error500InternalServerError(err.Error())
		}
	}
	out := &StopRunOutput{}
	out.Body.Message = "Service instances restarting"
	return out, nil
}

func (srv *Server) humaStopService(ctx context.Context, input *TaskNameInput) (*StopRunOutput, error) {
	if err := srv.runService.StopService(input.TaskName); err != nil {
		switch {
		case errors.Is(err, ErrTaskNotFound):
			return nil, huma.Error404NotFound(err.Error())
		case errors.Is(err, ErrNotAService):
			return nil, huma.Error400BadRequest(err.Error())
		default:
			return nil, huma.Error500InternalServerError(err.Error())
		}
	}
	out := &StopRunOutput{}
	out.Body.Message = "Service stopped"
	return out, nil
}

func (srv *Server) humaGetRun(ctx context.Context, input *TaskRunInput) (*RunOutput, error) {
	run, err := srv.runService.GetRun(input.RunID)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	return &RunOutput{Body: *run}, nil
}

func (srv *Server) humaDeleteRun(ctx context.Context, input *TaskRunInput) (*struct{}, error) {
	if err := srv.runService.DeleteRun(input.RunID); err != nil {
		if errors.Is(err, ErrRunNotFound) {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, huma.Error500InternalServerError("Failed to delete run")
	}
	return nil, nil
}

func (srv *Server) humaStopRun(ctx context.Context, input *TaskRunInput) (*StopRunOutput, error) {
	err := srv.runService.StopRun(input.RunID)
	if err != nil {
		switch {
		case errors.Is(err, ErrRunNotFound):
			return nil, huma.Error404NotFound(err.Error())
		case errors.Is(err, ErrNotRunning):
			return nil, huma.Error400BadRequest(err.Error())
		default:
			return nil, huma.Error500InternalServerError("Failed to stop run")
		}
	}
	out := &StopRunOutput{}
	out.Body.Message = "Run stop signal sent"
	return out, nil
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
		"ping":          PingEvent{},
	}, func(ctx context.Context, input *struct{}, send sse.Sender) {
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
		unsubscribe := srv.eventBus.SubscribeAll(func(event events.Event) {
			switch event.Type {
			case events.EventRunCreated, events.EventRunStarted, events.EventRunCompleted, events.EventRunFailed, events.EventRunUpdated:
				select {
				case eventChan <- event:
				default:
					slog.Warn("Run stream channel full", "event", event.Type)
				}
			}
		})
		defer unsubscribe()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case event := <-eventChan:
				data := toSSEEventData(event)
				if err := send(sse.Message{Data: data}); err != nil {
					return
				}
			case <-ticker.C:
				if err := send(sse.Message{Data: PingEvent{}}); err != nil {
					return
				}
			}
		}
	})
}

// toSSEEventData converts an internal event to the correct SSE wrapper type
// so that huma/sse emits the right event name.
func toSSEEventData(event events.Event) any {
	re, ok := event.Data.(events.RunEvent)
	if !ok {
		return RunUpdatedEvent{}
	}
	switch event.Type {
	case events.EventRunCreated:
		return RunCreatedEvent(re)
	case events.EventRunStarted:
		return RunStartedEvent(re)
	case events.EventRunCompleted:
		return RunCompletedEvent(re)
	case events.EventRunFailed:
		return RunFailedEvent(re)
	case events.EventRunUpdated:
		return RunUpdatedEvent(re)
	default:
		return RunUpdatedEvent(re)
	}
}
