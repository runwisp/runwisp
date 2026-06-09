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

func TestKindRunMissed_Title(t *testing.T) {
	ev := &Event{TaskName: "nightly"}
	assert.Equal(t, "nightly missed", KindRunMissed.Title(ev))
}

func TestKindNotifyDeliveryFailed_Title_WithChannel(t *testing.T) {
	ev := &Event{
		Kind:  KindNotifyDeliveryFailed,
		Extra: map[string]any{"channel": "slack-ops"},
	}
	title := KindNotifyDeliveryFailed.Title(ev)
	assert.Equal(t, "Delivery to slack-ops failed", title)
}

func TestKindNotifyDeliveryFailed_Title_WrongChannelType(t *testing.T) {
	ev := &Event{
		Kind:  KindNotifyDeliveryFailed,
		Extra: map[string]any{"channel": 42},
	}
	title := KindNotifyDeliveryFailed.Title(ev)
	assert.Equal(t, "Notification delivery failed", title)
}

func TestKindNotifyDeliveryFailed_Title_NoChannelKey(t *testing.T) {
	ev := &Event{
		Kind:  KindNotifyDeliveryFailed,
		Extra: map[string]any{},
	}
	title := KindNotifyDeliveryFailed.Title(ev)
	assert.Equal(t, "Notification delivery failed", title)
}
