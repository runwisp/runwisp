// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUmask(t *testing.T) {
	ok := map[string]string{
		"":     "",
		"022":  "0022",
		"0022": "0022",
		"027":  "0027",
		"0777": "0777",
		"000":  "0000",
	}
	for in, want := range ok {
		got, err := parseUmask(in)
		require.NoErrorf(t, err, "parseUmask(%q)", in)
		assert.Equalf(t, want, got, "parseUmask(%q)", in)
	}

	bad := []string{"22", "888", "7777", "abc", "0999", "12345", "0o22"}
	for _, in := range bad {
		_, err := parseUmask(in)
		assert.Errorf(t, err, "parseUmask(%q) should error", in)
	}
}

func TestUmask_CanonicalizedOnTask(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
umask = "27"
`)
	_, err := Load(cfgPath)
	// "27" is two digits — ambiguous, rejected.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "umask")

	cfgPath2, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
umask = "027"
`)
	cfg, err := Load(cfgPath2)
	require.NoError(t, err)
	assert.Equal(t, "0027", findTask(t, cfg, "job").Umask)
}

func TestUmask_RejectedOnCompose(t *testing.T) {
	cfgPath, dir := writePlainConfig(t, `[tasks.job]
compose_file    = "docker-compose.yml"
compose_service = "web"
umask           = "027"
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web:\n    image: nginx\n"), 0644))

	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "umask is not supported on compose")
}
