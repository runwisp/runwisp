// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/home"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
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

// A poll that brings the first config warning adds a header row; the layout
// must re-measure or the view overflows the screen and clicks land a row off.
func TestHandleDaemonInfo_NewWarningRemeasuresHeader(t *testing.T) {
	m := newTestModel(nil)
	m.width, m.height = 120, 40
	m.updateLayout()
	before := m.layout.homeH
	m.handleDaemonInfo(uikit.DaemonInfoMsg{
		Info: &model.DaemonInfo{ConfigWarnings: []string{"line 3: bad schedule"}},
	})
	if m.layout.homeH != before+1 {
		t.Fatalf("homeH = %d, want %d", m.layout.homeH, before+1)
	}
}

func TestWarningsChipClickAndKeyOpenDialog(t *testing.T) {
	m := newTestModel(nil)
	m.width, m.height = 120, 40
	m.info.ConfigWarnings = []string{"line 3: bad schedule"}
	m.updateLayout()

	newM, _ := m.handleHomePageClick(uikit.SidebarWidth+2, home.NoticesY)
	if clicked := newM.(Model); !clicked.dialogs.Has(dlgWarnings) {
		t.Fatal("clicking the warnings chip should open the warnings dialog")
	}

	got, _, handled := handleKeyW(m, tea.KeyPressMsg{Code: 'w', Text: "w"})
	if !handled || !got.dialogs.Has(dlgWarnings) {
		t.Fatal("w should open the warnings dialog")
	}
	if view := got.dialogs.RenderOverlays("", 120, 40); !strings.Contains(view, "line 3: bad schedule") {
		t.Fatal("dialog should list the warning text")
	}

	m.info.ConfigWarnings = nil
	if _, _, handled := handleKeyW(m, tea.KeyPressMsg{Code: 'w', Text: "w"}); handled {
		t.Fatal("w with no warnings should fall through")
	}
}
