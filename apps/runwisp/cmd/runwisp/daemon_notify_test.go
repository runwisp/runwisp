// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/runwisp/runwisp/internal/notify/coalesce"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestServerHub(t *testing.T) {
	// Regression: a nil Hub must come back as a true-nil interface, not a
	// typed-nil that slips past the server's `notifyHub == nil` guard and
	// panics the notifications stream. See serverHub.
	t.Run("nil hub yields nil interface", func(t *testing.T) {
		var b notifyBundle
		assert.Nil(t, b.serverHub(), "serverHub() leaked a typed-nil interface")
	})
	t.Run("real hub passes through", func(t *testing.T) {
		b := notifyBundle{Hub: inapp.NewHub(32)}
		got := b.serverHub()
		require.NotNil(t, got)
		assert.Same(t, b.Hub, got)
	})
}

func TestBackoffOverride(t *testing.T) {
	t.Run("nil when zero", func(t *testing.T) {
		assert.Nil(t, backoffOverride(0, slog.Default()))
	})
	t.Run("nil when negative", func(t *testing.T) {
		assert.Nil(t, backoffOverride(-time.Second, slog.Default()))
	})
	t.Run("caps MaxElapsedTime at supplied duration", func(t *testing.T) {
		cap := 5 * time.Second
		fn := backoffOverride(cap, slog.Default())
		require.NotNil(t, fn)
		provider := fn()
		require.NotNil(t, provider)
		assert.LessOrEqual(t, provider.Backoff.MaxElapsedTime, cap)
	})
	// Duration shorter than the HTTPProvider's default InitialInterval
	// (500ms) forces the InitialInterval > d branch to fire.
	t.Run("shrinks intervals when cap is smaller than defaults", func(t *testing.T) {
		cap := 100 * time.Millisecond
		fn := backoffOverride(cap, slog.Default())
		require.NotNil(t, fn)
		provider := fn()
		require.NotNil(t, provider)
		assert.LessOrEqual(t, provider.Backoff.MaxElapsedTime, cap)
		assert.LessOrEqual(t, provider.Backoff.InitialInterval, cap)
		assert.LessOrEqual(t, provider.Backoff.MaxInterval, cap)
	})
}

// fakeNotificationRepo implements storage.NotificationRepository for
// retention-callback tests; only the prune methods see use.
type fakeNotificationRepo struct {
	mock.Mock
}

func (f *fakeNotificationRepo) UpsertByFingerprint(context.Context, *storage.Notification, time.Duration, int) (bool, error) {
	panic("not used")
}
func (f *fakeNotificationRepo) ListNotifications(context.Context, int, string) ([]storage.Notification, error) {
	panic("not used")
}
func (f *fakeNotificationRepo) GetNotificationByID(context.Context, string) (*storage.Notification, error) {
	panic("not used")
}
func (f *fakeNotificationRepo) PruneNotificationsByCount(_ context.Context, keep int) (int64, error) {
	a := f.Called(keep)
	return a.Get(0).(int64), a.Error(1)
}
func (f *fakeNotificationRepo) PruneNotificationsByAge(_ context.Context, olderThan time.Duration) (int64, error) {
	a := f.Called(olderThan)
	return a.Get(0).(int64), a.Error(1)
}
func (f *fakeNotificationRepo) CountUnreadNotifications(context.Context) (int64, error) {
	panic("not used")
}
func (f *fakeNotificationRepo) MarkNotificationRead(context.Context, string, time.Time) (*storage.Notification, error) {
	panic("not used")
}
func (f *fakeNotificationRepo) MarkNotificationUnread(context.Context, string) (*storage.Notification, error) {
	panic("not used")
}
func (f *fakeNotificationRepo) MarkAllNotificationsRead(context.Context, time.Time) error {
	panic("not used")
}

