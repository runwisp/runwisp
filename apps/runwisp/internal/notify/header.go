// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"fmt"
	"strings"

	"github.com/cenkalti/backoff/v4"
)

// RejectHeaderCRLF returns a permanent error when value contains a CR or LF.
// A newline ends a header and starts another, so an unchecked value could add
// a Bcc or turn the rest into a body. Defense in depth: addresses come from
// TOML and subjects from a rendered template. Empty input is allowed (required
// fields are checked by their channel). The error is permanent because retrying
// cannot remove a newline from a configured value.
func RejectHeaderCRLF(field, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return backoff.Permanent(fmt.Errorf("%s contains CR or LF, which is not allowed in a mail header", field))
	}
	return nil
}

// RejectMailHeaderCRLF applies RejectHeaderCRLF to every header-bound value of
// a mail channel (kind is "smtp" or "sendmail"). The body is exempt: a newline
// there is just a newline.
func RejectMailHeaderCRLF(kind, subject, from, replyTo string, recipients ...[]string) error {
	for _, f := range [][2]string{{"subject", subject}, {"from", from}, {"reply-to", replyTo}} {
		if err := RejectHeaderCRLF(kind+" "+f[0], f[1]); err != nil {
			return err
		}
	}
	for _, group := range recipients {
		for _, addr := range group {
			if err := RejectHeaderCRLF(kind+" recipient", addr); err != nil {
				return err
			}
		}
	}
	return nil
}
