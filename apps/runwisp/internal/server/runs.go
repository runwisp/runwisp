// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/runwisp/runwisp/internal/storage"
)

type PaginationParams struct {
	Limit, Offset int
	Filter        model.RunFilter
	SortField     storage.SortColumn
	SortDirection storage.SortDirection
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
		OperationID: "listTasks",
		Method:      http.MethodGet,
		Path:        "/api/tasks",
		Summary:     "List all tasks",
		Tags:        []string{"Tasks"},
	}, srv.humaListTasks)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "listRuns",
		Method:      http.MethodGet,
		Path:        "/api/runs",
		Summary:     "List all runs",
		Tags:        []string{"Runs"},
	}, srv.humaListRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getRunSummary",
		Method:      http.MethodGet,
		Path:        "/api/runs/summary",
		Summary:     "Get aggregate run statistics",
		Tags:        []string{"Runs"},
	}, srv.humaGetRunSummary)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "getRun",
		Method:      http.MethodGet,
		Path:        "/api/runs/{runId}",
		Summary:     "Get a single run by ID",
		Tags:        []string{"Runs"},
	}, srv.humaGetRun)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "listTaskRuns",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/runs",
		Summary:     "List runs for a task",
		Tags:        []string{"Runs"},
	}, srv.humaListTaskRuns)

	huma.Register(protectedAPI, huma.Operation{
		OperationID:   "triggerRun",
		Method:        http.MethodPost,
		Path:          "/api/tasks/{taskName}/run",
		Summary:       "Trigger a new run",
		Description:   "Triggers the task and returns the pending run immediately. Pass `wait=true` to instead hold the request open until the run finishes and return it with its exit code and end reason — a one-call alternative to triggering then polling.",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusCreated,
	}, srv.humaTriggerRun)

	huma.Register(protectedAPI, huma.Operation{
		OperationID:   "restartTask",
		Method:        http.MethodPost,
		Path:          "/api/tasks/{taskName}/restart",
		Summary:       "Restart all instances of a service",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaRestartTask)

	huma.Register(protectedAPI, huma.Operation{
		OperationID:   "stopTask",
		Method:        http.MethodPost,
		Path:          "/api/tasks/{taskName}/stop",
		Summary:       "Stop a service for the daemon's lifetime",
		Description:   "Cancels every live instance and marks the service stopped. The supervisor stops refilling slots until a restart is issued or the daemon is restarted.",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaStopTask)

	huma.Register(protectedAPI, huma.Operation{
		OperationID:   "deleteRun",
		Method:        http.MethodDelete,
		Path:          "/api/tasks/{taskName}/runs/{runId}",
		Summary:       "Delete a run",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusNoContent,
	}, srv.humaDeleteRun)

	huma.Register(protectedAPI, huma.Operation{
		OperationID:   "stopRun",
		Method:        http.MethodPost,
		Path:          "/api/tasks/{taskName}/runs/{runId}/stop",
		Summary:       "Stop a running task",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusNoContent,
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
		OperationID: "bulkStopRuns",
		Method:      http.MethodPost,
		Path:        "/api/runs/bulk/stop",
		Summary:     "Stop every running run matched by the selector",
		Tags:        []string{"Runs"},
	}, srv.humaBulkStopRuns)

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
		OperationID: "getLogLineHistory",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/runs/{runId}/log/line/{lineNum}/history",
		Summary:     "Get the frame history of a settled progress bar / redraw line",
		Description: "Returns the prior whole-region frames a progress bar or multi-line redraw passed through before settling into the committed line. Empty unless the line's `frame_count` is non-zero.",
		Tags:        []string{"Logs"},
	}, srv.humaGetLogLineHistory)

	huma.Register(protectedAPI, huma.Operation{
		OperationID: "searchLogs",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/log/search",
		Summary:     "Search log lines across runs of a task",
		Description: "Streams on disk through the task's runs newest-first and returns matching lines. Pure on-demand scan; no index is maintained. Use `cursor` to paginate beyond the per-request hit/run budget.",
		Tags:        []string{"Logs"},
	}, srv.humaSearchLogs)

	srv.registerLocalCredentialsRoute(protectedAPI)
	srv.registerAppStreamSSE(protectedAPI)
	srv.registerLogSSE(protectedAPI)
	srv.registerDaemonLogSSE(protectedAPI)
	srv.registerNotificationsRoutes(protectedAPI)
}

func (srv *Server) humaListTasks(ctx context.Context, input *struct{}) (*TasksOutput, error) {
	return &TasksOutput{Body: TasksResponseBody{Items: srv.runService.ListTasks()}}, nil
}

func (srv *Server) humaListRuns(ctx context.Context, input *RunsQueryInput) (*RunsOutput, error) {
	p := input.toPaginationParams()
	result, err := srv.runService.ListRuns(ctx, "", p)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to get runs")
	}
	return &RunsOutput{Body: RunsResponseBody{Items: result.Items, Total: result.Total}}, nil
}

