// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleCLIError_UserFacingKeepsRichRendering(t *testing.T) {
	var buf bytes.Buffer
	handleCLIError(&buf, fang.Styles{}, &userFacingError{
		title:   "task \"bkup\" not found",
		details: "Did you mean \"backup\"?",
	})
	out := buf.String()
	// The branded renderer emits the ERROR badge, the title, and the details
	// verbatim. userFacing errors never get the --help hint.
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "task \"bkup\" not found")
	assert.Contains(t, out, "Did you mean")
	assert.NotContains(t, out, "runwisp --help")
}

func TestHandleCLIError_UsageErrorGetsHelpHint(t *testing.T) {
	var buf bytes.Buffer
	handleCLIError(&buf, fang.Styles{}, errors.New(`unknown command "install" for "runwisp"`))
	out := buf.String()
	// Generic cobra usage errors share the same ERROR badge and get a --help hint.
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "unknown command")
	assert.Contains(t, out, "Run 'runwisp --help' for usage.")
}

func TestHandleCLIError_NonUsageErrorHasNoHelpHint(t *testing.T) {
	var buf bytes.Buffer
	handleCLIError(&buf, fang.Styles{}, errors.New("config parse failed: unexpected token"))
	out := buf.String()
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "config parse failed")
	// A --help hint would send the operator down the wrong path for a parse error.
	assert.NotContains(t, out, "runwisp --help")
}

func TestIsUsageError(t *testing.T) {
	for _, msg := range []string{
		`unknown command "install" for "runwisp"`,
		"unknown flag: --nope",
		"unknown shorthand flag: 'x' in -x",
		"flag needs an argument: --port",
		"invalid argument \"abc\" for \"--port\"",
		`required flag(s) "config" not set`,
		"accepts 1 arg(s), received 0",
	} {
		assert.True(t, isUsageError(errors.New(msg)), msg)
	}
	assert.False(t, isUsageError(errors.New("config parse failed: unexpected token")))
}

func TestUserFacingError_ErrorTitleOnly(t *testing.T) {
	e := &userFacingError{title: "boom"}
	assert.Equal(t, "boom", e.Error())
}

func TestUserFacingError_ErrorWithDetails(t *testing.T) {
	e := &userFacingError{title: "boom", details: "more context"}
	out := e.Error()
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "more context")
}

func TestRenderError_TitleOnly(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, "boom", "", "")
	out := buf.String()
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "boom")
}

func TestRenderError_WithDetailsAndBullets(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, "boom", "context line\n  - fix one\n  - fix two\n", "")
	out := buf.String()
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "context line")
	assert.Contains(t, out, "fix one")
	assert.Contains(t, out, "fix two")
}

func TestRenderError_WithHint(t *testing.T) {
	var buf bytes.Buffer
	renderError(&buf, "boom", "", "Run 'runwisp --help' for usage.")
	out := buf.String()
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "Run 'runwisp --help' for usage.")
}

func TestAuthRateLimitedError_HasHints(t *testing.T) {
	err := authRateLimitedError(9477)
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "9477")
	assert.Contains(t, ufe.details, "Wait")
}

func TestIsUserFacing_Direct(t *testing.T) {
	e := &userFacingError{title: "direct error"}
	got, ok := isUserFacing(e)
	require.True(t, ok)
	assert.Equal(t, "direct error", got.title)
}

func TestIsUserFacing_Wrapped(t *testing.T) {
	inner := &userFacingError{title: "inner"}
	wrapped := fmt.Errorf("outer: %w", inner)
	got, ok := isUserFacing(wrapped)
	require.True(t, ok)
	assert.Equal(t, "inner", got.title)
}

func TestIsUserFacing_NotUserFacing(t *testing.T) {
	_, ok := isUserFacing(errors.New("plain error"))
	assert.False(t, ok)
}

func TestIsUserFacing_Nil(t *testing.T) {
	_, ok := isUserFacing(nil)
	assert.False(t, ok)
}

func TestRemoteAuthRequiredError(t *testing.T) {
	err := remoteAuthRequiredError("https://example.com")
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "https://example.com")
	assert.Contains(t, ufe.details, "RUNWISP_PASSWORD")
}

