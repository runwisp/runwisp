// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/notify"
)

func TestResolve_InlineSecretWins(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "https://hooks.slack.test/T/B/Z",
		}},
	}
	got, err := Resolve(cfg, t.TempDir())
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://hooks.slack.test/T/B/Z", got.Notifiers[0].WebhookURL)
}

func TestResolve_EnvSecret(t *testing.T) {
	t.Setenv("RUNWISP_TEST_SLACK_URL", "https://hooks.slack.test/from-env")
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:            "ops",
			Type:          "slack",
			WebhookURLEnv: "RUNWISP_TEST_SLACK_URL",
		}},
	}
	got, err := Resolve(cfg, t.TempDir())
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://hooks.slack.test/from-env", got.Notifiers[0].WebhookURL)
}

func TestResolve_EnvSecretMissing(t *testing.T) {
	t.Setenv("RUNWISP_TEST_MISSING_URL", "")
	require.NoError(t, os.Unsetenv("RUNWISP_TEST_MISSING_URL"))
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:            "ops",
			Type:          "slack",
			WebhookURLEnv: "RUNWISP_TEST_MISSING_URL",
		}},
	}
	_, err := Resolve(cfg, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUNWISP_TEST_MISSING_URL")
	assert.Contains(t, err.Error(), `notifier "ops"`)
}

func TestResolve_FileSecretRelativeToDataDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slack.url"), []byte("https://hooks.slack.test/from-file\n"), 0o600))

	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:             "ops",
			Type:           "slack",
			WebhookURLFile: "slack.url",
		}},
	}
	got, err := Resolve(cfg, dir)
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://hooks.slack.test/from-file", got.Notifiers[0].WebhookURL)
}

func TestResolve_FileSecretAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(abs, []byte("absolute-token\n"), 0o600))

	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:           "tg",
			Type:         "telegram",
			BotTokenFile: abs,
			ChatID:       "-100123",
		}},
	}
	got, err := Resolve(cfg, t.TempDir())
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "absolute-token", got.Notifiers[0].BotToken)
}

func TestResolve_CompiledRulePredicates(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "https://hooks.slack.test/T/B/Z",
		}},
		Routes: []config.NotificationRoute{{
			Kinds:      []string{"run.failed"},
			Severity:   "warn",
			TaskGlob:   "backup-*",
			NotifierID: []string{"ops"},
		}},
	}
	got, err := Resolve(cfg, t.TempDir())
	require.NoError(t, err)
	require.Len(t, got.Rules, 1)
	require.Equal(t, []string{"ops"}, got.Rules[0].ActionIDs)

	matches := got.Rules[0].Match
	require.NotNil(t, matches)

	yes := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "backup-db"}
	assert.True(t, matches(yes))

	wrongKind := &notify.Event{Kind: notify.KindRunSucceeded, Severity: notify.SevInfo, TaskName: "backup-db"}
	assert.False(t, matches(wrongKind))

	wrongSev := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevInfo, TaskName: "backup-db"}
	assert.False(t, matches(wrongSev))

	wrongTask := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "etl-1"}
	assert.False(t, matches(wrongTask))
}
