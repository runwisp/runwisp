// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- sendmail notifier validation ------------------------------------------
//
// The point of the type is that a box migrating off cron already has a working
// MTA, so the config is addressing and nothing else. These tests pin that:
// no host, no port, no credentials, and it still validates.

func TestValidate_Sendmail_NeedsNothingButAddresses(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "mta"
type = "sendmail"
from = "RunWisp <runwisp@example.com>"
to   = ["ops@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
	require.Len(t, cfg.Notify.Notifiers, 1)
	n := cfg.Notify.Notifiers[0]
	assert.Equal(t, "sendmail", n.Type)
	assert.Equal(t, []string{"ops@example.com"}, n.Recipients)
	assert.Empty(t, n.SendmailPath, "an unset path means find the system binary")
}

func TestValidate_Sendmail_AcceptsAnExplicitBinary(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id            = "mta"
type          = "sendmail"
from          = "runwisp@example.com"
to            = ["ops@example.com"]
cc            = ["audit@example.com"]
bcc           = ["archive@example.com"]
reply_to      = "noreply@example.com"
sendmail_path = "/usr/local/bin/msmtp"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))
	n := cfg.Notify.Notifiers[0]
	assert.Equal(t, "/usr/local/bin/msmtp", n.SendmailPath)
	assert.Equal(t, []string{"audit@example.com"}, n.CC)
	assert.Equal(t, []string{"archive@example.com"}, n.BCC)
	assert.Equal(t, "noreply@example.com", n.ReplyTo)
}

// TestValidate_Sendmail_RejectsARelativeBinary: the daemon may run from
// anywhere, and resolving the mailer through $PATH-at-exec-time is how you get
// a different binary than the one you configured.
func TestValidate_Sendmail_RejectsARelativeBinary(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id            = "mta"
type          = "sendmail"
from          = "runwisp@example.com"
to            = ["ops@example.com"]
sendmail_path = "bin/sendmail"
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}

func TestValidate_Sendmail_RequiresFromAndTo(t *testing.T) {
	cases := map[string]string{
		"from is required for type=sendmail": `
[[notifier]]
id   = "mta"
type = "sendmail"
to   = ["ops@example.com"]
`,
		"to is required for type=sendmail": `
[[notifier]]
id   = "mta"
type = "sendmail"
from = "runwisp@example.com"
`,
	}
	for want, body := range cases {
		t.Run(want, func(t *testing.T) {
			cfg, err := decode([]byte(schedulerTZHeader+body), "")
			require.NoError(t, err)
			err = Validate(cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
}

func TestValidate_Sendmail_RejectsAMalformedAddress(t *testing.T) {
	src := schedulerTZHeader + `
[[notifier]]
id   = "mta"
type = "sendmail"
from = "runwisp@example.com"
to   = ["not an address"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	err = Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid")
}

// TestValidate_Sendmail_SupportsInlineRecipientOverride: `notify_on_failure =
// ["mta:oncall@example.com"]` is the sugar that makes one MTA notifier serve
// every task, and it is the closest thing to a per-crontab MAILTO.
func TestValidate_Sendmail_SupportsInlineRecipientOverride(t *testing.T) {
	src := schedulerTZHeader + `
[tasks.backup]
cron              = "@daily"
run               = "/bin/backup"
on_overlap        = "queue"
notify_on_failure = ["mta:oncall@example.com"]

[[notifier]]
id   = "mta"
type = "sendmail"
from = "runwisp@example.com"
to   = ["ops@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	var synthetic *NotifierSpec
	for i := range cfg.Notify.Notifiers {
		if cfg.Notify.Notifiers[i].ID == "mta:oncall@example.com" {
			synthetic = &cfg.Notify.Notifiers[i]
		}
	}
	require.NotNil(t, synthetic, "expected a synthetic notifier for the inline override")
	assert.Equal(t, "sendmail", synthetic.Type)
	assert.Equal(t, []string{"oncall@example.com"}, synthetic.Recipients)
	assert.Equal(t, "runwisp@example.com", synthetic.From, "the parent's identity carries over")
}

func TestValidate_Sendmail_RejectsAMalformedInlineOverride(t *testing.T) {
	src := schedulerTZHeader + `
[tasks.backup]
cron              = "@daily"
run               = "/bin/backup"
on_overlap        = "queue"
notify_on_failure = ["mta:not an address"]

[[notifier]]
id   = "mta"
type = "sendmail"
from = "runwisp@example.com"
to   = ["ops@example.com"]
`
	cfg, err := decode([]byte(src), "")
	require.Error(t, err, "a malformed override must not load")
	_ = cfg
}
