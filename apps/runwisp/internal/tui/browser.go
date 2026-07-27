// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// canOpenBrowser reports whether the current environment has a graphical
// session capable of opening a browser. Returns false for SSH sessions
// without X11/Wayland forwarding, headless servers, and containers.
//
// Indirected through a function variable so tests can force the headless
// branch on graphical platforms (macOS CI runners, Windows).
var canOpenBrowser = canOpenBrowserDefault

func canOpenBrowserDefault() bool {
	switch runtime.GOOS {
	case "linux":
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	case "darwin", "windows":
		return true
	default:
		return false
	}
}

// openBrowser opens the given URL in the user's default browser.
// Uses platform-specific commands: xdg-open (Linux), open (macOS), cmd /c start (Windows).
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
