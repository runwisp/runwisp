// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
	"strings"
)

// ParseRunUserSpec splits a run-as spec of the form `user` or `user:group`
// into its parts. Either side may be a name or a numeric id — this performs
// shape validation only; OS resolution (name → uid/gid) happens at run time in
// the executor, because the target account may not exist when the config is
// validated.
//
// The `user:group` form mirrors `chown` and docker-compose's `user:` field; a
// single `user` key is used (rather than separate user/group keys) because
// `group` is already the task's UI grouping label.
//
// An empty spec yields empty parts and no error: the run keeps the daemon's
// own identity.
func ParseRunUserSpec(spec string) (user, group string, err error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return "", "", nil
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) > 2 {
		return "", "", fmt.Errorf("%q has too many ':' separators; use \"user\" or \"user:group\"", spec)
	}
	user = strings.TrimSpace(parts[0])
	if user == "" {
		return "", "", fmt.Errorf("%q has an empty user", spec)
	}
	if len(parts) == 2 {
		group = strings.TrimSpace(parts[1])
		if group == "" {
			return "", "", fmt.Errorf("%q has an empty group after ':'", spec)
		}
	}
	return user, group, nil
}
