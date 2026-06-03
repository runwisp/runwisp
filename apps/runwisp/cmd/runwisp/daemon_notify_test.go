// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/stretchr/testify/assert"
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
		b := notifyBundle{Hub: inapp.NewHub(32, 50)}
		got := b.serverHub()
		require.NotNil(t, got)
		assert.Same(t, b.Hub, got)
	})
}

// TestInitNotify_UnresolvableSecretIsFatal locks the prime-directive contract
// that a configured notifier whose ${VAR} secret can't resolve fails daemon
// startup loudly rather than booting with notifications silently disabled.
// Resolution happens before the db/bus are touched, so nil dependencies are
// fine here — the call must error out at secret resolution.
func TestInitNotify_UnresolvableSecretIsFatal(t *testing.T) {
	require.NoError(t, os.Unsetenv("RUNWISP_TEST_INITNOTIFY_MISSING"))
	cfg := &daemonConfig{
		Config: &config.Config{
			Notify: config.NotifyConfig{
				Notifiers: []config.NotifierSpec{{
					ID:         "ops",
					Type:       "slack",
					WebhookURL: "${RUNWISP_TEST_INITNOTIFY_MISSING}",
				}},
			},
		},
	}
	_, err := initNotify(cfg, nil, nil, slog.Default())
	require.Error(t, err, "an unresolvable ${VAR} secret must fail startup, not warn")
	assert.Contains(t, err.Error(), "RUNWISP_TEST_INITNOTIFY_MISSING",
		"the error must name the offending variable so the operator knows what to fix")
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
