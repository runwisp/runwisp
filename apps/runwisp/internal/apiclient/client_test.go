// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeRunsParams(t *testing.T) {
	tests := []struct {
		name   string
		params RunsParams
		want   map[string]string
	}{
		{
			name:   "empty params",
			params: RunsParams{},
			want:   map[string]string{},
		},
		{
			name: "all fields",
			params: RunsParams{
				Limit:         10,
				Offset:        20,
				Status:        "running",
				TaskName:      "my-task",
				SortField:     "created_at",
				SortDirection: "desc",
				Search:        "hello",
			},
			want: map[string]string{
				"limit":          "10",
				"offset":         "20",
				"status":         "running",
				"task_name":      "my-task",
				"sort_field":     "created_at",
				"sort_direction": "desc",
				"search":         "hello",
			},
		},
		{
			name:   "partial fields",
			params: RunsParams{Limit: 5, Status: "completed"},
			want:   map[string]string{"limit": "5", "status": "completed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeRunsParams(tt.params)
			assert.Equal(t, len(tt.want), len(got))
			for k, v := range tt.want {
				assert.Equal(t, v, got.Get(k), "param %s", k)
			}
		})
	}
}

func TestAuthenticate(t *testing.T) {
	nonce := "test-nonce-123"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			json.NewEncoder(w).Encode(map[string]string{"nonce": nonce})
		case "/api/auth":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["nonce"] != nonce {
				http.Error(w, "bad nonce", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"token": "jwt-token-xyz"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "my-password")
	assert.False(t, c.IsAuthenticated())

	err := c.Authenticate()
	require.NoError(t, err)
	assert.True(t, c.IsAuthenticated())
}

func TestAuthenticate_ChallengeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "pw")
	err := c.Authenticate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "auth challenge")
}

func TestAuthenticate_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/challenge":
			json.NewEncoder(w).Encode(map[string]string{"nonce": "n"})
		case "/api/auth":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "pw")
	err := c.Authenticate()
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestAuthenticate_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New(srv.URL, "pw")
	err := c.Authenticate()
	assert.ErrorIs(t, err, ErrRateLimited)
}

func TestListTasks(t *testing.T) {
	tasks := []model.TaskResponse{
		{Task: model.Task{Name: "task-1"}},
		{Task: model.Task{Name: "task-2"}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tasks", r.URL.Path)
		json.NewEncoder(w).Encode(tasks)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.ListTasks()
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "task-1", got[0].Name)
	assert.Equal(t, "task-2", got[1].Name)
}

func TestListRuns(t *testing.T) {
	resp := server.RunsResponseBody{
		Runs:  []model.Run{{ID: "run-1"}, {ID: "run-2"}},
		Total: 42,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/runs", r.URL.Path)
		assert.Equal(t, "10", r.URL.Query().Get("limit"))
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	runs, total, err := c.ListRuns(RunsParams{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, int64(42), total)
}

func TestListRunsByTask(t *testing.T) {
	resp := server.RunsResponseBody{
		Runs:  []model.Run{{ID: "run-1"}},
		Total: 1,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tasks/my-task/runs", r.URL.Path)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	runs, total, err := c.ListRunsByTask("my-task", RunsParams{})
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, int64(1), total)
}

func TestTriggerRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/tasks/my-task/run", r.URL.Path)
		json.NewEncoder(w).Encode(model.Run{ID: "new-run"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	run, err := c.TriggerRun("my-task")
	require.NoError(t, err)
	assert.Equal(t, "new-run", run.ID)
}

func TestStopRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/tasks/my-task/runs/run-1/stop", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	err := c.StopRun("my-task", "run-1")
	assert.NoError(t, err)
}

func TestGetRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tasks/my-task/runs/run-1", r.URL.Path)
		json.NewEncoder(w).Encode(model.Run{ID: "run-1", TaskName: "my-task"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	run, err := c.GetRun("my-task", "run-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", run.ID)
}

func TestGetSystemStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/system", r.URL.Path)
		json.NewEncoder(w).Encode(model.SystemStats{CPUUsage: 42.5, Version: "1.0"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	stats, err := c.GetSystemStats()
	require.NoError(t, err)
	assert.Equal(t, 42.5, stats.CPUUsage)
	assert.Equal(t, "1.0", stats.Version)
}

func TestHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	err := c.HealthCheck()
	assert.NoError(t, err)
}

func TestDoJSON_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	err := c.doJSON("GET", "/api/tasks", nil, nil)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestDoJSON_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	err := c.doJSON("GET", "/api/tasks", nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestDoJSON_AuthHeaderSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer my-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	c.token = "my-token"
	err := c.doJSON("GET", "/api/test", nil, nil)
	assert.NoError(t, err)
}

func TestGetLogRaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tasks/my-task/runs/run-1/log/raw", r.URL.Path)
		fmt.Fprintln(w, "line 1")
		fmt.Fprintln(w, "line 2")
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	body, err := c.GetLogRaw("my-task", "run-1")
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "line 1\nline 2\n", string(data))
}

func TestStreamRunEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/runs/stream", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "event: run.created")
		fmt.Fprintln(w, `data: {"type":"run.created","timestamp":"2025-01-01T00:00:00Z","data":{}}`)
		fmt.Fprintln(w, "")
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := c.StreamRunEvents(ctx)
	require.NoError(t, err)

	evt := <-ch
	assert.Equal(t, "run.created", evt.Type)
}

func TestBaseURL(t *testing.T) {
	c := New("http://localhost:9477/", "")
	assert.Equal(t, "http://localhost:9477", c.BaseURL())
}

func TestNewUnix_LocalShortCircuitsAuth(t *testing.T) {
	c := NewUnix("/tmp/runwisp-test.sock")
	assert.True(t, c.IsLocal(), "NewUnix client must be flagged as local")
	assert.True(t, c.IsAuthenticated(), "local client must report authenticated before any call")
	// Authenticate is a no-op on the local path; it must not error even
	// without a daemon at the socket path.
	require.NoError(t, c.Authenticate())
}

// TestNewUnix_DialsSocket exercises the Unix-socket transport against a real
// HTTP handler bound to a socket file. It is the smoke test that proves the
// custom dialer, the URL placeholder host, and the JSON round-trip all
// cooperate correctly.
func TestNewUnix_DialsSocket(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "runwisp.sock")

	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	handler := http.NewServeMux()
	handler.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.SystemStats{CPUUsage: 7.0, Version: "test"})
	})
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })

	c := NewUnix(socketPath)
	stats, err := c.GetSystemStats()
	require.NoError(t, err)
	assert.Equal(t, "test", stats.Version)
	assert.Equal(t, 7.0, stats.CPUUsage)
}
