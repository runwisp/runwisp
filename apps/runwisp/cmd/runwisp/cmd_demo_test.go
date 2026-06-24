// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/assert"
)

func TestReportDemoNoTUI_PasswordToStdoutGuidanceToStderr(t *testing.T) {
	client := &fakeCredentialsClient{
		creds: &apiclient.LocalCredentials{Password: "Kj2x9pQ7mN4vL8rT5wYz1c", Ephemeral: true},
	}
	f := Flags{Host: "127.0.0.1", Port: 9477, DataDir: "/tmp/runwisp-demo-xyz/data"}
	var stdout, stderr bytes.Buffer

	code := reportDemoNoTUI(&stdout, &stderr, client, f)

	assert.Equal(t, passwordExitOK, code)
	assert.Equal(t, "Kj2x9pQ7mN4vL8rT5wYz1c\n", stdout.String(),
		"stdout must carry only the password so the command stays pipeable")
	assert.Contains(t, stderr.String(), "http://127.0.0.1:9477",
		"operator needs the Web UI URL to connect")
	assert.Contains(t, stderr.String(), "runwisp stop --data /tmp/runwisp-demo-xyz/data",
		"operator needs the shutdown command for the throwaway daemon")
}

func TestReportDemoNoTUI_WildcardBindReportsLocalhost(t *testing.T) {
	client := &fakeCredentialsClient{
		creds: &apiclient.LocalCredentials{Password: "pw", Ephemeral: true},
	}
	f := Flags{Host: "0.0.0.0", Port: 8080, DataDir: "/tmp/demo/data"}
	var stdout, stderr bytes.Buffer

	reportDemoNoTUI(&stdout, &stderr, client, f)

	assert.Contains(t, stderr.String(), "http://localhost:8080",
		"a 0.0.0.0 bind is not connectable; report localhost")
}

func TestReportDemoNoTUI_NoPasswordExitsNonZeroWithoutLeaking(t *testing.T) {
	client := &fakeCredentialsClient{err: apiclient.ErrAuthDisabled}
	f := Flags{Host: "127.0.0.1", Port: 9477, DataDir: "/tmp/demo/data"}
	var stdout, stderr bytes.Buffer

	code := reportDemoNoTUI(&stdout, &stderr, client, f)

	assert.Equal(t, passwordExitNoAuth, code)
	assert.Empty(t, stdout.String(), "stdout must stay empty when there is no password to print")
}
