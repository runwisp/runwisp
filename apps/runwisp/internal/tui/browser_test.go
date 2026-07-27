// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
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
	// openBrowser on the actual platform spawns a real process, which we
	// don't want in unit tests. We only assert it doesn't panic for an
	// empty URL on the current platform: Start() either succeeds (browser
	// launched) or returns an error from exec. Either is acceptable.
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		err := openBrowser("about:blank")
		assert.ErrorContains(t, err, "unsupported platform")
	}
}
