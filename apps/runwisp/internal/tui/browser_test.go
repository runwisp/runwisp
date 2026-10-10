// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanOpenBrowser(t *testing.T) {
	switch runtime.GOOS {
	case "linux":
		t.Setenv("DISPLAY", "")
		t.Setenv("WAYLAND_DISPLAY", "")
		assert.False(t, canOpenBrowser(), "linux without DISPLAY/WAYLAND_DISPLAY → false")

		t.Setenv("DISPLAY", ":0")
		assert.True(t, canOpenBrowser(), "linux with DISPLAY set → true")

		t.Setenv("DISPLAY", "")
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		assert.True(t, canOpenBrowser(), "linux with WAYLAND_DISPLAY set → true")
	case "darwin":
		assert.True(t, canOpenBrowser(), "%s is always graphical", runtime.GOOS)
	default:
		assert.False(t, canOpenBrowser(), "unsupported platforms → false")
	}
}

func TestOpenBrowser_UnsupportedPlatformErrors(t *testing.T) {
	// openBrowserDefault, not openBrowser: this is the one test that wants the
	// real implementation rather than the seam TestMain installed. Calling it on
	// a supported platform would spawn a browser, so only the fallback arm is
	// exercised — everywhere else the switch reaches exec and there is nothing
	// to assert that wouldn't put a window on someone's screen.
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		err := openBrowserDefault("about:blank")
		assert.ErrorContains(t, err, "unsupported platform")
	}
}

// TestStartReaped_WaitsForTheOpener: the opener exits at once, and unless
// something waits on it it stays a zombie until the TUI quits, one per click.
func TestStartReaped_WaitsForTheOpener(t *testing.T) {
	done, err := startReaped(exec.Command("true"))
	require.NoError(t, err)
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the started process was never waited on")
	}

	_, err = startReaped(exec.Command("/nonexistent/opener"))
	assert.Error(t, err)
}

// TestBrowserSeamIsNeutralizedInTests is the guard for the whole package: if
// TestMain ever stops replacing openBrowser, every test that reaches a launch
// path starts throwing real tabs at whoever runs the suite. That regression is
// invisible in a passing run — the tests still pass, they just open a browser —
// so it needs an assertion of its own.
func TestBrowserSeamIsNeutralizedInTests(t *testing.T) {
	require.NoError(t, openBrowser("about:blank#seam-check"))
	opened := takeBrowserOpens(t)
	assert.Equal(t, []string{"about:blank#seam-check"}, opened,
		"openBrowser must route to the recorder, not to xdg-open/open")
}
