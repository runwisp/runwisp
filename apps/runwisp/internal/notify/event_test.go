// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKind_TitleCoversAllKinds(t *testing.T) {
	ev := &Event{TaskName: "test-task"}
	for _, k := range AllKinds {
		title := k.Title(ev)
		assert.NotEmpty(t, title, "Kind.Title() must return a non-empty string for %s", k)
	}
}

func TestKind_FingerprintTokenCoversAllKinds(t *testing.T) {
	for _, k := range AllKinds {
		token := k.FingerprintToken()
		assert.NotEmpty(t, token, "Kind.FingerprintToken() must return a non-empty string for %s", k)
	}
}
