// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShellSupportsErrexit(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"", true}, // empty falls back to /bin/sh
		{"/bin/sh", true},
		{"/bin/bash", true},
		{"/bin/dash", true},
		{"/usr/local/bin/dash", true},
		{"/nix/store/abc123-bash-5.2/bin/bash", true},
		{"/opt/homebrew/bin/zsh", true},
		{"/bin/busybox", true},
		{"/usr/bin/python3", false},
		{"/usr/bin/perl", false},
		{"/usr/bin/fish", false},
		{"/opt/node/bin/node", false},
		{"/usr/bin/bash5", false}, // exact basenames only, no suffix guessing
	} {
		assert.Equal(t, tc.want, ShellSupportsErrexit(tc.path), "shell %q", tc.path)
	}
}

// TestBuildDockerfile_ArmsErrexit pins the container backend to the same
// fail-fast contract as the host shell backend: a multi-line script whose
// middle line fails must not exit 0 and be recorded as a successful run.
func TestBuildDockerfile_ArmsErrexit(t *testing.T) {
	exec := &ContainerExecution{BaseImage: "alpine:3.22", Script: "echo hi"}

	dockerfile := exec.BuildDockerfile()

	assert.Contains(t, dockerfile, `ENTRYPOINT ["/bin/sh", "-e", "/runwisp-script.sh"]`)
}

// TestBuildDockerfile_RawDockerfileWins documents that an explicit Dockerfile
// is passed through untouched — including its ENTRYPOINT, so the errexit
// default above is ours to set only when we generate the file.
func TestBuildDockerfile_RawDockerfileWins(t *testing.T) {
	exec := &ContainerExecution{Dockerfile: "FROM scratch\n", BaseImage: "ignored"}

	assert.Equal(t, "FROM scratch\n", exec.BuildDockerfile())
}
