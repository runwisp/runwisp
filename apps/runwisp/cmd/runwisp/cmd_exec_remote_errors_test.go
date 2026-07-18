// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// authStatusServer answers the CHAP challenge with a fixed status code, so a
// test can drive authenticateRemote down each of its failure branches.
func authStatusServer(t *testing.T, challengeStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/api/auth/challenge":
			w.WriteHeader(challengeStatus)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestAuthenticateRemote_MissingPassword(t *testing.T) {
	client := apiclient.New("https://example.com", "")
	err := authenticateRemote(client, "https://example.com", "")
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "password is required")
}

func TestAuthenticateRemote_Unauthorized(t *testing.T) {
	srv := authStatusServer(t, http.StatusUnauthorized)
	defer srv.Close()

	err := authenticateRemote(apiclient.New(srv.URL, "pw"), srv.URL, "pw")
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "authentication")
	assert.Contains(t, ufe.Error(), "rejected the password")
}

func TestAuthenticateRemote_RateLimited(t *testing.T) {
	srv := authStatusServer(t, http.StatusTooManyRequests)
	defer srv.Close()

	err := authenticateRemote(apiclient.New(srv.URL, "pw"), srv.URL, "pw")
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "too many authentication attempts")
}

func TestAuthenticateRemote_OtherError(t *testing.T) {
	srv := authStatusServer(t, http.StatusInternalServerError)
	defer srv.Close()

	err := authenticateRemote(apiclient.New(srv.URL, "pw"), srv.URL, "pw")
	require.Error(t, err)
	_, ok := isUserFacing(err)
	assert.False(t, ok, "a transport/5xx failure is a plain wrapped error, not user-facing")
	assert.Contains(t, err.Error(), "authenticate with")
}

// triggerStatusServer answers POST .../run with a fixed status code and serves
// an (empty) task list so daemonTaskNames stays best-effort.
func triggerStatusServer(t *testing.T, runStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/run") && r.Method == http.MethodPost:
			w.WriteHeader(runStatus)
		case r.URL.Path == "/api/tasks":
			_ = json.NewEncoder(w).Encode([]model.Task{{Name: "other"}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTriggerRemote_NotFound(t *testing.T) {
	srv := triggerStatusServer(t, http.StatusNotFound)
	defer srv.Close()

	client := apiclient.New(srv.URL, "pw")
	client.SetToken("tok")
	_, err := triggerRemote(client, "backup", srv.URL, "pw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `task "backup" not found`)
}

func TestTriggerRemote_Forbidden(t *testing.T) {
	srv := triggerStatusServer(t, http.StatusForbidden)
	defer srv.Close()

	client := apiclient.New(srv.URL, "pw")
	client.SetToken("tok")
	_, err := triggerRemote(client, "backup", srv.URL, "pw")
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "cannot be triggered over the API")
}

func TestTriggerRemote_RateLimited(t *testing.T) {
	srv := triggerStatusServer(t, http.StatusTooManyRequests)
	defer srv.Close()

	client := apiclient.New(srv.URL, "pw")
	client.SetToken("tok")
	_, err := triggerRemote(client, "backup", srv.URL, "pw")
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "too many authentication attempts")
}

func TestTriggerRemote_OtherError(t *testing.T) {
	srv := triggerStatusServer(t, http.StatusInternalServerError)
	defer srv.Close()

	client := apiclient.New(srv.URL, "pw")
	client.SetToken("tok")
	_, err := triggerRemote(client, "backup", srv.URL, "pw")
	require.Error(t, err)
	_, ok := isUserFacing(err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), `trigger "backup"`)
}

// TestTriggerRemote_ReauthFailsWithoutPassword exercises the 401 → re-auth path
// where the cached token is stale but no password is available to re-handshake.
func TestTriggerRemote_ReauthFailsWithoutPassword(t *testing.T) {
	srv := triggerStatusServer(t, http.StatusUnauthorized)
	defer srv.Close()

	client := apiclient.New(srv.URL, "")
	client.SetToken("stale")
	_, err := triggerRemote(client, "backup", srv.URL, "")
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "password is required")
}

