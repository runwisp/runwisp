// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapConcurrencyBehavior(t *testing.T) {
	tests := []struct {
		policy model.ConcurrencyPolicy
		want   string
	}{
		{model.PolicyQueue, "queue"},
		{model.PolicySkip, "skip"},
		{model.PolicyTerminate, "terminate"},
		{model.ConcurrencyPolicy("unknown"), ""},
		{model.ConcurrencyPolicy(""), ""},
	}
	for _, tc := range tests {
		got := mapConcurrencyBehavior(tc.policy)
		assert.Equal(t, tc.want, got, "policy=%q", tc.policy)
	}
}

func TestDurationToMillis(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want int
	}{
		{0, 0},
		{-time.Second, 0},
		{-1, 0},
		{500 * time.Millisecond, 500},
		{2 * time.Minute, 120000},
		{time.Second, 1000},
	}
	for _, tc := range tests {
		got := durationToMillis(tc.d)
		assert.Equal(t, tc.want, got, "duration=%v", tc.d)
	}
}

func TestClassifySyncHTTPError(t *testing.T) {
	tests := []struct {
		code int
		want CloudErrorKind
	}{
		{http.StatusUnauthorized, CloudErrorKindAuth},
		{http.StatusForbidden, CloudErrorKindAuth},
		{http.StatusBadRequest, CloudErrorKindValidation},
		{http.StatusUnprocessableEntity, CloudErrorKindValidation},
		{http.StatusNotFound, CloudErrorKindValidation},
		{http.StatusInternalServerError, CloudErrorKindTransient},
		{http.StatusServiceUnavailable, CloudErrorKindTransient},
		{502, CloudErrorKindTransient},
	}
	for _, tc := range tests {
		got := classifySyncHTTPError(tc.code)
		assert.Equal(t, tc.want, got, "statusCode=%d", tc.code)
	}
}

func TestClassifySyncErrorMessage(t *testing.T) {
	tests := []struct {
		message string
		want    CloudErrorKind
	}{
		{"token expired", CloudErrorKindAuth},
		{"token revoked", CloudErrorKindAuth},
		{"TOKEN EXPIRED", CloudErrorKindAuth}, // case-insensitive
		{"conflict detected", CloudErrorKindConflict},
		{"out of sync", CloudErrorKindConflict},
		{"invalid request", CloudErrorKindValidation},
		{"malformed JSON", CloudErrorKindValidation},
		{"network timeout", CloudErrorKindTransient},
		{"some other error", CloudErrorKindTransient},
		{"", CloudErrorKindTransient},
	}
	for _, tc := range tests {
		got := classifySyncErrorMessage(tc.message)
		assert.Equal(t, tc.want, got, "message=%q", tc.message)
	}
}

func TestBuildOneSyncTask(t *testing.T) {
	t.Run("basic shell task with cron", func(t *testing.T) {
		task := &model.Task{
			Name:        "my-task",
			Description: "does things",
			Run:         "echo hello",
			Cron:        "*/5 * * * *",
		}

		st, ok := buildOneSyncTask(task)
		require.True(t, ok)
		assert.Equal(t, "my-task", st.Name)
		assert.Equal(t, "does things", st.Description)
		assert.Equal(t, string(model.RestartNever), st.RestartPolicy)
		require.Len(t, st.Schedules, 1)
		assert.Equal(t, "*/5 * * * *", st.Schedules[0].Cron)
		assert.Equal(t, "UTC", st.Schedules[0].Timezone)
		assert.True(t, st.Schedules[0].Enabled)
		require.NotNil(t, st.Enabled)
		assert.True(t, *st.Enabled)
	})

	t.Run("task with all optional fields", func(t *testing.T) {
		retryAttempts := 3
		task := &model.Task{
			Name:          "full-task",
			Run:           "echo hi",
			Restart:       model.RestartOnFailure,
			RetryAttempts: retryAttempts,
			RetryDelay:    2 * time.Second,
			RetryBackoff:  model.BackoffExponential,
			MaxConcurrent: 2,
			OnOverlap:     model.PolicySkip,
			Timeout:       30 * time.Second,
		}

		st, ok := buildOneSyncTask(task)
		require.True(t, ok)
		assert.Equal(t, string(model.RestartOnFailure), st.RestartPolicy)
		require.NotNil(t, st.MaxRetries)
		assert.Equal(t, retryAttempts, *st.MaxRetries)
		require.NotNil(t, st.RetryDelay)
		assert.Equal(t, 2000, *st.RetryDelay)
		assert.Equal(t, model.BackoffExponential, st.RetryBackoff)
		require.NotNil(t, st.ConcurrencyLimit)
		assert.Equal(t, 2, *st.ConcurrencyLimit)
		assert.Equal(t, "skip", st.ConcurrencyBehavior)
		require.NotNil(t, st.Timeout)
		assert.Equal(t, 30000, *st.Timeout)
		assert.Empty(t, st.Schedules) // no cron
	})

	t.Run("task with zero retry delay omits RetryDelay field", func(t *testing.T) {
		task := &model.Task{
			Name:       "no-delay",
			Run:        "echo hi",
			RetryDelay: 0,
		}
		st, ok := buildOneSyncTask(task)
		require.True(t, ok)
		assert.Nil(t, st.RetryDelay)
	})

	t.Run("cron with surrounding whitespace is trimmed", func(t *testing.T) {
		task := &model.Task{
			Name: "ws-task",
			Run:  "echo ws",
			Cron: "  0 * * * *  ",
		}
		st, ok := buildOneSyncTask(task)
		require.True(t, ok)
		require.Len(t, st.Schedules, 1)
		assert.Equal(t, "0 * * * *", st.Schedules[0].Cron)
	})
}

