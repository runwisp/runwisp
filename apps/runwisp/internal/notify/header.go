// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"fmt"
	"strings"

	"github.com/cenkalti/backoff/v4"
)

// RejectHeaderCRLF returns a permanent error when value contains a CR or LF.
//
// A newline inside a header value ends that header and starts another, so a
// value carrying one can append headers of its own — a Bcc that redirects the
// mail, or a blank line that turns the rest into a body the operator never
// wrote. Every mail-shaped channel needs the same guard, which is why it lives
// here rather than in one of them.
//
// It is defense in depth, not the primary control: addresses come from TOML,
// which is trusted, and subjects come from a rendered template. It exists to
// catch the code path added later that lets untrusted text reach a header.
//
// Empty input is allowed — "this field is required" is a different check, made
// by the channel that requires it. The error is permanent because no amount of
// retrying will remove a newline from a configured value.
func RejectHeaderCRLF(field, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return backoff.Permanent(fmt.Errorf("%s contains CR or LF, which is not allowed in a mail header", field))
	}
	return nil
}
