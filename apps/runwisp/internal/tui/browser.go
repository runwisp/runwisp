// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// canOpenBrowser reports whether the current environment has a graphical
// session capable of opening a browser. Returns false for SSH sessions
// without X11/Wayland forwarding, headless servers, and containers.
//
// Indirected through a function variable so tests can force the headless
// branch on graphical platforms (macOS CI runners).
var canOpenBrowser = canOpenBrowserDefault

func canOpenBrowserDefault() bool {
	switch runtime.GOOS {
	case "linux":
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	case "darwin":
		return true
	default:
		return false
	}
}

// openBrowser opens the given URL in the user's default browser.
//
// Indirected through a function variable for the same reason as the clipboard
// seam: it spawns a real process against the developer's own desktop session.
// The package's TestMain replaces it so no test can put a window on screen.
var openBrowser = openBrowserDefault

// openBrowserDefault uses platform-specific commands: xdg-open (Linux),
// open (macOS).
func openBrowserDefault(url string) error {
	var err error
	switch runtime.GOOS {
	case "linux":
		_, err = startReaped(exec.Command("xdg-open", url))
	case "darwin":
		_, err = startReaped(exec.Command("open", url))
	default:
		err = fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return err
}

// startReaped starts cmd without blocking on it and waits for it in the
// background, so the exited opener is reaped instead of lingering as a zombie
// for the TUI's lifetime. The channel delivers Wait's result.
func startReaped(cmd *exec.Cmd) (<-chan error, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, nil
}

// browseMsg opens url in a browser when the session has one and reports the
// outcome; without a browser it carries just the URL so the handler can fall
// back to the clipboard.
func browseMsg(url string) uikit.OpenBrowserMsg {
	if !canOpenBrowser() {
		return uikit.OpenBrowserMsg{URL: url}
	}
	if err := openBrowser(url); err != nil {
		return uikit.OpenBrowserMsg{URL: url, Err: err}
	}
	return uikit.OpenBrowserMsg{URL: url, BrowserOpened: true}
}