func TestRemoteAuthFailedError(t *testing.T) {
	err := remoteAuthFailedError("https://example.com")
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "authentication")
	assert.Contains(t, ufe.title, "https://example.com")
	assert.Contains(t, ufe.details, "rejected the password")
}

func TestRemoteRateLimitedError(t *testing.T) {
	err := remoteRateLimitedError("https://example.com")
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "too many authentication attempts")
	assert.Contains(t, ufe.details, "Wait a few minutes")
}

func TestRemoteUnreachableError(t *testing.T) {
	err := remoteUnreachableError("https://example.com", errors.New("dial tcp: connection refused"))
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "not reachable")
	assert.Contains(t, ufe.title, "connection refused")
	assert.Contains(t, ufe.details, "URL is correct")
}

func TestRemoteAPITriggerDisabledError(t *testing.T) {
	err := remoteAPITriggerDisabledError("backup")
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, `"backup"`)
	assert.Contains(t, ufe.details, "api_trigger = false")
}

func TestRunwispPortConflictError(t *testing.T) {
	info := &model.InstanceInfo{
		Version:    "1.2.3",
		Pid:        4242,
		DataDir:    "/var/lib/other",
		ConfigPath: "/etc/other/runwisp.toml",
		SocketPath: "/var/lib/other/runwisp.sock",
	}

	t.Run("names the running instance and how to reach it", func(t *testing.T) {
		err := runwispPortConflictError("", 9477, info)
		var ufe *userFacingError
		require.True(t, errors.As(err, &ufe))
		assert.Contains(t, ufe.title, "v1.2.3")
		assert.Contains(t, ufe.title, "pid 4242")
		assert.Contains(t, ufe.title, "127.0.0.1:9477")
		assert.Contains(t, ufe.details, "/var/lib/other")
		assert.Contains(t, ufe.details, "/etc/other/runwisp.toml")
		assert.Contains(t, ufe.details, "--socket /var/lib/other/runwisp.sock")
		assert.Contains(t, ufe.details, "stop --data /var/lib/other")
	})

	t.Run("honours an explicit host", func(t *testing.T) {
		err := runwispPortConflictError("0.0.0.0", 9477, info)
		var ufe *userFacingError
		require.True(t, errors.As(err, &ufe))
		assert.Contains(t, ufe.title, "0.0.0.0:9477")
	})
}

func TestInstanceSummaryLines(t *testing.T) {
	lines := instanceSummaryLines(&model.InstanceInfo{
		DataDir:    "/data",
		ConfigPath: "/cfg/runwisp.toml",
	})
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "/data")
	assert.Contains(t, lines[1], "/cfg/runwisp.toml")
}

func TestCertPinMismatchError(t *testing.T) {
	err := certPinMismatchError("https://daemon.example:9477", &apiclient.CertPinMismatchError{
		Host:   "https://daemon.example:9477",
		Pinned: "aaaa",
		Got:    "bbbb",
	})
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "TLS certificate")
	assert.Contains(t, ufe.title, "https://daemon.example:9477")
	assert.Contains(t, ufe.details, "sha256:aaaa")
	assert.Contains(t, ufe.details, "sha256:bbbb")
	assert.Contains(t, ufe.details, "may be intercepted")
}

func TestUnknownTaskError(t *testing.T) {
	t.Run("suggests closest match and lists tasks", func(t *testing.T) {
		err := unknownTaskError("backap", []string{"cleanup", "backup"})
		msg := err.Error()
		assert.Contains(t, msg, `task "backap" not found`)
		assert.Contains(t, msg, `Did you mean "backup"?`)
		assert.Contains(t, msg, "Available tasks:")
		assert.Contains(t, msg, "- backup")
		assert.Contains(t, msg, "- cleanup")
	})

	t.Run("no suggestion for gibberish", func(t *testing.T) {
		err := unknownTaskError("zzqxwy", []string{"backup"})
		assert.NotContains(t, err.Error(), "Did you mean")
		assert.Contains(t, err.Error(), "- backup")
	})

	t.Run("empty task set points at runwisp.toml", func(t *testing.T) {
		err := unknownTaskError("anything", nil)
		assert.Contains(t, err.Error(), "No tasks are defined")
		assert.Contains(t, err.Error(), "runwisp.toml")
	})
}
