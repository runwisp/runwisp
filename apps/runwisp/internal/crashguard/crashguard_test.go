// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package crashguard

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGuardRecoversAndSignals verifies a panic in a Guard-protected goroutine is
// recovered (does not crash the process) and turned into a self-SIGTERM, which
// is the daemon's clean-shutdown trigger. signal.Notify intercepts the SIGTERM
// so it never terminates the test binary.
func TestGuardRecoversAndSignals(t *testing.T) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		defer Guard()
		panic("boom")
	}()

	select {
	case <-sigCh:
		// Recovered and signalled: the goroutine did not crash the process.
	case <-time.After(5 * time.Second):
		t.Fatal("Guard did not self-signal SIGTERM after a panic")
	}
	// The daemon exits non-zero off this latch; without it a panic shut the
	// daemon down with exit 0, so systemd's Restart=on-failure never fired.
	if err := Panicked(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Panicked() = %v, want the recovered panic", err)
	}
}

// TestGuardNoPanicIsNoop verifies Guard is a no-op when the goroutine returns
// normally: no spurious shutdown signal.
func TestGuardNoPanicIsNoop(t *testing.T) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer Guard()
	}()
	<-done

	select {
	case <-sigCh:
		t.Fatal("Guard signalled SIGTERM without a panic")
	case <-time.After(200 * time.Millisecond):
		// No signal: correct.
	}
}
