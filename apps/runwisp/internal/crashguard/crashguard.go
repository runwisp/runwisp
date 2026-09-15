// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package crashguard turns a panic in a long-lived daemon goroutine into the
// daemon's ordinary fatal-shutdown path instead of an unrecovered runtime
// crash.
//
// Why this exists: when a TUI is attached in-process (`runwisp cloud`, the
// inline fallback), Bubble Tea owns the terminal in alt-screen + raw mode.
// Bubble Tea's own panic recovery only covers its own goroutines — a panic in
// a daemon goroutine crashes the process without restoring the terminal, so the
// operator is dropped back to a shell full of garbled escape sequences. Routing
// the panic through SIGTERM (the same self-signal superviseServerStart uses for
// a fatal server error) lets the TUI tear down cleanly and restore the
// terminal; in headless mode it drives the normal graceful shutdown.
package crashguard

import (
	"log/slog"
	"os"
	"runtime/debug"
	"syscall"
)

// Guard recovers a panic in the calling goroutine, logs it with its stack, then
// self-signals SIGTERM to drive a clean daemon shutdown. Defer it at the top of
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
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(syscall.SIGTERM)
	}
	select {} // block until the shutdown path tears the process down
}
