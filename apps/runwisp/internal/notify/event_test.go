// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify/kinds"
	"github.com/stretchr/testify/assert"
)

func TestFingerprintKey_Nil(t *testing.T) {
	assert.Equal(t, "", FingerprintKey(nil), "a nil event has the empty fingerprint")
}

func TestFingerprintKey_DifferentTaskNamesDiffer(t *testing.T) {
	ev1 := &Event{Kind: KindRunFailed, TaskName: "a"}
	ev2 := &Event{Kind: KindRunFailed, TaskName: "b"}
	assert.NotEqual(t, FingerprintKey(ev1), FingerprintKey(ev2),
		"different task names must yield different fingerprint keys")
}

func TestFingerprintKey_WithEndReason(t *testing.T) {
	r := model.ReasonFailed
	ev := &Event{
		Kind:     KindRunFailed,
		TaskName: "t1",
		Run:      &model.Run{EndReason: &r},
	}
	assert.Contains(t, FingerprintKey(ev), "failed")
}

func TestFingerprintKey_WithDeliveryFailed(t *testing.T) {
	ev := &Event{
		Kind:     KindNotifyDeliveryFailed,
		TaskName: "t1",
		Extra:    map[string]any{"channel": "slack", "original_kind": "run.failed"},
	}
	key := FingerprintKey(ev)
	assert.Contains(t, key, "slack")
	assert.Contains(t, key, "run.failed", "delivery-failure events fold by channel + original kind")
}

func TestKind_TitleCoversAllKinds(t *testing.T) {
	ev := &Event{TaskName: "test-task"}
	for _, k := range kinds.AllKindStrings {
		title := Kind(k).Title(ev)
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
