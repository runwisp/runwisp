// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import "os/user"

// SystemUserExists reports whether name is an account on this machine. It is the
// CronOptions.UserExists every caller reading one of the machine's own crontabs
// should pass.
//
// It resolves through os/user, which is what internal/executor uses to become the
// account before a run — so a name this rejects is a name no run could have used
// either. That pairing is the whole point: the answer here has to be the same
// answer the executor would reach, or a job would be skipped for an identity that
// works or scheduled for one that doesn't.
func SystemUserExists(name string) bool {
	if name == "" {
		return false
	}
	_, err := user.Lookup(name)
	return err == nil
}
