// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

func TestDefaultConfigPath(t *testing.T) {
	cases := []struct {
		name string
		euid int
		env  string
		want string
	}{
		{"non-root, no env", 1000, "", "runwisp.toml"},
		{"root, no env", 0, "", "/etc/runwisp/runwisp.toml"},
		{"non-root, env set", 1000, "/opt/runwisp/runwisp.toml", "/opt/runwisp/runwisp.toml"},
		{"root, env set wins over euid", 0, "/opt/runwisp/runwisp.toml", "/opt/runwisp/runwisp.toml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, defaultConfigPath(c.euid, c.env))
		})
	}
}

func TestDefaultDataDir(t *testing.T) {
	cases := []struct {
		name string
		euid int
		env  string
		want string
	}{
		{"non-root, no env", 1000, "", ".runwisp"},
		{"root, no env", 0, "", "/var/lib/runwisp"},
		{"non-root, env set", 1000, "/opt/runwisp/data", "/opt/runwisp/data"},
		{"root, env set wins over euid", 0, "/opt/runwisp/data", "/opt/runwisp/data"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, defaultDataDir(c.euid, c.env))
		})
	}
}

func TestResolvePathDefaults_LeavesExplicitFlagsAlone(t *testing.T) {
	orig := flags
	defer func() { flags = orig }()

	flags = Flags{CfgFile: "/explicit/runwisp.toml", DataDir: "/explicit/data"}
	resolvePathDefaults(0)
	assert.Equal(t, "/explicit/runwisp.toml", flags.CfgFile)
	assert.Equal(t, "/explicit/data", flags.DataDir)
}

func TestResolvePathDefaults_FillsEmptyFromEuid(t *testing.T) {
	orig := flags
	defer func() { flags = orig }()

	flags = Flags{}
	resolvePathDefaults(0)
	assert.Equal(t, "/etc/runwisp/runwisp.toml", flags.CfgFile)
	assert.Equal(t, "/var/lib/runwisp", flags.DataDir)
}
