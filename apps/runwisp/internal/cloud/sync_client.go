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
	Description         string             `json:"description,omitempty"`
	ConcurrencyLimit    *int               `json:"concurrencyLimit,omitempty"`
	ConcurrencyBehavior string             `json:"concurrencyBehavior,omitempty"`
	RestartPolicy       string             `json:"restartPolicy,omitempty"`
	MaxRetries          *int               `json:"maxRetries,omitempty"`
	RetryDelay          *int               `json:"retryDelay,omitempty"`
	RetryBackoff        string             `json:"retryBackoff,omitempty"`
	Timeout             *int               `json:"timeout,omitempty"`
	Enabled             *bool              `json:"enabled,omitempty"`
	Script              json.RawMessage    `json:"script,omitempty"`
	Schedules           []syncTaskSchedule `json:"schedules,omitempty"`
}

type syncTaskSchedule struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
}

type taskSyncResponseEnvelope struct {
	Result *struct {
		Data *taskSyncResponseData `json:"data"`
	} `json:"result,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type taskSyncResponseData struct {
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

	var envelope taskSyncResponseEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: "failed to decode task sync response",
			Err:     err,
		}
	}

	if envelope.Error != nil {
		kind := classifySyncErrorMessage(envelope.Error.Message)
		return nil, &CloudError{
			Kind:    kind,
			Message: envelope.Error.Message,
		}
	}

	if envelope.Result == nil || envelope.Result.Data == nil {
		return nil, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: "task sync response missing result payload",
		}
	}

	data := envelope.Result.Data
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

		task := syncTask{
			Name:          t.Name,
			Description:   t.Description,
			RestartPolicy: string(model.RestartNever),
		}

		// Serialize the execution definition using the typed format
		execDef := t.ResolvedExecutionDef()
		if scriptJSON, err := model.MarshalExecutionDef(execDef); err == nil {
			task.Script = scriptJSON
		} else {
			slog.Warn("Failed to marshal execution", "task", t.Name, "err", err)
			continue // Skip task if we cannot marshal its execution definition
		}

		if t.Restart != "" {
			task.RestartPolicy = string(t.Restart)
		}
		if t.RetryAttempts > 0 {
			retries := t.RetryAttempts
			task.MaxRetries = &retries
		}
		if delayMs := parseRetryDelayMillis(t.RetryDelay); delayMs > 0 {
			task.RetryDelay = &delayMs
		}
		if t.RetryBackoff != "" {
			task.RetryBackoff = t.RetryBackoff
		}

		if t.Parallelism > 0 {
			limit := t.Parallelism
			task.ConcurrencyLimit = &limit
		}

		behavior := mapConcurrencyBehavior(t.OnOverlap)
		if behavior != "" {
			task.ConcurrencyBehavior = behavior
		}

		if timeoutMs := parseTimeoutMillis(t.Timeout); timeoutMs > 0 {
			task.Timeout = &timeoutMs
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

		syncTasks = append(syncTasks, task)
	}

	return syncTasks
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

func parseTimeoutMillis(raw string) int {
	return parseDurationMillis(raw)
}

func parseRetryDelayMillis(raw string) int {
	return parseDurationMillis(raw)
}

func parseDurationMillis(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	if duration <= 0 {
		return 0
	}
	return int(duration.Milliseconds())
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

// errorClassification maps substring patterns to error kinds.
// Checked against the lowercased error message from the cloud API.
var errorClassification = []struct {
	patterns []string
	kind     CloudErrorKind
}{
	{[]string{"token", "revoked", "expired"}, CloudErrorKindAuth},
	{[]string{"conflict", "out of sync"}, CloudErrorKindConflict},
	{[]string{"invalid", "malformed"}, CloudErrorKindValidation},
}

func classifySyncErrorMessage(message string) CloudErrorKind {
	lower := strings.ToLower(message)
	for _, rule := range errorClassification {
		for _, pattern := range rule.patterns {
			if strings.Contains(lower, pattern) {
				return rule.kind
			}
		}
	}
	return CloudErrorKindTransient
}
