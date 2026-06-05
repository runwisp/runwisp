// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schedulerTZHeader prefixes a `[scheduler] timezone = "UTC"` block so cron
// tasks in test fixtures satisfy the post-#4 fail-closed timezone validation.
const schedulerTZHeader = `
[scheduler]
timezone = "UTC"
`

func TestDecode_NotifierAndRoutes(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "ops"
type = "slack"
webhook_url = "https://hooks.slack.test/ops"

[[notifier]]
id = "oncall"
type = "telegram"
bot_token = "tok"
chat_id = "-1001"

[[notification_route]]
match = { kind = ["run.failed"], task = "backup-*" }
notify = ["ops", "oncall", "inapp"]

[notify]
global_notifiers = ["inapp"]
coalesce_window = "30m"

[tasks.backup-db]
cron = "0 3 * * *"
max_concurrent = 1
on_overlap = "queue"
run = "backup.sh"
notify_on_failure = ["ops"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	assert.Len(t, cfg.Notify.Notifiers, 2)
	assert.Equal(t, "ops", cfg.Notify.Notifiers[0].ID)
	assert.Equal(t, "telegram", cfg.Notify.Notifiers[1].Type)
	assert.Equal(t, []string{"inapp"}, cfg.Notify.GlobalNotifiers)

	require.Len(t, cfg.Notify.Routes, 3, "explicit route + per-task sugar + default inapp catch-all")
	explicit := cfg.Notify.Routes[0]
	assert.Equal(t, []string{"run.failed"}, explicit.Kinds)
	assert.Equal(t, "backup-*", explicit.TaskGlob)
	assert.Equal(t, []string{"ops", "oncall", "inapp"}, explicit.NotifierID)

	synth := cfg.Notify.Routes[1]
	assert.Equal(t, []string{"run.failed", "run.timeout", "run.crashed"}, synth.Kinds)
	assert.Equal(t, "backup-db", synth.TaskGlob)
	assert.Contains(t, synth.NotifierID, "ops")
	assert.Contains(t, synth.NotifierID, "inapp", "per-task sugar must imply inapp by default")

	defaultRoute := cfg.Notify.Routes[2]
	assert.Equal(t, []string{"run.failed", "run.timeout", "run.crashed"}, defaultRoute.Kinds)
	assert.Empty(t, defaultRoute.TaskGlob, "default catch-all matches every task")
	assert.Equal(t, []string{"inapp"}, defaultRoute.NotifierID)
}

func TestNotifyConfig_DefaultInappRouteWithZeroConfig(t *testing.T) {
	src := schedulerTZHeader + `
[tasks.process-event-queue]
cron        = "*/10 * * * *"
max_concurrent = 1
on_overlap  = "queue"
run         = "exit 1"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	require.Len(t, cfg.Notify.Routes, 1,
		"zero-config TOML must still produce the default in-app catch-all route")
	r := cfg.Notify.Routes[0]
	assert.Equal(t, []string{"run.failed", "run.timeout", "run.crashed"}, r.Kinds)
	assert.Empty(t, r.TaskGlob)
	assert.Equal(t, []string{"inapp"}, r.NotifierID)
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
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate notifier id")
}

// TestDecode_RejectsRemovedSecretSourceKeys pins the breaking change: the
// per-key _env/_file triplets are gone; ${VAR} / ${file:...} substitution
// replaced them. DisallowUnknownFields turns each removed key into a hard
// decode error naming the key.
func TestDecode_RejectsRemovedSecretSourceKeys(t *testing.T) {
	for _, key := range []string{
		"webhook_url_env", "webhook_url_file",
		"bot_token_env", "bot_token_file",
		"password_env", "password_file",
	} {
		t.Run(key, func(t *testing.T) {
			src := `
[[notifier]]
id = "x"
type = "slack"
` + key + ` = "whatever"
`
			_, err := decode([]byte(src), "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), key)
		})
	}
}

func TestValidate_RejectsReservedInappID(t *testing.T) {
	src := `
[[notifier]]
id = "inapp"
type = "slack"
webhook_url = "https://example/x"
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved")
}

func TestValidate_RejectsRouteWithUnknownNotifier(t *testing.T) {
	src := `
[[notification_route]]
match = { kind = ["run.failed"] }
notify = ["does-not-exist"]
`
	cfg, err := decode([]byte(src), "")
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
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "match.kind")
}

