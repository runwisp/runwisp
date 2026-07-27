// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParams_MapsKindKeyAndDefault(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [
  { env = "PROJECT_ID", required = true },
  { arg = "source", required = true },
  { arg = "dest", default = "/backups" },
  { option = "--region", choices = ["us", "eu"] },
  { option = "--limit", type = "number", default = 100 },
  { flag = "--force" },
]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	params := findTask(t, cfg, "backup").Parameters
	require.Len(t, params, 6)

	assert.Equal(t, model.ParamEnv, params[0].Kind)
	assert.Equal(t, "PROJECT_ID", params[0].Key)
	assert.True(t, params[0].Required)

	assert.Equal(t, model.ParamArg, params[2].Kind)
	require.NotNil(t, params[2].Default)
	assert.Equal(t, "/backups", *params[2].Default)

	assert.Equal(t, model.ParamOption, params[4].Kind)
	require.NotNil(t, params[4].Default)
	assert.Equal(t, "100", *params[4].Default, "numeric default canonicalises to string")

	assert.Equal(t, model.ParamFlag, params[5].Kind)
	assert.Equal(t, "--force", params[5].Key)
}

func TestParams_MultipleIdentitiesRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { env = "FOO", arg = "bar" } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
}

func TestParams_DuplicateKeyRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { arg = "source" }, { arg = "source" } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source")
}

func TestParams_OptionMustStartWithDash(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { option = "region" } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
}

func TestParams_EnvCollisionRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
env = { PROJECT_ID = "static" }
params = [ { env = "PROJECT_ID" } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
}

func TestParams_RequiredWithoutDefaultOnScheduledRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
cron = "0 * * * *"
params = [ { arg = "source", required = true } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source")
}

func TestParams_RequiredWithoutDefaultOnManualAllowed(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { arg = "source", required = true } ]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Len(t, findTask(t, cfg, "backup").Parameters, 1)
}

func TestParams_AllowCustomRequiresChoices(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { option = "--tag", allow_custom = true } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
}

func TestParams_FlagRejectsTypeAndChoices(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { flag = "--force", choices = ["a", "b"] } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
}

func TestParams_RejectedOnService(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.web]
run = "server"
params = [ { env = "PORT" } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "params")
}

func TestParams_FlagDefaultCanonicalised(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [
  { flag = "--force", default = 1 },
  { flag = "--verbose", default = "TRUE" },
  { flag = "--quiet", default = false },
]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	params := findTask(t, cfg, "backup").Parameters
	require.Len(t, params, 3)
	require.NotNil(t, params[0].Default)
	assert.Equal(t, "true", *params[0].Default, "int 1 → true")
	assert.Equal(t, "true", *params[1].Default, `"TRUE" → true`)
	assert.Equal(t, "false", *params[2].Default, "bool false → false")
}

func TestParams_FlagNonBooleanDefaultRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { flag = "--force", default = "maybe" } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
}

func TestParams_RequiredFlagRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { flag = "--force", required = true } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestParams_NumericChoicesMustBeNumbers(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { option = "--limit", type = "number", choices = ["10", "twenty"] } ]
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "twenty")
}

func TestParams_NumericChoicesAccepted(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.backup]
run = "backup.sh"
params = [ { option = "--limit", type = "number", choices = ["10", "20"], default = 10 } ]
`)
	_, err := Load(cfgPath)
	require.NoError(t, err)
}

func TestCanonicalizeParamDefault_FloatNoExponent(t *testing.T) {
	got, err := canonicalizeParamDefault(1e21)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "1000000000000000000000", *got, "no exponent notation")

	got, err = canonicalizeParamDefault(1.0)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "1", *got)
}