func TestBuildTaskSyncID(t *testing.T) {
	t.Run("empty slice produces non-empty hash", func(t *testing.T) {
		id := buildTaskSyncID([]syncTask{})
		assert.NotEmpty(t, id)
		assert.Len(t, id, 16) // 8 bytes hex-encoded
	})

	t.Run("same tasks produce same id", func(t *testing.T) {
		tasks := []syncTask{{Name: "a"}, {Name: "b"}}
		id1 := buildTaskSyncID(tasks)
		id2 := buildTaskSyncID(tasks)
		assert.Equal(t, id1, id2)
	})

	t.Run("different tasks produce different id", func(t *testing.T) {
		id1 := buildTaskSyncID([]syncTask{{Name: "a"}})
		id2 := buildTaskSyncID([]syncTask{{Name: "b"}})
		assert.NotEqual(t, id1, id2)
	})
}

func TestBuildSyncTasks(t *testing.T) {
	t.Run("nil task entry is skipped", func(t *testing.T) {
		tasks := map[string]*model.Task{
			"present": {Name: "present", Run: "echo hi"},
			"nil-one": nil,
		}
		result := buildSyncTasks(tasks)
		assert.Len(t, result, 1)
		assert.Equal(t, "present", result[0].Name)
	})

	t.Run("output is sorted by name", func(t *testing.T) {
		tasks := map[string]*model.Task{
			"zebra": {Name: "zebra", Run: "echo z"},
			"apple": {Name: "apple", Run: "echo a"},
			"mango": {Name: "mango", Run: "echo m"},
		}
		result := buildSyncTasks(tasks)
		require.Len(t, result, 3)
		assert.Equal(t, "apple", result[0].Name)
		assert.Equal(t, "mango", result[1].Name)
		assert.Equal(t, "zebra", result[2].Name)
	})

	t.Run("empty map returns empty slice", func(t *testing.T) {
		result := buildSyncTasks(map[string]*model.Task{})
		assert.Empty(t, result)
	})
}

func TestSyncTasksSendsDirectPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		require.Equal(t, http.MethodPost, req.Method)

		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		var payload map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &payload))

		_, hasJSONEnvelope := payload["json"]
		assert.False(t, hasJSONEnvelope)
		assert.Equal(t, "Bearer rt_test", req.Header.Get("Authorization"))
		_, hasToken := payload["token"]
		assert.False(t, hasToken)
		_, hasTasks := payload["tasks"]
		assert.True(t, hasTasks)

		resp.Header().Set("Content-Type", "application/json")
		_, _ = resp.Write([]byte(`{"result":{"data":{"success":true,"summary":{"added":0,"updated":0,"markedOutOfSync":0,"total":0}}}}`))
	}))
	defer server.Close()

	client := NewTaskSyncClient(server.URL, 5*time.Second)
	result, err := client.SyncTasks(context.Background(), "rt_test", map[string]*model.Task{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
}

func TestSyncTasksHTTPErrorBubblesUpAsCloudError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, _ *http.Request) {
		resp.WriteHeader(http.StatusUnauthorized)
		_, _ = resp.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer server.Close()

	client := NewTaskSyncClient(server.URL, 5*time.Second)
	_, err := client.SyncTasks(context.Background(), "rt_test", map[string]*model.Task{})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindAuth, ce.Kind)
	assert.Contains(t, ce.Message, "401")
}

func TestSyncTasksTransportFailureIsTransient(t *testing.T) {
	// Unreachable URL — net.Dial fails, surfaced as a transient CloudError.
	client := NewTaskSyncClient("http://127.0.0.1:1/invalid", 1*time.Second)
	_, err := client.SyncTasks(context.Background(), "rt_test", map[string]*model.Task{})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindTransient, ce.Kind)
}

func TestSyncTasksInvalidJSONBodyIsValidationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, _ *http.Request) {
		resp.Header().Set("Content-Type", "application/json")
		_, _ = resp.Write([]byte(`{not json`))
	}))
	defer server.Close()

	client := NewTaskSyncClient(server.URL, 5*time.Second)
	_, err := client.SyncTasks(context.Background(), "rt_test", map[string]*model.Task{})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
}

func TestSyncTasksParsesCurrentResponseShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		resp.Header().Set("Content-Type", "application/json")
		_, _ = resp.Write([]byte(`{"result":{"data":{"success":true,"summary":{"added":1,"updated":2,"markedOutOfSync":3,"total":4}}}}`))
	}))
	defer server.Close()

	client := NewTaskSyncClient(server.URL, 5*time.Second)
	result, err := client.SyncTasks(context.Background(), "rt_test", map[string]*model.Task{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, TaskSyncSummary{
		Added:           1,
		Updated:         2,
		MarkedOutOfSync: 3,
		Total:           4,
	}, result.Summary)
}