func TestParseNotifyToken(t *testing.T) {
	cases := []struct {
		in              string
		wantParent      string
		wantOverride    string
		wantHasOverride bool
	}{
		{"slack-ops", "slack-ops", "", false},
		{"inapp", "inapp", "", false},
		{"slack:#ops", "slack", "#ops", true},
		{"slack-ops:#alerts", "slack-ops", "#alerts", true},
		{"tg:-1001234", "tg", "-1001234", true},
		{"  slack  :  #ops  ", "slack", "#ops", true},
		{":#ops", "", "#ops", true},
		{"slack:", "slack", "", true},
	}
	for _, c := range cases {
		gotParent, gotOverride, gotHas := parseNotifyToken(c.in)
		assert.Equal(t, c.wantParent, gotParent, "parent for %q", c.in)
		assert.Equal(t, c.wantOverride, gotOverride, "override for %q", c.in)
		assert.Equal(t, c.wantHasOverride, gotHas, "hasOverride for %q", c.in)
	}
}

func TestInlineToken_SlackChannelOverride(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id          = "slack"
type        = "slack"
webhook_url = "https://hooks.slack.test/T/B/Z"
channel     = "#default"

[tasks.audit]
cron              = "0 4 * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "audit.sh"
notify_on_failure = ["slack:#ops"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	require.Len(t, cfg.Notify.Notifiers, 2, "parent + synthesised override")
	parent := cfg.Notify.Notifiers[0]
	synth := cfg.Notify.Notifiers[1]
	assert.Equal(t, "slack", parent.ID)
	assert.Equal(t, "#default", parent.SlackChannel)
	assert.Equal(t, "slack:#ops", synth.ID)
	assert.Equal(t, "slack", synth.Type)
	assert.Equal(t, "#ops", synth.SlackChannel, "override replaces parent channel")
	assert.Equal(t, parent.WebhookURL, synth.WebhookURL, "creds cloned from parent")

	var taskRoute NotificationRoute
	for _, r := range cfg.Notify.Routes {
		if r.TaskGlob == "audit" {
			taskRoute = r
			break
		}
	}
	assert.Contains(t, taskRoute.NotifierID, "slack:#ops")
}

func TestInlineToken_TelegramChatIDOverride(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id        = "tg"
type      = "telegram"
bot_token = "tok"
chat_id   = "-1001"

[tasks.deploy]
cron              = "0 9 * * 1"
max_concurrent    = 1
on_overlap        = "queue"
run               = "deploy.sh"
notify_on_failure = ["tg:-2002"]
notify_on_success = ["tg:-3003"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	ids := make(map[string]string, len(cfg.Notify.Notifiers))
	for _, n := range cfg.Notify.Notifiers {
		ids[n.ID] = n.ChatID
	}
	assert.Equal(t, "-1001", ids["tg"])
	assert.Equal(t, "-2002", ids["tg:-2002"])
	assert.Equal(t, "-3003", ids["tg:-3003"])
}

func TestInlineToken_DedupAcrossTasks(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id              = "slack"
type            = "slack"
webhook_url     = "https://example/hook"

[tasks.a]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "x"
notify_on_failure = ["slack:#ops"]

[tasks.b]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "y"
notify_on_failure = ["slack:#ops"]

[tasks.c]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "z"
notify_on_failure = ["slack:#ops", "slack:#fyi"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	count := 0
	for _, n := range cfg.Notify.Notifiers {
		if n.ID == "slack:#ops" || n.ID == "slack:#fyi" {
			count++
		}
	}
	assert.Equal(t, 2, count, "each unique token produces one synthetic notifier")
}

func TestInlineToken_InRouteNotifyList(t *testing.T) {
	src := `
[[notifier]]
id              = "slack"
type            = "slack"
webhook_url     = "https://example/hook"

[[notification_route]]
match  = { kind = ["run.failed"], task = "backup-*" }
notify = ["slack:#ops"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	found := false
	for _, n := range cfg.Notify.Notifiers {
		if n.ID == "slack:#ops" {
			found = true
			assert.Equal(t, "#ops", n.SlackChannel)
		}
	}
	assert.True(t, found, "explicit-route token must also synthesise a spec")
}

func TestInlineToken_UnknownParent(t *testing.T) {
	src := `
[tasks.x]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "x"
notify_on_failure = ["slack:#ops"]
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown parent notifier id")
}

func TestInlineToken_InappCannotBeOverridden(t *testing.T) {
	src := `
[tasks.x]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "x"
notify_on_failure = ["inapp:foo"]
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no target to override")
}

func TestInlineToken_EmptyOverride(t *testing.T) {
	src := `
[[notifier]]
id              = "slack"
type            = "slack"
webhook_url     = "https://example/hook"

[tasks.x]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "x"
notify_on_failure = ["slack:"]
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target override is empty")
}

func TestInlineToken_SlackOverrideMustStartWithHashOrAt(t *testing.T) {
	src := `
[[notifier]]
id              = "slack"
type            = "slack"
webhook_url     = "https://example/hook"

[tasks.x]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "x"
notify_on_failure = ["slack:ops"]
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must start with # or @")
}

func TestValidate_RejectsColonInNotifierID(t *testing.T) {
	src := `
[[notifier]]
id              = "slack:foo"
type            = "slack"
webhook_url     = "https://example/hook"
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain")
}

func TestNotifyConfig_EmptyGlobalNotifiersDropsImplicitInappFromSugar(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "ops"
type = "slack"
webhook_url = "https://example/x"

[notify]
global_notifiers = []

[tasks.backup-db]
cron = "0 3 * * *"
max_concurrent = 1
on_overlap = "queue"
run = "backup.sh"
notify_on_failure = ["ops"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	require.Len(t, cfg.Notify.Routes, 1, "no default catch-all when global_notifiers = []")
	assert.NotContains(t, cfg.Notify.Routes[0].NotifierID, "inapp",
		"per-task sugar must not implicitly add inapp once the operator has cleared global_notifiers")
}

func TestNotifyConfig_GlobalNotifiersWithCustomChannel(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "slack-ops"
type = "slack"
webhook_url = "https://example/x"

[notify]
global_notifiers = ["slack-ops"]

[tasks.backup-db]
cron = "0 3 * * *"
max_concurrent = 1
on_overlap = "queue"
run = "backup.sh"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	require.Len(t, cfg.Notify.Routes, 1, "default catch-all uses operator-chosen channels, not inapp")
	r := cfg.Notify.Routes[0]
	assert.Equal(t, []string{"slack-ops"}, r.NotifierID)
	assert.NotContains(t, r.NotifierID, "inapp")
}

func TestValidate_RejectsUnknownAppendNotifier(t *testing.T) {
	src := `
[notify]
global_notifiers = ["does-not-exist"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "global_notifiers")
}

func TestDecode_NotifyDefaultTimeout(t *testing.T) {
	src := schedulerTZHeader + `
[notify]
default_timeout = "5m"
history_keep_for = "720h"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
	assert.NotZero(t, cfg.Notify.DefaultTimeout)
	assert.NotZero(t, cfg.Notify.HistoryKeepFor)
}

func TestDecode_NotifyDefaultTimeout_Invalid(t *testing.T) {
	src := schedulerTZHeader + `
[notify]
default_timeout = "not-a-duration"
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default_timeout")
}

func TestDecode_NotifyHistoryKeepFor_Invalid(t *testing.T) {
	src := schedulerTZHeader + `
[notify]
history_keep_for = "bad-value"
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "history_keep_for")
}

func TestValidate_RejectsTelegramMissingChatID(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "tg"
type = "telegram"
bot_token = "tok"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chat_id")
}

func TestValidate_RejectsTelegramMissingBotToken(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "tg"
type = "telegram"
chat_id = "-1001"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bot_token")
}

func TestValidate_RejectsTelegramMarkdownV2WithoutTemplatePath(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "tg"
type = "telegram"
bot_token = "tok"
chat_id = "-1001"
parse_mode = "MarkdownV2"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse_mode=MarkdownV2")
}

func TestValidate_AcceptsTelegramMarkdownV2WithTemplatePath(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "tg"
type = "telegram"
bot_token = "tok"
chat_id = "-1001"
parse_mode = "MarkdownV2"
template_path = "/etc/runwisp/tg.html"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
}

func TestValidate_SlackChannelBadPrefix(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "s"
type = "slack"
webhook_url = "https://example.com/hook"
channel = "ops"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "# or @")
}

func TestValidate_SlackChannelWithAtPrefix(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "s"
type = "slack"
webhook_url = "https://example.com/hook"
channel = "@user"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
}

func TestValidate_UnknownNotifierType(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "x"
type = "pagerduty"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pagerduty")
}

func TestValidate_RejectsSlackMissingWebhookURL(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "s"
type = "slack"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook_url is required")
}

func TestDesugarServiceNotify(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id = "ops"
type = "slack"
webhook_url = "https://example/x"

[services.svc]
run = "svc-process"
on_overlap = "queue"
instances = 1
notify_on_failure = ["ops"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
	// Service task must still produce a route
	found := false
	for _, r := range cfg.Notify.Routes {
		if r.TaskGlob == "svc" {
			found = true
			break
		}
	}
	assert.True(t, found, "service task with notify_on_failure must produce a route")
}

func TestValidate_RouteWithEmptySeverity(t *testing.T) {
	src := schedulerTZHeader + `
[[notification_route]]
match = { kind = ["run.failed"] }
notify = ["inapp"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
}

func TestValidate_RouteWithBadSeverity(t *testing.T) {
	src := `
[[notification_route]]
match = { kind = ["run.failed"], severity = "unknown-sev" }
notify = ["inapp"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "match.severity")
}

func TestValidate_RouteWithInvalidGlob(t *testing.T) {
	src := `
[[notification_route]]
match = { kind = ["run.failed"], task = "[invalid" }
notify = ["inapp"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid task glob")
}

// --- SMTP notifier validation ---------------------------------------------

func TestValidate_SMTP_HappyPath(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id           = "email-ops"
type         = "smtp"
host         = "smtp.example.com"
port         = 587
from         = "RunWisp <runwisp@example.com>"
to           = ["ops@example.com", "Alerts Team <alerts@example.com>"]
cc           = ["audit@example.com"]
username     = "apikey"
password     = "secret"
tls          = "starttls"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
	require.Len(t, cfg.Notify.Notifiers, 1)
	n := cfg.Notify.Notifiers[0]
	assert.Equal(t, "smtp", n.Type)
	assert.Equal(t, 587, n.Port)
	assert.Equal(t, "smtp.example.com", n.Host)
	assert.Equal(t, []string{"ops@example.com", "Alerts Team <alerts@example.com>"}, n.Recipients)
	assert.Equal(t, []string{"audit@example.com"}, n.CC)
}

func TestValidate_SMTP_AuthlessLocalRelay(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "email-local"
type = "smtp"
host = "127.0.0.1"
port = 25
from = "RunWisp <runwisp@example.com>"
to   = ["ops@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg), "auth-less local relay must be accepted")
}

func TestValidate_SMTP_MissingHost(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "email-ops"
type = "smtp"
from = "runwisp@example.com"
to   = ["ops@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host is required")
}

func TestValidate_SMTP_MissingFrom(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "email-ops"
type = "smtp"
host = "smtp.example.com"
to   = ["ops@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "from is required")
}

func TestValidate_SMTP_InvalidFrom(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "email-ops"
type = "smtp"
host = "smtp.example.com"
from = "not-an-email"
to   = ["ops@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "from")
}

func TestValidate_SMTP_EmptyTo(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "email-ops"
type = "smtp"
host = "smtp.example.com"
from = "runwisp@example.com"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "to is required")
}

func TestValidate_SMTP_TLSNoneRejectedWithCreds(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id       = "email-ops"
type     = "smtp"
host     = "smtp.example.com"
from     = "runwisp@example.com"
to       = ["ops@example.com"]
username = "u"
password = "p"
tls      = "none"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PLAIN auth")
}

func TestValidate_SMTP_BadTLSValue(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "email-ops"
type = "smtp"
host = "smtp.example.com"
from = "runwisp@example.com"
to   = ["ops@example.com"]
tls  = "wat"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls")
}

func TestValidate_SMTP_BadRecipientAddress(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "email-ops"
type = "smtp"
host = "smtp.example.com"
from = "runwisp@example.com"
to   = ["not-an-email"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid")
}

func TestValidate_SMTP_UsernameWithoutPasswordRejected(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id       = "email-ops"
type     = "smtp"
host     = "smtp.example.com"
from     = "runwisp@example.com"
to       = ["ops@example.com"]
username = "u"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username and password must be set together")
}

func TestInlineToken_SMTPRecipientOverride(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id           = "email-ops"
type         = "smtp"
host         = "smtp.example.com"
from         = "RunWisp <runwisp@example.com>"
to           = ["ops@example.com"]
username     = "apikey"
password     = "secret"

[tasks.backup-db]
cron              = "0 3 * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "backup.sh"
notify_on_failure = ["email-ops:alerts@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	require.Len(t, cfg.Notify.Notifiers, 2, "parent + synthesised override")
	var synth NotifierSpec
	for _, n := range cfg.Notify.Notifiers {
		if n.ID == "email-ops:alerts@example.com" {
			synth = n
		}
	}
	require.Equal(t, "email-ops:alerts@example.com", synth.ID)
	assert.Equal(t, []string{"alerts@example.com"}, synth.Recipients)
	assert.Empty(t, synth.CC, "override clears CC from parent")
}

func TestInlineToken_SMTPRejectsBadEmail(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id           = "email-ops"
type         = "smtp"
host         = "smtp.example.com"
from         = "runwisp@example.com"
to           = ["ops@example.com"]
username     = "u"
password     = "p"

[tasks.x]
cron              = "* * * * *"
max_concurrent    = 1
on_overlap        = "queue"
run               = "x"
notify_on_failure = ["email-ops:bad@@@"]
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid email")
}

func TestValidate_WebhookHappyPath(t *testing.T) {
	src := `
[[notifier]]
id   = "my-hook"
type = "webhook"
url  = "https://example.com/hook"

[notifier.headers]
Authorization = "Bearer tok"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
	require.Len(t, cfg.Notify.Notifiers, 1)
	assert.Equal(t, "https://example.com/hook", cfg.Notify.Notifiers[0].URL)
	assert.Equal(t, "Bearer tok", cfg.Notify.Notifiers[0].Headers["Authorization"])
}

func TestValidate_WebhookMissingURL(t *testing.T) {
	src := `
[[notifier]]
id   = "my-hook"
type = "webhook"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url is required")
}

func TestValidate_WebhookInvalidScheme(t *testing.T) {
	src := `
[[notifier]]
id   = "my-hook"
type = "webhook"
url  = "ftp://example.com/hook"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http or https")
}

func TestValidate_WebhookEmptyHeaderKey(t *testing.T) {
	spec := &NotifierSpec{
		ID:      "my-hook",
		Type:    "webhook",
		URL:     "https://example.com/hook",
		Headers: map[string]string{"": "val"},
	}
	err := validateWebhookNotifier(spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "header key must not be empty")
}

func TestInlineToken_WebhookRejectsOverride(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "my-hook"
type = "webhook"
url  = "https://example.com/hook"

[tasks.foo]
cron              = "* * * * *"
run               = "true"
notify_on_failure = ["my-hook:override"]
`
	_, err := decode([]byte(src), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "do not support inline target overrides")
}

func TestValidate_RouteEmptyNotifyList(t *testing.T) {
	src := `
[[notification_route]]
match = { kind = ["run.failed"] }
notify = []
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notify list is empty")
}
