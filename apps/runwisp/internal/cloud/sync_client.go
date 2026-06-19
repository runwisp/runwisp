// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
}

type syncTaskSchedule struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}

// taskSyncResponse is the bare response shape returned by the new cloud
// /api/v1/runner/tasks/sync endpoint — no tRPC `result.data` wrapper.
// Errors come back as non-2xx HTTP plus a JSON body the handler logs from
// the status line; we don't try to decode them.
type taskSyncResponse struct {
	Success bool            `json:"success"`
	Summary TaskSyncSummary `json:"summary"`
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

	var data taskSyncResponse
	if err := json.Unmarshal(responseBody, &data); err != nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: "failed to decode task sync response",
			Err:     err,
		}
	}

	result := &TaskSyncResult{
		Success: data.Success,
		Summary: data.Summary,
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
	if behavior := mapConcurrencyBehavior(t.OnOverlap); behavior != "" {
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
	if strings.TrimSpace(t.Cron) != "" {
		task.Schedules = []syncTaskSchedule{
			{
				Cron:     strings.TrimSpace(t.Cron),
				Timezone: "UTC",
				Enabled:  true,
			},
		}
	}
	return task, true
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

func mapConcurrencyBehavior(policy model.ConcurrencyPolicy) string {
	return concurrencyBehaviorMap[policy]
}

func durationToMillis(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d.Milliseconds())
}

func classifySyncHTTPError(statusCode int) CloudErrorKind {
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return CloudErrorKindAuth
	}
	if statusCode >= 400 && statusCode < 500 {
		return CloudErrorKindValidation
	}
	return CloudErrorKindTransient
}
