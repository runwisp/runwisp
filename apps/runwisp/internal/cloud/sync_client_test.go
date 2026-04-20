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
