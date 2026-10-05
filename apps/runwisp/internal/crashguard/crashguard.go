// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package crashguard turns a panic in a long-lived daemon goroutine into the
// daemon's ordinary fatal-shutdown path instead of an unrecovered runtime
// crash.
//
// Why this exists: when a TUI is attached in-process (`runwisp station`, the
// inline fallback), Bubble Tea owns the terminal in alt-screen + raw mode.
// Bubble Tea's own panic recovery only covers its own goroutines — a panic in
// a daemon goroutine crashes the process without restoring the terminal, so the
// operator is dropped back to a shell full of garbled escape sequences. Routing
// the panic through SIGTERM (the same self-signal superviseServerStart uses for
// a fatal server error) lets the TUI tear down cleanly and restore the
// terminal; in headless mode it drives the normal graceful shutdown. The panic
// is also latched (Panicked) so that shutdown ends in a non-zero exit, which is
// what lets a service manager see the failure and restart the daemon.
package crashguard

import (
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync/atomic"
	"syscall"
)

// panicked holds the first panic Guard recovered. It is process-wide, like the
// SIGTERM Guard raises.
var panicked atomic.Pointer[error]

// Panicked returns the first panic Guard recovered in this process, or nil if
// none did. The daemon's shutdown path reads it to exit non-zero.
func Panicked() error {
	if p := panicked.Load(); p != nil {
		return *p
	}
	return nil
}

// Guard recovers a panic in the calling goroutine, logs it with its stack,
// latches it for Panicked, then self-signals SIGTERM to drive a clean daemon shutdown. Defer it at the top of
// a long-lived daemon goroutine:
//
//	go func() {
//		defer crashguard.Guard()
//		...
//	}()
//
// After signalling it blocks forever: the process is on its way down, and the
// goroutine must not return into a half-broken subsystem. The bounded shutdown
// timeouts in the graceful-shutdown path keep a blocked goroutine from wedging
// teardown.
func Guard() {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("daemon goroutine panicked; initiating shutdown",
		"panic", r, "stack", string(debug.Stack()))
	// Latched before the signal, so the shutdown it triggers sees it.
	err := fmt.Errorf("daemon goroutine panicked: %v", r)
	panicked.CompareAndSwap(nil, &err)
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
	select {} // block until the shutdown path tears the process down
}
