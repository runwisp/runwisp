// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCredentialsClient struct {
	creds *server.LocalCredentialsBody
	err   error
}

func (f *fakeCredentialsClient) GetLocalCredentials(context.Context) (*server.LocalCredentialsBody, error) {
	return f.creds, f.err
}

func TestRunPassword_PrintsPasswordToStdout(t *testing.T) {
	client := &fakeCredentialsClient{
		creds: &server.LocalCredentialsBody{Password: "Kj2x9pQ7mN4vL8rT5wYz1c", Ephemeral: true},
	}
	var stdout, stderr bytes.Buffer

	code := runPassword(t.Context(), &stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitOK, code)
	assert.Equal(t, "Kj2x9pQ7mN4vL8rT5wYz1c\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestRunPassword_EnvVarRefusal(t *testing.T) {
	client := &fakeCredentialsClient{err: apiclient.ErrLocalCredentialsUnavailable}
	var stdout, stderr bytes.Buffer

	code := runPassword(t.Context(), &stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitRefused, code)
	assert.Empty(t, stdout.String(),
		"refusal path must never write the password (or anything) to stdout")
	assert.Contains(t, stderr.String(), "RUNWISP_PASSWORD")
	assert.Contains(t, stderr.String(), "will not disclose")
}

func TestRunPassword_AuthDisabled(t *testing.T) {
	client := &fakeCredentialsClient{err: apiclient.ErrAuthDisabled}
	var stdout, stderr bytes.Buffer

	code := runPassword(t.Context(), &stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitNoAuth, code,
		"no-auth must exit with its own code, never the env-var refusal code")
	assert.Empty(t, stdout.String(),
		"no-auth path must never write anything to stdout")
	assert.Contains(t, stderr.String(), "RUNWISP_AUTH=off")
	assert.Contains(t, stderr.String(), "no password")
}

func TestRunPassword_DaemonUnreachable(t *testing.T) {
	client := &fakeCredentialsClient{err: errors.New("request failed: dial unix: no such file")}
	var stdout, stderr bytes.Buffer

	code := runPassword(t.Context(), &stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitUnreachable, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "daemon not running at /tmp/runwisp.sock")
}

func TestRunPassword_Internal403(t *testing.T) {
	client := &fakeCredentialsClient{
		err: &apiclient.HTTPStatusError{StatusCode: http.StatusForbidden, Body: "not local"},
	}
	var stdout, stderr bytes.Buffer

	code := runPassword(t.Context(), &stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitInternalGate, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "not local-trusted")
}

func TestRunPassword_UnexpectedHTTPStatusFallsThrough(t *testing.T) {
	client := &fakeCredentialsClient{
		err: &apiclient.HTTPStatusError{StatusCode: http.StatusInternalServerError, Body: "boom"},
	}
	var stdout, stderr bytes.Buffer

	code := runPassword(t.Context(), &stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitUnexpectedErr, code)
	assert.Empty(t, stdout.String())
	assert.True(t, strings.Contains(stderr.String(), "unexpected error"),
		"non-404/403 HTTP errors must surface as unexpected, not refusal")
}

// fakeSystemUnit points systemUnitFile at a rendered unit running with dataDir,
// so a test can describe a box with the system service installed.
func fakeSystemUnit(t *testing.T, dataDir string) string {
	t.Helper()
	unit := filepath.Join(t.TempDir(), "runwisp.service")
	body, err := autostart.RenderSystemdUnit(autostart.SystemdParams{
		Binary: "/usr/local/bin/runwisp", Config: "/etc/runwisp/runwisp.toml", DataDir: dataDir,
		Host: "127.0.0.1", Port: 9477, Home: "/root", Path: "/usr/bin", System: true,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(unit, body, 0o644))
	prev := systemUnitFile
	t.Cleanup(func() { systemUnitFile = prev })
	systemUnitFile = unit
	return unit
}

// `sudo runwisp password` after a takeover refused, telling the operator to
// find a password they never set: RunWisp generated it and saved it beside the
// unit. It now prints that one, but still never an operator-supplied one.
func TestRunPassword_PrintsThePasswordTheServiceInstallSaved(t *testing.T) {
	dataDir := "/var/lib/runwisp"
	unit := fakeSystemUnit(t, dataDir)
	prevFlags := flags
	t.Cleanup(func() { flags = prevFlags })
	flags.DataDir = dataDir
	require.NoError(t, os.MkdirAll(unit+".d", 0o755))
	require.NoError(t, os.WriteFile(unit+".d/"+autostart.PasswordDropInName,
		[]byte(autostart.ManagedMarker+"\n[Service]\nEnvironment=\"RUNWISP_PASSWORD=s3cr%%t\"\n"), 0o600))

	withheld := func(context.Context) (*server.LocalCredentialsBody, error) {
		return nil, apiclient.ErrLocalCredentialsUnavailable
	}
	var stdout, stderr bytes.Buffer
	code := runPasswordVia(t.Context(), &stdout, &stderr, withheld, savedServicePassword, "/s", "RUNWISP_PASSWORD", startDaemonHint)
	assert.Equal(t, passwordExitOK, code)
	assert.Equal(t, "s3cr%t\n", stdout.String())

	require.NoError(t, os.WriteFile(unit+".d/"+autostart.EnvDropInName,
		[]byte("[Service]\nEnvironment=\"RUNWISP_PASSWORD=mine\"\n"), 0o600))
	_, ok := savedServicePassword()
	assert.False(t, ok, "a RUNWISP_PASSWORD the operator set stays undisclosed")

	flags.DataDir = "/srv/other"
	require.NoError(t, os.Remove(unit+".d/"+autostart.EnvDropInName))
	_, ok = savedServicePassword()
	assert.False(t, ok, "another daemon's password is not the service's")
}

func TestRunPasswordVia_ViewerRefusalNamesViewerEnvVar(t *testing.T) {
	fetch := func(context.Context) (*server.LocalCredentialsBody, error) {
		return nil, apiclient.ErrLocalCredentialsUnavailable
	}
	var stdout, stderr bytes.Buffer

	code := runPasswordVia(t.Context(), &stdout, &stderr, fetch, nil, "/tmp/runwisp.sock", "RUNWISP_VIEWER_PASSWORD", startDaemonHint)
	assert.Equal(t, passwordExitRefused, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "RUNWISP_VIEWER_PASSWORD")
}
