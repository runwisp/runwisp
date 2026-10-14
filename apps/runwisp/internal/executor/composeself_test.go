// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerIDInMountinfo(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for name, line := range map[string]string{
		"docker": "812 790 8:1 /var/lib/docker/containers/" + id + "/hostname /etc/hostname rw - ext4 /dev/sda1 rw",
		"podman": "812 790 0:46 /containers/storage/overlay-containers/" + id + "/userdata/hostname /etc/hostname rw - tmpfs tmpfs rw",
	} {
		m := containerIDInMountinfo.FindStringSubmatch(line)
		require.NotNil(t, m, name)
		assert.Equal(t, id, m[1], name)
	}
	assert.Nil(t, containerIDInMountinfo.FindStringSubmatch("26 25 0:23 / /proc rw - proc proc rw"))
}

func TestComposeProjectArgs_OmitsFileForOwnProject(t *testing.T) {
	assert.Equal(t, []string{"compose", "-p", "shop"},
		composeProjectArgs(&model.ComposeExecution{ProjectName: "shop"}))
}

// A unit with only compose_service execs into the project RunWisp's own
// container belongs to: `compose -p <project> exec`, no -f, and nothing that
// needs the project directory.
func TestComposeBackend_Start_OwnProjectExec(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	installDockerShimScript(t, dir, "#!/bin/sh\n"+
		"if [ \"$1\" = inspect ]; then\n  [ \"$6\" = known ] && echo shop\n  exit 0\nfi\n"+
		"if [ \"$1\" = compose ] && [ \"$2\" = version ]; then exit 0; fi\n"+
		"echo \"$@\" > '"+argsFile+"'\n")

	b := &ComposeBackend{dockerCmd: "docker", selfRefs: func() []string { return []string{"unknown", "known"} }}
	ce := &model.ComposeExecution{Service: "app", Mode: model.ComposeModeExec, Command: "php artisan schedule:run"}

	proc, err := b.Start(context.Background(), &model.Task{}, &model.Run{}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	exit, _ := proc.Wait()
	assert.Equal(t, 0, exit)

	recorded, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	assert.Contains(t, string(recorded), "compose -p shop exec -T")
	assert.NotContains(t, string(recorded), " -f ")
	assert.Equal(t, "shop", b.selfProject, "a found project is cached")
	assert.Empty(t, ce.ProjectName, "the unit's definition is not rewritten")
}

func TestComposeBackend_Start_OwnProjectNotFound(t *testing.T) {
	dir := t.TempDir()
	installDockerShimScript(t, dir, "#!/bin/sh\nexit 0\n")

	b := &ComposeBackend{dockerCmd: "docker", selfRefs: func() []string { return []string{"abc"} }}
	_, err := b.Start(context.Background(), &model.Task{}, &model.Run{},
		&model.ComposeExecution{Service: "app", Mode: model.ComposeModeExec, Command: "true"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), composeProjectLabel)
	assert.Empty(t, b.selfProject, "a failed lookup is not cached")
}
