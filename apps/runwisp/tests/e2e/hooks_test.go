//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	hookOldToken = "old-token-0123456789abcdef0123456789abcdef"
	hookNewToken = "new-token-0123456789abcdef0123456789abcdef"
)

func hookConfig(token string) string {
	return `
[daemon]
shutdown_timeout = "500ms"

[tasks.deploy]
run = "echo deployed"
trigger_tokens = ["` + token + `"]
`
}

// postHookE2E calls the token-authenticated trigger endpoint over real TCP with
// no session, returning the status and (on success) the decoded run.
func postHookE2E(t *testing.T, baseURL, token string) (int, model.Run) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		baseURL+"/api/hooks/deploy?wait=true&waitTimeout=30", strings.NewReader(""))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var run model.Run
	if resp.StatusCode == http.StatusCreated {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&run))
	}
	return resp.StatusCode, run
}

// TestHooks_TriggerTokenAndRotation boots a real password-protected daemon and
// drives POST /api/hooks/{task} the way a CI job would: no session, just the
// task's bearer token. A rotated token takes effect on `runwisp reload` and
// the old one stops working.
func TestHooks_TriggerTokenAndRotation(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := t.TempDir() + "/runwisp.toml"
	writeReloadConfig(t, configPath, hookConfig(hookOldToken))
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	status, _ := postHookE2E(t, daemon.baseURL, "")
	assert.Equal(t, http.StatusUnauthorized, status, "no token must be rejected")
	status, _ = postHookE2E(t, daemon.baseURL, hookNewToken)
	assert.Equal(t, http.StatusUnauthorized, status, "a token not in the config must be rejected")

	status, run := postHookE2E(t, daemon.baseURL, hookOldToken)
	require.Equal(t, http.StatusCreated, status)
	assert.Equal(t, model.TriggeredByToken, run.TriggeredBy)
	require.NotNil(t, run.EndReason, "wait=true must return the finished run")
	assert.Equal(t, model.ReasonSuccess, *run.EndReason)

	writeReloadConfig(t, configPath, hookConfig(hookNewToken))
	out, err := runCLI(t, projectDir, binaryPath,
		"reload", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "reload should succeed: %s", out)

	status, _ = postHookE2E(t, daemon.baseURL, hookOldToken)
	assert.Equal(t, http.StatusUnauthorized, status, "the rotated-out token must stop working after reload")
	status, _ = postHookE2E(t, daemon.baseURL, hookNewToken)
	assert.Equal(t, http.StatusCreated, status)
}
