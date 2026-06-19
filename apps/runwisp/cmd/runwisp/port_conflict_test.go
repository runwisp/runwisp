// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testInstance() *model.InstanceInfo {
	return &model.InstanceInfo{
		App:        "runwisp",
		Version:    "1.2.3",
		Pid:        4242,
		DataDir:    "/srv/other/data",
		ConfigPath: "/srv/other/runwisp.toml",
		SocketPath: "/srv/other/data/runwisp.sock",
	}
}

func TestResolvePortConflict_NotRunwisp(t *testing.T) {
	f := Flags{Host: "127.0.0.1", Port: 9477}
	bind := errors.New("bind: address already in use")

	choice, err := resolvePortConflict(f, bind, nil, true, strings.NewReader(""), &strings.Builder{})

	assert.Equal(t, conflictAbort, choice)
	require.Error(t, err)
	// Generic message, since the port-holder is not a RunWisp daemon.
	assert.Contains(t, err.Error(), "another process")
}

func TestResolvePortConflict_NonInteractiveNamesInstance(t *testing.T) {
	f := Flags{Host: "127.0.0.1", Port: 9477}

	choice, err := resolvePortConflict(f, nil, testInstance(), false, nil, nil)

	assert.Equal(t, conflictAbort, choice)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "/srv/other/data", "names the other daemon's datadir")
	assert.Contains(t, msg, "/srv/other/data/runwisp.sock", "suggests connecting over its socket")
	assert.Contains(t, msg, "1.2.3", "names the version")
}

func TestResolvePortConflict_InteractiveChoices(t *testing.T) {
	f := Flags{Host: "127.0.0.1", Port: 9477}

	cases := map[string]struct {
		input string
		want  conflictChoice
	}{
		"connect":      {"c\n", conflictConnect},
		"connect word": {"connect\n", conflictConnect},
		"stop":         {"s\n", conflictStopAndLaunch},
		"quit":         {"q\n", conflictAbort},
		"empty quits":  {"\n", conflictAbort},
		"eof quits":    {"", conflictAbort},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			choice, err := resolvePortConflict(f, nil, testInstance(), true, strings.NewReader(tc.input), &out)
			require.NoError(t, err)
			assert.Equal(t, tc.want, choice)
			// The operator is always shown which datadir the daemon uses.
			assert.Contains(t, out.String(), "/srv/other/data")
		})
	}
}

func TestResolvePortConflict_InteractiveRetriesOnGarbage(t *testing.T) {
	f := Flags{Host: "127.0.0.1", Port: 9477}
	var out strings.Builder

	choice, err := resolvePortConflict(f, nil, testInstance(), true, strings.NewReader("huh?\ns\n"), &out)

	require.NoError(t, err)
	assert.Equal(t, conflictStopAndLaunch, choice)
	assert.Contains(t, out.String(), "Please answer c, s, or q.")
}
