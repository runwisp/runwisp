// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFlags_DBPath_JoinsDataDir(t *testing.T) {
	f := Flags{DataDir: "/var/lib/runwisp"}
	assert.Equal(t, filepath.Join("/var/lib/runwisp", "runwisp.db"), f.DBPath())
}

func TestFlags_DBPath_EmptyDataDir(t *testing.T) {
	f := Flags{}
	assert.Equal(t, "runwisp.db", f.DBPath())
}

func TestFlags_LogDir_JoinsDataDir(t *testing.T) {
	f := Flags{DataDir: "/var/lib/runwisp"}
	assert.Equal(t, filepath.Join("/var/lib/runwisp", "logs"), f.LogDir())
}
