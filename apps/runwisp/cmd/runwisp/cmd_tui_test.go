// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

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