// TestFollowRun_PollsWhenStreamEndsWithoutDone covers followRun's fallback: the
// SSE stream closes without a done event, so it polls GetRun for the terminal
// exit code.
func TestFollowRun_PollsWhenStreamEndsWithoutDone(t *testing.T) {
	reason := model.ReasonFailed
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// A line but no "done" event, then the stream just ends.
			fmt.Fprintln(w, "event: line")
			fmt.Fprintln(w, `data: {"n":0,"stream":"stdout","text":"hi"}`)
			fmt.Fprintln(w, "")
		case strings.Contains(r.URL.Path, "/runs/"):
			_ = json.NewEncoder(w).Encode(model.Run{
				ID: "run-1", TaskName: "backup", Status: model.PhaseEnded,
				EndReason: &reason, ExitCode: 4,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "pw")
	client.SetToken("tok")
	code, _, err := followRun(client, "backup", "run-1", os.Stdout)
	require.NoError(t, err)
	assert.Equal(t, 4, code, "exit code must come from the polled terminal run")
}

// TestFollowRun_ReconnectsAndPrintsTailWhenStreamDropsWithoutDone is the
// regression guard for the silent-output flake: under load the SSE transport
// can close mid-run without a Done event. followRun must re-open from the next
// unseen line and print the persisted tail rather than exiting having swallowed
// captured output ("nothing silently fails"). The first connection delivers the
// head and drops; only the reconnect (from>0) carries the tail and Done.
func TestFollowRun_ReconnectsAndPrintsTailWhenStreamDropsWithoutDone(t *testing.T) {
	// Capture stdout so we can assert the tail line was actually printed.
	oldOut := os.Stdout
	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = wOut
	t.Cleanup(func() { os.Stdout = oldOut })

	reason := model.ReasonSuccess
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			if r.URL.Query().Get("from") == "0" {
				// First connect: deliver the head, then drop with NO done event.
				fmt.Fprintln(w, "event: line")
				fmt.Fprintln(w, `data: {"n":0,"stream":"stdout","text":"alpha-line-1"}`)
				fmt.Fprintln(w, "")
				return
			}
			// Reconnect from the tail: deliver the rest and the done event.
			fmt.Fprintln(w, "event: line")
			fmt.Fprintln(w, `data: {"n":1,"stream":"stdout","text":"alpha-line-2"}`)
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "event: done")
			fmt.Fprintln(w, "data: {}")
			fmt.Fprintln(w, "")
		case strings.Contains(r.URL.Path, "/runs/"):
			_ = json.NewEncoder(w).Encode(model.Run{
				ID: "run-1", TaskName: "backup", Status: model.PhaseEnded,
				EndReason: &reason, ExitCode: 0,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "pw")
	client.SetToken("tok")
	code, _, err := followRun(client, "backup", "run-1", os.Stdout)
	require.NoError(t, wOut.Close())
	require.NoError(t, err)
	assert.Equal(t, 0, code)

	out, readErr := io.ReadAll(rOut)
	require.NoError(t, readErr)
	assert.Contains(t, string(out), "alpha-line-1")
	assert.Contains(t, string(out), "alpha-line-2",
		"the tail after a mid-stream drop must be re-fetched and printed, not silently swallowed")
}

func TestRunExec_URLWithDaemonFlagRejected(t *testing.T) {
	t.Setenv("RUNWISP_URL", "")
	execFlags.URL = "https://example.com"
	execFlags.Daemon = true
	t.Cleanup(func() { execFlags.URL = ""; execFlags.Daemon = false })

	_, err := runExec("backup", Flags{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--url cannot be combined")
}

// TestRunExec_RoutesToRemoteWithEnvPassword confirms runExec dispatches to the
// remote path when --url is set and reads the password from the environment.
func TestRunExec_RoutesToRemoteWithEnvPassword(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{exitCode: 0, endReason: model.ReasonSuccess}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	execFlags.URL = srv.URL
	t.Setenv("RUNWISP_PASSWORD", "pw")
	t.Cleanup(func() { execFlags.URL = "" })

	code, err := runExec("backup", Flags{})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, int32(1), stub.authCount.Load(), "env password drove one CHAP handshake")
}
