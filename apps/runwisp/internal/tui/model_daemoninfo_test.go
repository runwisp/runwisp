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
	m.handleDaemonInfo(uikit.DaemonInfoMsg{
		Info: &model.DaemonInfo{ConfigStale: true, ServiceManaged: true},
	})
	if !m.info.ConfigStale {
		t.Fatal("expected ConfigStale to be set from daemon info")
	}
	if !m.info.ServiceManaged {
		t.Fatal("expected ServiceManaged to be set from daemon info")
	}
}

func TestHandleDaemonInfo_UpdatesSidebarUpdateIndicator(t *testing.T) {
	m := newTestModel(nil)
	m.handleDaemonInfo(uikit.DaemonInfoMsg{
		Info: &model.DaemonInfo{UpdateAvailable: true, LatestVersion: "v9.9.9"},
	})
	if m.sidebar.LatestVersion() != "v9.9.9" {
		t.Fatalf("LatestVersion = %q, want v9.9.9", m.sidebar.LatestVersion())
	}
	m.sidebar.FocusVersion()
	if !m.sidebar.VersionFocused() {
		t.Fatal("expected the sidebar to have picked up UpdateAvailable from daemon info")
	}
}

func TestHandleDaemonInfo_ErrorKeepsLastKnownState(t *testing.T) {
	m := newTestModel(nil)
	m.info.ConfigStale = true
	m.handleDaemonInfo(uikit.DaemonInfoMsg{Err: errors.New("connection refused")})
	if !m.info.ConfigStale {
		t.Fatal("expected error to leave ConfigStale untouched")
	}
}

// A reload that changes [daemon] timezone re-bases the TUI's clock at once.
func TestHandleReloadResult_AdoptsNewTimezone(t *testing.T) {
	m := newTestModel(nil)
	updated, _ := m.handleReloadResult(uikit.ReloadResultMsg{
		Result: &model.ReloadResult{},
		Info:   &model.DaemonInfo{ResolvedTimezone: "Asia/Tokyo", TimezoneSource: "config"},
	})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if got.info.Timezone != "Asia/Tokyo" || got.info.TimezoneSource != "config" {
		t.Fatalf("timezone not adopted: %q (%q)", got.info.Timezone, got.info.TimezoneSource)
	}
	if got.loc.String() != "Asia/Tokyo" {
		t.Fatalf("location = %v, want Asia/Tokyo", got.loc)
	}
}
