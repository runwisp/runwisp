// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ntfy / gotify / pushover notifier validation ---------------------------

// decodeAndValidate runs both stages a notifier block goes through: decode
// (field ownership, inline overrides) and Validate (required keys, shapes).
func decodeAndValidate(t *testing.T, body string) (*Config, error) {
	t.Helper()
	cfg, err := decode([]byte(schedulerTZHeader+body), "")
	if err != nil {
		return nil, err
	}
	return cfg, Validate(cfg)
}

func TestValidate_PushNotifiers_MinimalBlocks(t *testing.T) {
	cfg, err := decodeAndValidate(t, `
[notifiers.phone]
type  = "ntfy"
topic = "runwisp-alerts"

[notifiers.gotify]
type  = "gotify"
url   = "https://gotify.example.com"
token = "Aapp"

[notifiers.po]
type  = "pushover"
token = "app"
user  = "user"
`)
	require.NoError(t, err)
	byID := map[string]NotifierSpec{}
	for _, n := range cfg.Notify.Notifiers {
		byID[n.ID] = n
	}
	assert.Equal(t, "runwisp-alerts", byID["phone"].Topic)
	assert.Empty(t, byID["phone"].URL, "an unset ntfy url means the public server")
	assert.Equal(t, "https://gotify.example.com", byID["gotify"].URL)
	assert.Equal(t, "Aapp", byID["gotify"].Token)
	assert.Equal(t, "user", byID["po"].User)
}

func TestValidate_PushNotifiers_Rejects(t *testing.T) {
	cases := map[string]string{
		"topic is required for type=ntfy": `
[notifiers.phone]
type = "ntfy"
`,
		`ntfy topic "my/topic" must be 1-64`: `
[notifiers.phone]
type  = "ntfy"
topic = "my/topic"
`,
		"url must use http or https": `
[notifiers.phone]
type  = "ntfy"
topic = "alerts"
url   = "ntfy.example.com"
`,
		"user is not valid for type=ntfy": `
[notifiers.phone]
type  = "ntfy"
topic = "alerts"
user  = "someone"
`,
		"url is required for type=gotify": `
[notifiers.gotify]
type  = "gotify"
token = "t"
`,
		"token is required for type=gotify": `
[notifiers.gotify]
type = "gotify"
url  = "https://gotify.example.com"
`,
		"token is required for type=pushover": `
[notifiers.po]
type = "pushover"
user = "u"
`,
		"user is required for type=pushover": `
[notifiers.po]
type  = "pushover"
token = "t"
`,
		"topic is not valid for type=pushover": `
[notifiers.po]
type  = "pushover"
token = "t"
user  = "u"
topic = "alerts"
`,
		"token is not valid for type=slack": `
[notifiers.ops]
type        = "slack"
webhook_url = "https://hooks.slack.com/x"
token       = "t"
`,
	}
	for want, body := range cases {
		t.Run(want, func(t *testing.T) {
			_, err := decodeAndValidate(t, body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
}

func TestValidate_PushNotifiers_InlineOverrides(t *testing.T) {
	cfg, err := decodeAndValidate(t, `
[notifiers.phone]
type  = "ntfy"
topic = "alerts"

[notifiers.po]
type  = "pushover"
token = "app"
user  = "user"

[tasks.backup]
cron   = "@daily"
run    = "/bin/backup"
on_overlap = "queue"
notify = ["phone:backups", "po:group-key"]
`)
	require.NoError(t, err)
	byID := map[string]NotifierSpec{}
	for _, n := range cfg.Notify.Notifiers {
		byID[n.ID] = n
	}
	assert.Equal(t, "backups", byID["phone:backups"].Topic)
	assert.Equal(t, "group-key", byID["po:group-key"].User)
	assert.Equal(t, "app", byID["po:group-key"].Token, "the override keeps the parent's credentials")
}

func TestValidate_PushNotifiers_InlineOverrideRejects(t *testing.T) {
	cases := map[string]string{
		"gotify notifiers do not support inline target overrides": `
[notifiers.gotify]
type  = "gotify"
url   = "https://gotify.example.com"
token = "t"

[tasks.backup]
cron   = "@daily"
run    = "/bin/backup"
on_overlap = "queue"
notify = ["gotify:other"]
`,
		`ntfy topic "bad topic" must be 1-64`: `
[notifiers.phone]
type  = "ntfy"
topic = "alerts"

[tasks.backup]
cron   = "@daily"
run    = "/bin/backup"
on_overlap = "queue"
notify = ["phone:bad topic"]
`,
	}
	for want, body := range cases {
		t.Run(want, func(t *testing.T) {
			_, err := decodeAndValidate(t, body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
}