func (srv *Server) humaGetRunSummary(ctx context.Context, input *struct{}) (*RunSummaryOutput, error) {
	summary, err := srv.db.GetRunSummary(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to get run summary")
	}
	return &RunSummaryOutput{Body: *summary}, nil
}

func (srv *Server) humaListTaskRuns(ctx context.Context, input *TaskRunsQueryInput) (*RunsOutput, error) {
	p := input.toPaginationParams()
	result, err := srv.runService.ListRuns(ctx, input.TaskName, p)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to get runs")
	}
	return &RunsOutput{Body: RunsResponseBody{Items: result.Items, Total: result.Total}}, nil
}

func (srv *Server) humaTriggerRun(ctx context.Context, input *TriggerRunInput) (*TriggerRunOutput, error) {
	var params map[string]*string
	if input.Body != nil {
		params = input.Body.Params
	}
	if input.Wait {
		run, err := srv.runService.TriggerRunAndWait(ctx, input.TaskName, params, time.Duration(input.WaitTimeout)*time.Second)
		if err != nil {
			return nil, mapDomainError(ctx, err, "Failed to trigger run")
		}
		return &TriggerRunOutput{Body: *run}, nil
	}
	run, err := srv.runService.TriggerRun(ctx, input.TaskName, params)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to trigger run")
	}
	return &TriggerRunOutput{Body: *run}, nil
}

func (srv *Server) humaRestartTask(ctx context.Context, input *TaskNameInput) (*struct{}, error) {
	if err := srv.runService.RestartService(input.TaskName); err != nil {
		return nil, mapDomainError(ctx, err, "Failed to restart service")
	}
	return nil, nil
}

func (srv *Server) humaStopTask(ctx context.Context, input *TaskNameInput) (*struct{}, error) {
	if err := srv.runService.StopService(input.TaskName); err != nil {
		return nil, mapDomainError(ctx, err, "Failed to stop service")
	}
	return nil, nil
}

func (srv *Server) humaGetRun(ctx context.Context, input *RunIDInput) (*RunOutput, error) {
	run, err := srv.runService.GetRun(ctx, input.RunID)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to fetch run")
	}
	return &RunOutput{Body: *run}, nil
}

func (srv *Server) humaDeleteRun(ctx context.Context, input *TaskRunInput) (*struct{}, error) {
	if err := srv.requireRunInTask(ctx, input.TaskName, input.RunID); err != nil {
		return nil, err
	}
	if err := srv.runService.DeleteRun(ctx, input.RunID); err != nil {
		return nil, mapDomainError(ctx, err, "Failed to delete run")
	}
	return nil, nil
}

func (srv *Server) humaStopRun(ctx context.Context, input *TaskRunInput) (*struct{}, error) {
	if err := srv.requireRunInTask(ctx, input.TaskName, input.RunID); err != nil {
		return nil, err
	}
	if err := srv.runService.StopRun(ctx, input.RunID); err != nil {
		return nil, mapDomainError(ctx, err, "Failed to stop run")
	}
	return nil, nil
}

// requireRunInTask rejects a run that does not belong to the taskName in the
// URL, so DELETE/POST /api/tasks/{taskName}/runs/{runId} cannot act on another
// task's run just because the run ID is known. Mirrors the 400/404 contract the
// per-run log endpoints use.
func (srv *Server) requireRunInTask(ctx context.Context, taskName, runID string) error {
	if _, err := srv.getRunForTask(ctx, taskName, runID); err != nil {
		if errors.Is(err, errInvalidRunID) {
			return huma.Error400BadRequest("Invalid run ID")
		}
		return huma.Error404NotFound("Run not found")
	}
	return nil
}

func (srv *Server) humaBulkDeleteRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkAffectedOutput, error) {
	n, err := srv.runService.bulkSoftDelete(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to delete runs")
	}
	return &BulkAffectedOutput{Body: BulkAffectedBody{Affected: n}}, nil
}

func (srv *Server) humaBulkRestoreRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkAffectedOutput, error) {
	n, err := srv.runService.bulkRestore(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to restore runs")
	}
	return &BulkAffectedOutput{Body: BulkAffectedBody{Affected: n}}, nil
}

func (srv *Server) humaBulkStopRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkAffectedOutput, error) {
	n, err := srv.runService.bulkCancel(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to cancel runs")
	}
	return &BulkAffectedOutput{Body: BulkAffectedBody{Affected: n}}, nil
}

func (srv *Server) humaBulkRerunRuns(ctx context.Context, input *BulkRunSelectorInput) (*BulkRerunOutput, error) {
	triggered, err := srv.runService.bulkRerun(ctx, input.Body)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to rerun")
	}
	return &BulkRerunOutput{Body: BulkRerunBody{Triggered: triggered}}, nil
}

// AppStreamInput carries the resume cursor for the app-event SSE stream. A
// browser reconnecting the same EventSource resends Last-Event-ID natively; a
// freshly opened EventSource (e.g. after a cross-tab leader handoff) has an
// empty Last-Event-ID, so the client passes the last id it saw as a query
// param instead. The header wins when both are present.
type AppStreamInput struct {
	LastEventID    string `header:"Last-Event-ID" doc:"Native SSE resume cursor; takes precedence over the lastEventId query"`
	LastEventQuery string `query:"lastEventId" doc:"Explicit resume id for a fresh EventSource whose native Last-Event-ID is empty (cross-tab leader handoff)"`
}

