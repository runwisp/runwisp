// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/model"
)

type TaskSyncClient struct {
	httpClient *http.Client
	syncURL    string
}

type TaskSyncSummary struct {
	Added           int `json:"added"`
	Updated         int `json:"updated"`
	MarkedOutOfSync int `json:"markedOutOfSync"`
	Total           int `json:"total"`
}

type TaskSyncResult struct {
	Success bool            `json:"success"`
	Summary TaskSyncSummary `json:"summary"`
}

type taskSyncPayload struct {
	Tasks  []syncTask `json:"tasks"`
	SyncID string     `json:"syncId,omitempty"`
}

type syncTask struct {
	Name                string             `json:"name"`
	Group               string             `json:"group,omitempty"`
	Description         string             `json:"description,omitempty"`
	ConcurrencyLimit    *int               `json:"concurrencyLimit,omitempty"`
	ConcurrencyBehavior string             `json:"concurrencyBehavior,omitempty"`
	RestartPolicy       string             `json:"restartPolicy,omitempty"`
	MaxRetries          *int               `json:"maxRetries,omitempty"`
	RetryDelay          *int               `json:"retryDelay,omitempty"`
	RetryBackoff        string             `json:"retryBackoff,omitempty"`
	Timeout             *int               `json:"timeout,omitempty"`
	Env                 map[string]string  `json:"env,omitempty"`
	GracefulStop        *int               `json:"gracefulStop,omitempty"`
	LogMaxSize          *int64             `json:"logMaxSize,omitempty"`
	LogOnFull           string             `json:"logOnFull,omitempty"`
	Enabled             *bool              `json:"enabled,omitempty"`
	Script              json.RawMessage    `json:"script,omitempty"`
	Schedules           []syncTaskSchedule `json:"schedules,omitempty"`

	// Service-only fields. All omitempty so a cloud that predates service sync
	// ignores them and treats the row as a plain task.
	Kind              string                `json:"kind,omitempty"`
	Instances         *int                  `json:"instances,omitempty"`
	Autostart         *bool                 `json:"autostart,omitempty"`
	RestartDelay      *int                  `json:"restartDelay,omitempty"`      // milliseconds
	RestartBackoff    string                `json:"restartBackoff,omitempty"`    // distinct from a task's RetryBackoff
	BackoffResetAfter *int                  `json:"backoffResetAfter,omitempty"` // milliseconds, from HealthyAfter
	Compose           *model.TaskComposeRef `json:"compose,omitempty"`
}

type syncTaskSchedule struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}

func NewTaskSyncClient(syncURL string, timeout time.Duration) *TaskSyncClient {
	return &TaskSyncClient{
		httpClient: &http.Client{Timeout: timeout},
		syncURL:    syncURL,
	}
}

func (client *TaskSyncClient) SyncTasks(ctx context.Context, token string, tasks map[string]*model.Task) (*TaskSyncResult, error) {
	syncTasks := buildSyncTasks(tasks)
	requestBody := taskSyncPayload{
		Tasks:  syncTasks,
		SyncID: buildTaskSyncID(syncTasks),
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: "failed to encode task sync request",
			Err:     err,
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.syncURL, bytes.NewReader(payload))
	if err != nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindTransient,
			Message: "failed to create task sync request",
			Err:     err,
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindTransient,
			Message: "task sync request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindTransient,
			Message: "failed to read task sync response",
			Err:     err,
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := classifySyncHTTPError(resp.StatusCode)
		return nil, &CloudError{
			Kind:    kind,
			Message: fmt.Sprintf("task sync failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody))),
		}
	}

	// The /api/v1/runner/tasks/sync endpoint returns this shape bare — no tRPC
	// `result.data` wrapper — so it decodes directly into TaskSyncResult.
	result := &TaskSyncResult{}
	if err := json.Unmarshal(responseBody, result); err != nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: "failed to decode task sync response",
			Err:     err,
		}
	}

	if !result.Success {
		return nil, &CloudError{
			Kind:    CloudErrorKindConflict,
			Message: "task sync returned success=false",
		}
	}

	return result, nil
}

func buildSyncTasks(tasks map[string]*model.Task) []syncTask {
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)

	syncTasks := make([]syncTask, 0, len(names))
	for _, name := range names {
		t := tasks[name]
		if t == nil {
			continue
		}
		task, ok := buildOneSyncTask(t)
		if ok {
			syncTasks = append(syncTasks, task)
		}
	}
	return syncTasks
}