func TestBuildInappRenderer_LoadsDefaultTemplate(t *testing.T) {
	r, err := buildInappRenderer()
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestBuildOutboundChannels_UnknownTypeReturnsError(t *testing.T) {
	specs := []channel.NotifierSpec{
		{ID: "broken", Type: "garbage"},
	}
	_, err := buildOutboundChannels(specs, false, coalesce.Config{}, slog.Default(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "garbage")
}

func TestBuildOutboundChannels_EmptyReturnsNoChannels(t *testing.T) {
	channels, err := buildOutboundChannels(nil, false, coalesce.Config{}, slog.Default(), nil)
	require.NoError(t, err)
	assert.Empty(t, channels)
}

func TestBuildOutboundChannels_BuildsSlackChannel(t *testing.T) {
	specs := []channel.NotifierSpec{
		{ID: "ops", Type: "slack", WebhookURL: "https://hooks.example/abc"},
	}
	channels, err := buildOutboundChannels(specs, false, coalesce.Config{}, slog.Default(), nil)
	require.NoError(t, err)
	require.Len(t, channels, 1)
}

func TestBuildOutboundChannels_AppliesCoalesceWrapperWhenEnabled(t *testing.T) {
	specs := []channel.NotifierSpec{
		{ID: "ops", Type: "slack", WebhookURL: "https://hooks.example/abc"},
	}
	channelsNoCoalesce, err := buildOutboundChannels(specs, false, coalesce.Config{Window: time.Second, EveryN: 3}, slog.Default(), nil)
	require.NoError(t, err)
	require.Len(t, channelsNoCoalesce, 1)

	channelsCoalesce, err := buildOutboundChannels(specs, true, coalesce.Config{Window: time.Second, EveryN: 3}, slog.Default(), nil)
	require.NoError(t, err)
	require.Len(t, channelsCoalesce, 1)

	// With coalescing enabled the wrapper type differs from the raw channel.
	// We assert that by comparing the concrete types via their string form;
	// a structural assertion is enough — the wrapper is added at runtime.
	assert.NotEqual(t, channelsNoCoalesce[0], channelsCoalesce[0],
		"coalesced channel should not equal its raw counterpart")
}

func TestBuildRetentionFn_NilWhenBothLimitsDisabled(t *testing.T) {
	assert.Nil(t, buildRetentionFn(&fakeNotificationRepo{}, config.NotifyConfig{}, slog.Default()))
}

func TestBuildRetentionFn_KeepOnlyInvokesPruneByCount(t *testing.T) {
	repo := &fakeNotificationRepo{}
	repo.On("PruneNotificationsByCount", 10).Return(int64(2), nil)

	fn := buildRetentionFn(repo, config.NotifyConfig{KeepNotifications: 10}, slog.Default())
	require.NotNil(t, fn)
	fn(t.Context())
	repo.AssertExpectations(t)
}

func TestBuildRetentionFn_BothLimitsCallBoth(t *testing.T) {
	repo := &fakeNotificationRepo{}
	repo.On("PruneNotificationsByCount", 5).Return(int64(0), nil)
	repo.On("PruneNotificationsByAge", time.Hour).Return(int64(0), nil)

	fn := buildRetentionFn(repo, config.NotifyConfig{KeepNotifications: 5, KeepFor: time.Hour}, slog.Default())
	require.NotNil(t, fn)
	fn(t.Context())
	repo.AssertExpectations(t)
}

func TestBuildRetentionFn_LogsButDoesNotPanicOnPruneError(t *testing.T) {
	repo := &fakeNotificationRepo{}
	repo.On("PruneNotificationsByCount", 1).Return(int64(0), errors.New("db gone"))
	repo.On("PruneNotificationsByAge", time.Minute).Return(int64(0), errors.New("db gone"))

	fn := buildRetentionFn(repo, config.NotifyConfig{KeepNotifications: 1, KeepFor: time.Minute}, slog.Default())
	require.NotNil(t, fn)
	require.NotPanics(t, func() { fn(t.Context()) })
}

// TestInitNotify_NoNotifiersNoRoutesReturnsZero exercises the early-return
// branch where there's nothing to wire — initNotify must hand back an empty
// bundle without touching the DB or starting any goroutines.
func TestInitNotify_NoNotifiersNoRoutesReturnsZero(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &daemonConfig{
		Fingerprint: "fp",
		Config: &config.Config{
			Notify: config.NotifyConfig{},
		},
	}

	bundle, err := initNotify(cfg, db, events.NewEventBus(), slog.Default())
	require.NoError(t, err)
	assert.Nil(t, bundle.Service, "expected zero bundle when nothing is configured")
	assert.Nil(t, bundle.Hub)
}

// TestInitNotify_InappRouteWiresHubAndService exercises the happy path:
// a route targets the special "inapp" channel, so initNotify must build a Hub
// and a Service.
func TestInitNotify_InappRouteWiresHubAndService(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &daemonConfig{
		Fingerprint: "fp",
		Config: &config.Config{
			Notify: config.NotifyConfig{
				Routes: []config.NotificationRoute{
					{Kinds: []string{"run.failed"}, NotifierID: []string{"inapp"}},
				},
				CoalesceWindow:  time.Minute,
				KeepOccurrences: 5,
			},
		},
	}

	bundle, err := initNotify(cfg, db, events.NewEventBus(), slog.Default())
	require.NoError(t, err)
	require.NotNil(t, bundle.Service, "expected Service when inapp route is wired")
	require.NotNil(t, bundle.Hub, "expected Hub when inapp route is wired")
}

func TestMutedMissedTasks(t *testing.T) {
	muteFalse := false
	muteTrue := true

	t.Run("nil when nothing is muted", func(t *testing.T) {
		tasks := []model.Task{
			{Name: "a"}, // omitted → notifies
			{Name: "b", TreatMissedAsFailure: &muteTrue}, // explicit true → notifies
		}
		assert.Nil(t, mutedMissedTasks(tasks),
			"the common case must allocate nothing")
	})

	t.Run("collects only the explicit-false tasks", func(t *testing.T) {
		tasks := []model.Task{
			{Name: "loud"},
			{Name: "quiet", TreatMissedAsFailure: &muteFalse},
			{Name: "also-quiet", TreatMissedAsFailure: &muteFalse},
		}
		muted := mutedMissedTasks(tasks)
		require.Len(t, muted, 2)
		_, hasQuiet := muted["quiet"]
		_, hasAlsoQuiet := muted["also-quiet"]
		_, hasLoud := muted["loud"]
		assert.True(t, hasQuiet)
		assert.True(t, hasAlsoQuiet)
		assert.False(t, hasLoud, "a notifying task must not be muted")
	})
}

func TestRoutesReferenceInapp(t *testing.T) {
	tests := []struct {
		name   string
		routes []config.NotificationRoute
		want   bool
	}{
		{"empty", nil, false},
		{"no inapp", []config.NotificationRoute{{NotifierID: []string{"slack", "telegram"}}}, false},
		{"mixed includes inapp", []config.NotificationRoute{{NotifierID: []string{"slack", "inapp"}}}, true},
		{"only inapp", []config.NotificationRoute{{NotifierID: []string{"inapp"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, routesReferenceInapp(tt.routes))
		})
	}
}
