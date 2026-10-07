// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package textutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShellQuoteNeutralisesMetacharacters(t *testing.T) {
	// A value crafted to break out of the command must survive as one inert
	// literal — the core trust-model guarantee for operator-supplied values.
	assert.Equal(t, `''\''; rm -rf / #'`, ShellQuote(`'; rm -rf / #`))
}
