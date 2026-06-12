// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitCodeList renders a TOML array literal of n exit codes, each cycling
// through 0-255 so every entry is individually in range — isolating the
// length-cap check from the per-value range check.
func exitCodeList(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = strconv.Itoa(i % 256)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func TestExitCodes_DefaultsToZero(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []int{0}, findTask(t, cfg, "job").ExitCodes)
}

func TestExitCodes_ExplicitList(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
exit_codes = [0, 2, 75]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 2, 75}, findTask(t, cfg, "job").ExitCodes)
}

func TestExitCodes_InheritedFromDefaults(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[defaults]
exit_codes = [0, 1]

[tasks.inherits]
run = "echo hi"

[tasks.overrides]
run = "echo hi"
exit_codes = [0]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1}, findTask(t, cfg, "inherits").ExitCodes)
	assert.Equal(t, []int{0}, findTask(t, cfg, "overrides").ExitCodes)
}

func TestExitCodes_EmptyListRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
exit_codes = []
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list at least one exit code")
}

func TestExitCodes_OutOfRangeRejected(t *testing.T) {
	for _, code := range []string{"-1", "256", "999"} {
		cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
exit_codes = [`+code+`]
`)
		_, err := Load(cfgPath)
		require.Error(t, err, "code %s should be rejected", code)
		assert.Contains(t, err.Error(), "out of range")
	}
}

func TestExitCodes_AtCapAccepted(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
exit_codes = `+exitCodeList(ExitCodesCap)+`
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Len(t, findTask(t, cfg, "job").ExitCodes, ExitCodesCap)
}

func TestExitCodes_OverCapRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
exit_codes = `+exitCodeList(ExitCodesCap+1)+`
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the cap")
}

// Duplicates are not deduplicated or rejected — locking current behavior.
func TestExitCodes_DuplicatesAccepted(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
exit_codes = [0, 0, 1]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 0, 1}, findTask(t, cfg, "job").ExitCodes)
}

// The 0 and 255 ends of the POSIX range are valid in a multi-entry list.
func TestExitCodes_BoundaryValuesAccepted(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
exit_codes = [0, 128, 255]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 128, 255}, findTask(t, cfg, "job").ExitCodes)
}
