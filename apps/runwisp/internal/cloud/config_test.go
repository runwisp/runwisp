// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigDisabledWhenNoToken(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_TOKEN", "")

	cfg, err := LoadConfig("1.0.0", "", "", "fp")
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
}

func TestLoadConfigEnabledWithTokenOverride(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_TOKEN", "")
	t.Setenv("RUNWISP_CLOUD_URL", "")

	cfg, err := LoadConfig("1.0.0", "rt_token", "", "fp")
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "rt_token", cfg.CloudToken)
	assert.Equal(t, defaultCloudURL, cfg.BaseURL.String())
}

func TestLoadConfigTokenFromEnv(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_TOKEN", "env_token")
	t.Setenv("RUNWISP_CLOUD_URL", "")
	t.Setenv("RUNWISP_CLOUD_ALLOW_INSECURE", "")

	cfg, err := LoadConfig("1.0.0", "", "", "fp")
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "env_token", cfg.CloudToken)
}

func TestLoadConfigInvalidURL(t *testing.T) {
	// A URL with a control character is unparseable.
	_, err := LoadConfig("1.0.0", "tok", "://bad url\x00", "fp")
	require.Error(t, err)
}

func TestLoadConfigHTTPWithoutAllowInsecure(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_ALLOW_INSECURE", "")

	_, err := LoadConfig("1.0.0", "tok", "http://localhost:3000", "fp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insecure")
}

func TestLoadConfigHTTPWithAllowInsecure(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_ALLOW_INSECURE", "true")

	cfg, err := LoadConfig("1.0.0", "tok", "http://localhost:3000", "fp")
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "http", cfg.BaseURL.Scheme)
}

func TestLoadConfigDefaultAgentVersion(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_URL", "")
	t.Setenv("RUNWISP_CLOUD_ALLOW_INSECURE", "")

	cfg, err := LoadConfig("", "tok", "", "fp")
	require.NoError(t, err)
	assert.Equal(t, "0.0.0", cfg.AgentVersion)
}

func TestLoadConfigURLFromEnv(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_TOKEN", "tok")
	t.Setenv("RUNWISP_CLOUD_URL", "https://custom.example.com")
	t.Setenv("RUNWISP_CLOUD_ALLOW_INSECURE", "")

	cfg, err := LoadConfig("1.0.0", "", "", "fp")
	require.NoError(t, err)
	assert.Equal(t, "https", cfg.BaseURL.Scheme)
	assert.Equal(t, "custom.example.com", cfg.BaseURL.Host)
}

func TestLoadConfigURLOverrideTakesPrecedenceOverEnv(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_URL", "https://env.example.com")

	cfg, err := LoadConfig("1.0.0", "tok", "https://override.example.com", "fp")
	require.NoError(t, err)
	assert.Equal(t, "override.example.com", cfg.BaseURL.Host)
}

func TestLoadConfigFromEnv_TokenSet(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_TOKEN", "env-token")
	t.Setenv("RUNWISP_CLOUD_URL", "")
	t.Setenv("RUNWISP_CLOUD_ALLOW_INSECURE", "")

	cfg, err := LoadConfigFromEnv("1.0.0", "fp")
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, "env-token", cfg.CloudToken)
}

func TestLoadConfigFromEnv_NoToken(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_TOKEN", "")
	t.Setenv("RUNWISP_CLOUD_URL", "")

	cfg, err := LoadConfigFromEnv("1.0.0", "fp")
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
}

func TestLoadConfigInvalidScheme(t *testing.T) {
	_, err := LoadConfig("1.0.0", "tok", "ftp://example.com", "fp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scheme")
}

func TestLoadConfigMissingHost(t *testing.T) {
	_, err := LoadConfig("1.0.0", "tok", "https://", "fp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host")
}

func TestConfig_WebSocketURL_HTTPS(t *testing.T) {
	cfg, err := LoadConfig("1.0.0", "tok", "https://app.runwisp.com", "fp")
	require.NoError(t, err)
	wsURL := cfg.WebSocketURL()
	assert.Contains(t, wsURL, "wss://")
}

func TestConfig_WebSocketURL_HTTP(t *testing.T) {
	t.Setenv("RUNWISP_CLOUD_ALLOW_INSECURE", "true")
	cfg, err := LoadConfig("1.0.0", "tok", "http://localhost:8080", "fp")
	require.NoError(t, err)
	wsURL := cfg.WebSocketURL()
	assert.Contains(t, wsURL, "ws://")
}

func TestConfig_TaskSyncURL(t *testing.T) {
	cfg, err := LoadConfig("1.0.0", "tok", "https://app.runwisp.com", "fp")
	require.NoError(t, err)
	assert.Contains(t, cfg.TaskSyncURL(), "/api/v1/runner/tasks/sync")
}

func TestConfig_WebSocketURL_Path(t *testing.T) {
	cfg, err := LoadConfig("1.0.0", "tok", "https://app.runwisp.com", "fp")
	require.NoError(t, err)
	assert.Contains(t, cfg.WebSocketURL(), "/api/v1/runner/ws")
}
