// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
)

// resolveClientPassword resolves the daemon password for a client process
// (TUI, CLI) that itself does not run the daemon. The password is read from
// RUNWISP_PASSWORD; with SRP storing only a verifier, the daemon no longer
// retains the original password and the CLI has no way to recover it from
// SQLite. Use the local-JWT shortcut (mintLocalJWT) for password-less local
// operation; this function exists for the remote case where the operator
// supplies the password explicitly.
func resolveClientPassword() (string, error) {
	if envPw := os.Getenv("RUNWISP_PASSWORD"); envPw != "" {
		return envPw, nil
	}
	return "", errors.New("RUNWISP_PASSWORD is not set; the daemon stores only an SRP verifier and cannot recover the original password")
}
