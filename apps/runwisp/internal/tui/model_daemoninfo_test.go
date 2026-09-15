// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"errors"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

func TestHandleDaemonInfo_UpdatesConfigStale(t *testing.T) {
	m := newTestModel(nil)
	updated, cmd := m.handleDaemonInfo(uikit.DaemonInfoMsg{
		Info: &model.DaemonInfo{ConfigStale: true, ServiceManaged: true},
	})
	if cmd != nil {
		t.Fatal("expected nil cmd")
	}
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if !got.info.ConfigStale {
		t.Fatal("expected ConfigStale to be set from daemon info")
	}
	if !got.info.ServiceManaged {
		t.Fatal("expected ServiceManaged to be set from daemon info")
	}
}

func TestHandleDaemonInfo_UpdatesSidebarUpdateIndicator(t *testing.T) {
	m := newTestModel(nil)
	updated, _ := m.handleDaemonInfo(uikit.DaemonInfoMsg{
		Info: &model.DaemonInfo{UpdateAvailable: true, LatestVersion: "v9.9.9"},
	})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if got.sidebar.LatestVersion() != "v9.9.9" {
		t.Fatalf("LatestVersion = %q, want v9.9.9", got.sidebar.LatestVersion())
	}
	got.sidebar.FocusVersion()
	if !got.sidebar.VersionFocused() {
		t.Fatal("expected the sidebar to have picked up UpdateAvailable from daemon info")
	}
}

func TestHandleDaemonInfo_ErrorKeepsLastKnownState(t *testing.T) {
	m := newTestModel(nil)
	m.info.ConfigStale = true
	updated, _ := m.handleDaemonInfo(uikit.DaemonInfoMsg{Err: errors.New("connection refused")})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if !got.info.ConfigStale {
		t.Fatal("expected error to leave ConfigStale untouched")
	}
}
