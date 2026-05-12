// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	srp "mz.attahri.com/code/srp/v3"
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

// startStubSRPServer stands up an SRP-aware HTTP test server that uses the
// supplied password to derive verifier+salt. Tests exercise the full
// /srp/start + /srp/finish flow without spinning up the full daemon stack.
func startStubSRPServer(t *testing.T, password string) *httptest.Server {
	t.Helper()
	salt, err := datadir.GenerateSRPSalt()
	require.NoError(t, err)
	verifier, err := datadir.DeriveSRPVerifier(password, salt)
	require.NoError(t, err)
	var session *srp.Server
	var sessionID string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/srp/start":
			s, err := srp.NewServer(datadir.SRPParams(), datadir.SRPIdentity, salt, verifier)
			require.NoError(t, err)
			session = s
			sessionID = "test-session"
			json.NewEncoder(w).Encode(map[string]string{
				"sessionID": sessionID,
				"salt":      hex.EncodeToString(salt),
				"B":         hex.EncodeToString(s.B()),
			})
		case "/api/auth/srp/finish":
			var body struct {
				SessionID, A, M1 string
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if body.SessionID != sessionID {
				http.Error(w, "unknown session", http.StatusUnauthorized)
				return
			}
			A, _ := hex.DecodeString(body.A)
			require.NoError(t, session.SetA(A))
			M1, _ := hex.DecodeString(body.M1)
			ok, _ := session.CheckM1(M1)
			if !ok {
				http.Error(w, "bad proof", http.StatusUnauthorized)
				return
			}
			M2, _ := session.ComputeM2()
			json.NewEncoder(w).Encode(map[string]string{
				"M2":    hex.EncodeToString(M2),
				"token": "jwt-token-xyz",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func TestAuthenticate(t *testing.T) {
	srv := startStubSRPServer(t, "my-password")
	defer srv.Close()

	c := New(srv.URL, "my-password")
	assert.False(t, c.IsAuthenticated())

	err := c.Authenticate()
	require.NoError(t, err)
	assert.True(t, c.IsAuthenticated())
}

func TestAuthenticate_StartError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "pw")
	err := c.Authenticate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "auth srp start")
}

func TestAuthenticate_WrongPassword(t *testing.T) {
	srv := startStubSRPServer(t, "right-password")
	defer srv.Close()

	c := New(srv.URL, "wrong-password")
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

func TestBearerTokenInjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer pre-issued-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "", WithBearerToken("pre-issued-token"))
	assert.True(t, c.IsAuthenticated())
	require.NoError(t, c.doJSON("GET", "/api/anything", nil, nil))
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
	resp := RunsResponse{
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
	resp := RunsResponse{
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
