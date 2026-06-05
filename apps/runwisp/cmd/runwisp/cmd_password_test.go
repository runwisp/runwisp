// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/assert"
)

type fakeCredentialsClient struct {
	creds *apiclient.LocalCredentials
	err   error
}

func (f *fakeCredentialsClient) GetLocalCredentials() (*apiclient.LocalCredentials, error) {
	return f.creds, f.err
}

func TestRunPassword_PrintsPasswordToStdout(t *testing.T) {
	client := &fakeCredentialsClient{
		creds: &apiclient.LocalCredentials{Password: "Kj2x9pQ7mN4vL8rT5wYz1c", Ephemeral: true},
	}
	var stdout, stderr bytes.Buffer

	code := runPassword(&stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitOK, code)
	assert.Equal(t, "Kj2x9pQ7mN4vL8rT5wYz1c\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestRunPassword_EnvVarRefusal(t *testing.T) {
	client := &fakeCredentialsClient{err: apiclient.ErrLocalCredentialsUnavailable}
	var stdout, stderr bytes.Buffer

	code := runPassword(&stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitRefused, code)
	assert.Empty(t, stdout.String(),
		"refusal path must never write the password (or anything) to stdout")
	assert.Contains(t, stderr.String(), "RUNWISP_PASSWORD")
	assert.Contains(t, stderr.String(), "will not disclose")
}

func TestRunPassword_AuthDisabled(t *testing.T) {
	client := &fakeCredentialsClient{err: apiclient.ErrAuthDisabled}
	var stdout, stderr bytes.Buffer

	code := runPassword(&stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitNoAuth, code,
		"no-auth must exit with its own code, never the env-var refusal code")
	assert.Empty(t, stdout.String(),
		"no-auth path must never write anything to stdout")
	assert.Contains(t, stderr.String(), "RUNWISP_NO_AUTH")
	assert.Contains(t, stderr.String(), "no password")
}

func TestRunPassword_DaemonUnreachable(t *testing.T) {
	client := &fakeCredentialsClient{err: errors.New("request failed: dial unix: no such file")}
	var stdout, stderr bytes.Buffer

	code := runPassword(&stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitUnreachable, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "daemon not running at /tmp/runwisp.sock")
}

func TestRunPassword_Internal403(t *testing.T) {
	client := &fakeCredentialsClient{
		err: &apiclient.HTTPStatusError{StatusCode: http.StatusForbidden, Body: "not local"},
	}
	var stdout, stderr bytes.Buffer

	code := runPassword(&stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitInternalGate, code)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "not local-trusted")
}

func TestRunPassword_UnexpectedHTTPStatusFallsThrough(t *testing.T) {
	client := &fakeCredentialsClient{
		err: &apiclient.HTTPStatusError{StatusCode: http.StatusInternalServerError, Body: "boom"},
	}
	var stdout, stderr bytes.Buffer

	code := runPassword(&stdout, &stderr, client, "/tmp/runwisp.sock")
	assert.Equal(t, passwordExitUnexpectedErr, code)
	assert.Empty(t, stdout.String())
	assert.True(t, strings.Contains(stderr.String(), "unexpected error"),
		"non-404/403 HTTP errors must surface as unexpected, not refusal")
}