// resolveResumeID picks the SSE resume cursor: the native Last-Event-ID header
// when present and valid, else the lastEventId query fallback, else 0 (start
// from live with no replay).
func resolveResumeID(header, query string) int {
	if n, ok := parseResumeID(header); ok {
		return int(n)
	}
	if n, ok := parseResumeID(query); ok {
		return int(n)
	}
	return 0
}

func (srv *Server) registerAppStreamSSE(api huma.API) {
	sse.Register(api, huma.Operation{
		OperationID: "streamAppEvents",
		Method:      http.MethodGet,
		Path:        "/api/stream",
		Summary:     "Stream live application events",
		Description: "Single Server-Sent Events feed the web UI holds open per tab: run lifecycle events, periodic system resource samples, config-staleness flips, and in-app notifications. Each event carries a monotonic id; a reconnecting client resumes from Last-Event-ID (or the lastEventId query) and replays what it missed.",
		Tags:        []string{"Runs"},
	}, map[string]any{
		"run.created":                      RunCreatedEvent{},
		"run.started":                      RunStartedEvent{},
		"run.completed":                    RunCompletedEvent{},
		"run.failed":                       RunFailedEvent{},
		"run.updated":                      RunUpdatedEvent{},
		"run.deleted":                      RunDeletedSSEEvent{},
		"system":                           SystemSampleSSEEvent{},
		"config.stale":                     ConfigStaleSSEEvent{},
		inapp.UpdateTypeCreated:            NotificationCreatedEvent{},
		inapp.UpdateTypeUpdated:            NotificationUpdatedEvent{},
		inapp.UpdateTypeUnreadCountChanged: NotificationUnreadCountEvent{},
		"ping":                             PingEvent{},
	}, srv.appStreamHandler)
}

// appStreamHandler is the SSE callback for the unified app event stream. It
// admits the client (subject to the stream limit), flushes headers, replays any
// events the client missed since its Last-Event-ID, then subscribes to the live
// feed (via the shared appEventLog) and — when notify is enabled — the
// notification hub, relaying everything plus periodic pings over the one
// connection until the client disconnects. Folding these onto a single stream
// is what keeps a browser tab to one EventSource instead of three.
func (srv *Server) appStreamHandler(ctx context.Context, input *AppStreamInput, send sse.Sender) {
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
	// without waiting for the first real event. No id: this ping is outside
	// the event sequence and must not perturb the browser's lastEventId.
	if err := send(sse.Message{Data: PingEvent{}}); err != nil {
		return
	}

	replay, sub, unsub := srv.appEvents.subscribe(resolveResumeID(input.LastEventID, input.LastEventQuery))
	defer unsub()
	for _, e := range replay {
		if err := send(sse.Message{ID: e.id, Data: toSSEEventData(e.ev)}); err != nil {
			return
		}
	}

	var notifyCh <-chan inapp.Update
	if srv.notifyHub != nil {
		nsub, unsubscribe := srv.notifyHub.Subscribe()
		defer unsubscribe()
		notifyCh = nsub.Channel()
	}

	srv.pumpAppStream(ctx, sub, notifyCh, send)
}

// pumpAppStream relays live bus events (with their sequence id), in-app
// notifications, and periodic keepalive pings to the SSE client, returning when
// the context is cancelled, the subscriber overflows, or a send fails. notifyCh
// is nil when notify is disabled (a nil channel blocks forever in select, so
// that arm simply never fires).
func (srv *Server) pumpAppStream(ctx context.Context, sub *appSub, notifyCh <-chan inapp.Update, send sse.Sender) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.dead:
			// Overflowed: the client fell behind. Drop the connection so it
			// reconnects and replays from its last id rather than losing events.
			return
		case e := <-sub.ch:
			if err := send(sse.Message{ID: e.id, Data: toSSEEventData(e.ev)}); err != nil {
				return
			}
		case u, ok := <-notifyCh:
			if !ok {
				notifyCh = nil
				continue
			}
			// Notifications ride a separate DB-backed hub; no id so they stay
			// out of the event sequence the browser resumes from.
			if err := send(sse.Message{Data: notifyUpdateToPayload(u)}); err != nil {
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
	switch event.Type {
	case events.EventRunDeleted:
		if de, ok := event.Data.(events.RunDeletedEvent); ok {
			return RunDeletedSSEEvent(de)
		}
		return RunDeletedSSEEvent{}
	case events.EventSystemSample:
		if s, ok := event.Data.(events.SystemSampleEvent); ok {
			return SystemSampleSSEEvent{Sample: s.Sample, Uptime: s.Uptime}
		}
		return SystemSampleSSEEvent{}
	case events.EventConfigStale:
		if c, ok := event.Data.(events.ConfigStaleEvent); ok {
			return ConfigStaleSSEEvent{Stale: c.Stale}
		}
		return ConfigStaleSSEEvent{}
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