// buildOneSyncTask converts a single model.Task into its cloud sync representation.
// Returns (task, false) if the execution definition cannot be marshalled.
func buildOneSyncTask(t *model.Task) (syncTask, bool) {
	task := syncTask{
		Name:          t.Name,
		Group:         t.Group,
		Description:   t.Description,
		RestartPolicy: string(model.RestartNever),
	}

	execDef := t.ResolvedExecutionDef()
	if scriptJSON, err := model.MarshalExecutionDef(execDef); err == nil {
		task.Script = scriptJSON
	} else {
		slog.Warn("Failed to marshal execution", "task", t.Name, "err", err)
		return syncTask{}, false
	}

	if t.Restart != "" {
		task.RestartPolicy = string(t.Restart)
	}
	if t.RetryAttempts > 0 {
		retries := t.RetryAttempts
		task.MaxRetries = &retries
	}
	if delayMs := durationToMillis(t.RetryDelay); delayMs > 0 {
		task.RetryDelay = &delayMs
	}
	if t.RetryBackoff != "" {
		task.RetryBackoff = t.RetryBackoff
	}
	if t.MaxConcurrent > 0 {
		limit := t.MaxConcurrent
		task.ConcurrencyLimit = &limit
	}
	if behavior := concurrencyBehaviorMap[t.OnOverlap]; behavior != "" {
		task.ConcurrencyBehavior = behavior
	}
	if timeoutMs := durationToMillis(t.Timeout); timeoutMs > 0 {
		task.Timeout = &timeoutMs
	}
	if len(t.Env) > 0 {
		task.Env = t.Env
	}
	if gsMs := durationToMillis(t.GracefulStop); gsMs > 0 {
		task.GracefulStop = &gsMs
	}
	if t.LogMaxSize > 0 {
		sz := t.LogMaxSize
		task.LogMaxSize = &sz
	}
	if t.LogOnFull != "" {
		task.LogOnFull = t.LogOnFull
	}
	enabled := true
	task.Enabled = &enabled

	task.Kind = string(t.Kind)

	if t.Kind.IsService() {
		// Services carry no cron, so they never emit a schedule.
		applyServiceSyncFields(&task, t)
	} else if cron := strings.TrimSpace(t.Cron); cron != "" {
		task.Schedules = []syncTaskSchedule{
			{
				Cron:     cron,
				Timezone: "UTC",
				Enabled:  true,
			},
		}
	}
	return task, true
}

// applyServiceSyncFields populates the service-only fields of a sync row from a
// service task. Kept separate from buildOneSyncTask so the task path stays
// simple and neither grows the other's cognitive complexity.
func applyServiceSyncFields(task *syncTask, t *model.Task) {
	if t.Instances > 0 {
		instances := t.Instances
		task.Instances = &instances
	}
	autostart := t.Autostart
	task.Autostart = &autostart
	if delayMs := durationToMillis(t.RestartDelay); delayMs > 0 {
		task.RestartDelay = &delayMs
	}
	if t.RestartBackoff != "" {
		task.RestartBackoff = t.RestartBackoff
	}
	if resetMs := durationToMillis(t.HealthyAfter); resetMs > 0 {
		task.BackoffResetAfter = &resetMs
	}
	task.Compose = t.Compose
}

func buildTaskSyncID(tasks []syncTask) string {
	payload, err := json.Marshal(tasks)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:8])
}

var concurrencyBehaviorMap = map[model.ConcurrencyPolicy]string{
	model.PolicyQueue:     "queue",
	model.PolicySkip:      "skip",
	model.PolicyTerminate: "terminate",
}

func durationToMillis(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d.Milliseconds())
}

func classifySyncHTTPError(statusCode int) CloudErrorKind {
	// A 401/403 here is NOT treated as a hard-auth stop: task sync runs only
	// after the WebSocket handshake already authenticated the same token, so a
	// 401/403 on this separate HTTP request is far more likely a transient
	// intermediary (proxy/WAF/gateway) than a real credential verdict. The
	// handshake is the sole authoritative auth boundary; sync errors retry with
	// backoff like any other transient failure.
	if statusCode >= 400 && statusCode < 500 {
		return CloudErrorKindValidation
	}
	return CloudErrorKindTransient
}
