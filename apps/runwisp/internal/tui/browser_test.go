// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"runtime"
	"testing"

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
	case "darwin", "windows":
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
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		err := openBrowserDefault("about:blank")
		assert.ErrorContains(t, err, "unsupported platform")
	}
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
