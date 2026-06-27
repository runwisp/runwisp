// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/internal/chap"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDaemon spins up an httptest server that speaks the handful of endpoints
// the remote-TUI bootstrap touches: /health, /api/auth/status, and the CHAP
// pair (/api/auth/challenge, /api/auth). password is the expected CHAP secret;
// an empty password means /api/auth always 401s. authRequired toggles what the
// status probe reports.
type fakeDaemon struct {
	authRequired bool
	password     string
	statusCode   int // when non-zero, /api/auth/status returns this status
	challengeErr int // when non-zero, /api/auth/challenge returns this status
}

func (d fakeDaemon) start(t *testing.T) *httptest.Server {
	t.Helper()
	const nonce = "test-nonce"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/api/auth/status":
			if d.statusCode != 0 {
				w.WriteHeader(d.statusCode)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"auth_required": d.authRequired})
		case "/api/auth/challenge":
			if d.challengeErr != 0 {
				w.WriteHeader(d.challengeErr)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": nonce})
		case "/api/auth":
			var body struct {
				Response string `json:"response"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if d.password == "" || body.Response != chap.Response(d.password, nonce) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "fake-jwt"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// isolatedTokenCache points the token cache at a fresh temp dir so tests neither
// read a developer's real cache nor leak into it.
func isolatedTokenCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

func TestRunTUIViaRemote_Unreachable(t *testing.T) {
	isolatedTokenCache(t)
	// Nothing is listening on this port, so the health probe must fail fast and
	// surface an unreachable-daemon error rather than an auth error.
	err := runTUIViaRemote("http://127.0.0.1:1", Flags{})
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "not reachable")
}

func TestRunTUIViaRemote_AuthStatusError(t *testing.T) {
	isolatedTokenCache(t)
	srv := fakeDaemon{statusCode: http.StatusInternalServerError}.start(t)
	err := runTUIViaRemote(srv.URL, Flags{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check authentication status")
}

func TestRunTUIViaRemote_NoAuthReachesTUILaunch(t *testing.T) {
	isolatedTokenCache(t)
	srv := fakeDaemon{authRequired: false}.start(t)
	// A RUNWISP_NO_AUTH daemon needs no password: the bootstrap should sail past
	// auth and into the shared launch path, which declines here because the test
	// process has no interactive terminal.
	err := runTUIViaRemote(srv.URL, Flags{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no interactive terminal")
}

func TestRunTUIViaRemote_EnvPasswordAuthenticates(t *testing.T) {
	isolatedTokenCache(t)
	t.Setenv("RUNWISP_PASSWORD", "s3cret")
	srv := fakeDaemon{authRequired: true, password: "s3cret"}.start(t)
	// CHAP succeeds with the env password, then the launch path declines for lack
	// of a terminal — proving the whole authenticate-then-launch chain ran.
	err := runTUIViaRemote(srv.URL, Flags{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no interactive terminal")
}

func TestAuthenticateRemoteTUI_UsesCachedToken(t *testing.T) {
	isolatedTokenCache(t)
	baseURL := "http://daemon.example:9477"
	// Seed the cache so authentication short-circuits without any network call.
	storeCachedToken(baseURL, "cached-jwt")

	client, err := authenticateRemoteTUI(baseURL)
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "cached-jwt", client.Token())
}

func TestAuthenticateRemoteTUI_EnvPasswordStoresToken(t *testing.T) {
	isolatedTokenCache(t)
	t.Setenv("RUNWISP_PASSWORD", "s3cret")
	srv := fakeDaemon{authRequired: true, password: "s3cret"}.start(t)

	client, err := authenticateRemoteTUI(srv.URL)
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, "fake-jwt", client.Token())
	// The freshly minted token must be cached for the next invocation.
	assert.Equal(t, "fake-jwt", loadCachedToken(srv.URL))
}

func TestAuthenticateRemoteTUI_WrongEnvPasswordFails(t *testing.T) {
	isolatedTokenCache(t)
	t.Setenv("RUNWISP_PASSWORD", "wrong")
	srv := fakeDaemon{authRequired: true, password: "right"}.start(t)

	_, err := authenticateRemoteTUI(srv.URL)
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "authentication")
}

func TestAuthenticateRemoteTUI_RateLimited(t *testing.T) {
	isolatedTokenCache(t)
	t.Setenv("RUNWISP_PASSWORD", "s3cret")
	srv := fakeDaemon{authRequired: true, password: "s3cret", challengeErr: http.StatusTooManyRequests}.start(t)

	_, err := authenticateRemoteTUI(srv.URL)
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "too many authentication attempts")
}

func TestAuthenticateRemoteTUI_TransientServerError(t *testing.T) {
	isolatedTokenCache(t)
	t.Setenv("RUNWISP_PASSWORD", "s3cret")
	srv := fakeDaemon{authRequired: true, password: "s3cret", challengeErr: http.StatusInternalServerError}.start(t)

	_, err := authenticateRemoteTUI(srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authenticate with")
}

func TestAuthenticateRemoteTUI_NonInteractiveNoPasswordFails(t *testing.T) {
	isolatedTokenCache(t)
	// No cached token, no env password, and the test process is not a terminal —
	// there is nowhere to read a password from, so this is a hard error.
	srv := fakeDaemon{authRequired: true, password: "s3cret"}.start(t)

	_, err := authenticateRemoteTUI(srv.URL)
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "password is required")
}

func TestBuildStartupInfoFromDaemon_NilReturnsEmpty(t *testing.T) {
	si := buildStartupInfoFromDaemon(nil)
	assert.Empty(t, si.Version)
	assert.Nil(t, si.Tasks)
}

func TestBuildStartupInfoFromDaemon_PopulatesAllFields(t *testing.T) {
	info := &model.DaemonInfo{
		Version:          "1.2.3",
		Fingerprint:      "fp-xyz",
		Port:             9477,
		CloudEnabled:     true,
		ResolvedTimezone: "Europe/Berlin",
		TimezoneSource:   "system",
		Tasks: []model.TaskBrief{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	si := buildStartupInfoFromDaemon(info)
	assert.Equal(t, "1.2.3", si.Version)
	assert.Equal(t, "fp-xyz", si.Fingerprint)
	assert.Equal(t, 9477, si.Port)
	assert.True(t, si.CloudEnabled)
	assert.Equal(t, "Europe/Berlin", si.Timezone)
	assert.Equal(t, "system", si.TimezoneSource)
	assert.Len(t, si.Tasks, 2)
	assert.Equal(t, "alpha", si.Tasks[0].Name)
}

func TestRunTUIClient_DaemonUnreachable(t *testing.T) {
	t.Parallel()
	err := runTUIClient(Flags{DataDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error when no daemon is reachable")
	}
}

func TestResolveRemotePassword_EnvWins(t *testing.T) {
	// An env-var password is used as-is, even non-interactively (CI path).
	got, err := resolveRemotePassword("http://host:9477", "s3cret", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "s3cret" {
		t.Fatalf("password = %q, want %q", got, "s3cret")
	}
}

func TestResolveRemotePassword_NonInteractiveNoEnvErrors(t *testing.T) {
	// No env var and no terminal to prompt at — there is nowhere safe to read
	// the password from, so this must be a clear, actionable error.
	_, err := resolveRemotePassword("http://host:9477", "", false)
	if err == nil {
		t.Fatal("expected an error when no password is available and not a terminal")
	}
	var ufe *userFacingError
	if !errors.As(err, &ufe) {
		t.Fatalf("expected a userFacingError, got %T", err)
	}
}

func TestResolveTUIListenURL(t *testing.T) {
	cases := []struct {
		name string
		info *model.DaemonInfo
		mode tuiConnectMode
		want string
	}{
		{
			name: "external_url wins for remote",
			info: &model.DaemonInfo{Port: 9477, ExternalURL: "https://runwisp.example.com"},
			mode: tuiConnectMode{remote: true, connBaseURL: "http://10.0.0.5:9477"},
			want: "https://runwisp.example.com",
		},
		{
			name: "remote without external_url uses connection URL",
			info: &model.DaemonInfo{Port: 9477},
			mode: tuiConnectMode{remote: true, connBaseURL: "http://10.0.0.5:9477"},
			want: "http://10.0.0.5:9477",
		},
		{
			name: "local falls back to localhost:port",
			info: &model.DaemonInfo{Port: 9477},
			mode: tuiConnectMode{},
			want: "http://localhost:9477",
		},
		{
			name: "local prefers external_url",
			info: &model.DaemonInfo{Port: 9477, ExternalURL: "https://runwisp.example.com"},
			mode: tuiConnectMode{},
			want: "https://runwisp.example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveTUIListenURL(tc.info, tc.mode); got != tc.want {
				t.Fatalf("resolveTUIListenURL = %q, want %q", got, tc.want)
			}
		})
	}
}
