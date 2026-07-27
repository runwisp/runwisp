// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package main

import (
	"os"
	"syscall"
)

// sendSelfSIGTERM delivers SIGTERM to the current process. Used by tests that
// need to drive signal-based shutdown helpers.
func sendSelfSIGTERM() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
