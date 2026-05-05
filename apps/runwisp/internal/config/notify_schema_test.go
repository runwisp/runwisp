// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecode_NotifierAndRoutes(t *testing.T) {
	src := `
[[notifier]]
id = "ops"
type = "slack"
webhook_url_env = "RUNWISP_SLACK_OPS_URL"

[[notifier]]
id = "oncall"
type = "telegram"
bot_token_env = "RUNWISP_TG_TOKEN"
chat_id = "-1001"

[[notification_route]]
match = { kind = ["run.failed"], task = "backup-*" }
notify = ["ops", "oncall", "inapp"]

[notify]
disable_inapp = false
coalesce_window = "30m"

[tasks.backup-db]
cron = "0 3 * * *"
parallelism = 1
on_overlap = "queue"
run = "backup.sh"
notify_on_failure = ["ops"]
`
	cfg, err := decode([]byte(src))
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	assert.Len(t, cfg.Notify.Notifiers, 2)
	assert.Equal(t, "ops", cfg.Notify.Notifiers[0].ID)
	assert.Equal(t, "telegram", cfg.Notify.Notifiers[1].Type)
	assert.False(t, cfg.Notify.DisableInapp)

	require.Len(t, cfg.Notify.Routes, 2, "explicit route + per-task sugar")
	explicit := cfg.Notify.Routes[0]
	assert.Equal(t, []string{"run.failed"}, explicit.Kinds)
	assert.Equal(t, "backup-*", explicit.TaskGlob)
	assert.Equal(t, []string{"ops", "oncall", "inapp"}, explicit.NotifierID)

	synth := cfg.Notify.Routes[1]
	assert.Equal(t, []string{"run.failed", "run.timeout", "run.crashed"}, synth.Kinds)
	assert.Equal(t, "backup-db", synth.TaskGlob)
	assert.Contains(t, synth.NotifierID, "ops")
	assert.Contains(t, synth.NotifierID, "inapp", "per-task sugar must imply inapp by default")
}

func TestValidate_RejectsDuplicateNotifierID(t *testing.T) {
	src := `
[[notifier]]
id = "x"
type = "slack"
webhook_url = "https://example/x"

[[notifier]]
id = "x"
type = "telegram"
bot_token = "t"
chat_id = "-1"
`
	cfg, err := decode([]byte(src))
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate notifier id")
}

func TestValidate_RejectsConflictingSecretSources(t *testing.T) {
	src := `
[[notifier]]
id = "x"
type = "slack"
webhook_url = "https://example/x"
webhook_url_env = "ENV"
`
	cfg, err := decode([]byte(src))
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one of")
}

func TestValidate_RejectsReservedInappID(t *testing.T) {
	src := `
[[notifier]]
id = "inapp"
type = "slack"
webhook_url = "https://example/x"
`
	_, err := decode([]byte(src))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved")
}

func TestValidate_RejectsRouteWithUnknownNotifier(t *testing.T) {
	src := `
[[notification_route]]
match = { kind = ["run.failed"] }
notify = ["does-not-exist"]
`
	cfg, err := decode([]byte(src))
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown notifier id")
}

func TestValidate_RejectsBadKind(t *testing.T) {
	src := `
[[notification_route]]
match = { kind = ["run.bogus"] }
notify = ["inapp"]
`
	cfg, err := decode([]byte(src))
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "match.kind")
}

func TestNotifyConfig_DisableInappDropsImplicitInappFromSugar(t *testing.T) {
	src := `
[[notifier]]
id = "ops"
type = "slack"
webhook_url = "https://example/x"

[notify]
disable_inapp = true

[tasks.backup-db]
cron = "0 3 * * *"
parallelism = 1
on_overlap = "queue"
run = "backup.sh"
notify_on_failure = ["ops"]
`
	cfg, err := decode([]byte(src))
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	require.Len(t, cfg.Notify.Routes, 1)
	assert.NotContains(t, cfg.Notify.Routes[0].NotifierID, "inapp")
}
