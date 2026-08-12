// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"fmt"
	"os/user"
	"strconv"
	"syscall"

	"github.com/runwisp/runwisp/internal/model"
)

// runAs is a resolved run-as identity: the process credential the child is
// started with, plus the HOME/USER/LOGNAME identity env seeded beneath the
// task's own env so the dropped process looks like a real login for that user.
//
// Like signal.go, this lives without a build tag: the executor package is
// already host-Unix only (it relies on syscall.Kill / Setpgid), and
// syscall.Credential + os/user are available on every supported platform
// (Linux, macOS, WSL). A !unix stub would guard a build that never compiles.
type runAs struct {
	cred     *syscall.Credential
	identity []string
	// home is the target account's home directory, used to expand a
	// working_dir of `~` — see resolveWorkingDir. Empty when the spec was a bare
	// numeric uid with no account entry.
	home string
}

// resolveRunAs turns a `user` / `user:group` spec into a syscall.Credential and
// the matching identity environment. Resolution happens at run time (not config
// load) because the target account may not exist when the config is validated.
// A digit-only field is treated as a numeric id; otherwise it is looked up by
// name. Any failure is returned to the caller, which surfaces it as a failed
// start — never a silent fallback to the daemon's own uid.
func resolveRunAs(spec string) (*runAs, error) {
	userPart, groupPart, err := model.ParseRunUserSpec(spec)
	if err != nil {
		return nil, err
	}
	if userPart == "" {
		return nil, nil
	}

	ru, err := lookupRunAsUser(userPart)
	if err != nil {
		return nil, err
	}

	gid, gidKnown := ru.gid, ru.gidKnown
	if groupPart != "" {
		gid, err = lookupRunAsGid(groupPart)
		if err != nil {
			return nil, err
		}
		gidKnown = true
	}
	if !gidKnown {
		return nil, fmt.Errorf("numeric uid %q has no account entry; give an explicit group, e.g. %q", userPart, userPart+":"+userPart)
	}

	cred := &syscall.Credential{Uid: ru.uid, Gid: gid}
	if err := applySupplementaryGroups(cred, ru.entry, userPart); err != nil {
		return nil, err
	}

	return &runAs{cred: cred, identity: identityEnv(ru.username, ru.home), home: ru.home}, nil
}

// resolvedUser carries the run-as user resolution. entry is nil when the spec
// is a bare numeric uid with no matching account, in which case gidKnown is
// false unless the caller supplies an explicit group.
type resolvedUser struct {
	uid      uint32
	gid      uint32
	gidKnown bool
	username string
	home     string
	entry    *user.User
}

func lookupRunAsUser(part string) (resolvedUser, error) {
	if isAllDigits(part) {
		entry, err := user.LookupId(part)
		if err != nil {
			// No account entry for this numeric uid — run with the bare id. The
			// caller requires an explicit group (gidKnown stays false).
			n, convErr := strconv.ParseUint(part, 10, 32)
			if convErr != nil {
				return resolvedUser{}, fmt.Errorf("invalid numeric uid %q: %w", part, convErr)
			}
			return resolvedUser{uid: uint32(n), username: part}, nil
		}
		return userFromEntry(entry)
	}
	entry, err := user.Lookup(part)
	if err != nil {
		return resolvedUser{}, fmt.Errorf("unknown user %q: %w", part, err)
	}
	return userFromEntry(entry)
}

func userFromEntry(u *user.User) (resolvedUser, error) {
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return resolvedUser{}, fmt.Errorf("user %q has a non-numeric uid %q", u.Username, u.Uid)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return resolvedUser{}, fmt.Errorf("user %q has a non-numeric gid %q", u.Username, u.Gid)
	}
	return resolvedUser{
		uid:      uint32(uid),
		gid:      uint32(gid),
		gidKnown: true,
		username: u.Username,
		home:     u.HomeDir,
		entry:    u,
	}, nil
}

func lookupRunAsGid(part string) (uint32, error) {
	if isAllDigits(part) {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid numeric gid %q: %w", part, err)
		}
		return uint32(n), nil
	}
	g, err := user.LookupGroup(part)
	if err != nil {
		return 0, fmt.Errorf("unknown group %q: %w", part, err)
	}
	n, err := strconv.ParseUint(g.Gid, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("group %q has a non-numeric gid %q", part, g.Gid)
	}
	return uint32(n), nil
}

// applySupplementaryGroups fills the credential's supplementary group list from
// the resolved user's memberships. When the user is a bare numeric id (no
// account entry) we leave Groups nil with NoSetGroups=false, so the child's
// supplementary groups are cleared to just the primary gid rather than
// inheriting the daemon's — the secure default when dropping privileges.
//
// A failure to enumerate the target user's groups aborts the run. Fail-open
// here (leaving the daemon's group set in place via NoSetGroups) would let a
// task nominally dropped to an unprivileged user keep the daemon's
// supplementary groups — docker, disk, or another sensitive group — which is a
// privilege-escalation path (docker-group membership is effectively root). A
// privilege drop must never silently retain more groups than it dropped to.
func applySupplementaryGroups(cred *syscall.Credential, entry *user.User, label string) error {
	if entry == nil {
		return nil
	}
	gidStrs, err := entry.GroupIds()
	if err != nil {
		return fmt.Errorf("run-as %q: cannot read supplementary groups: %w", label, err)
	}
	groups := make([]uint32, 0, len(gidStrs))
	for _, s := range gidStrs {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return fmt.Errorf("run-as %q: non-numeric supplementary gid %q: %w", label, s, err)
		}
		groups = append(groups, uint32(n))
	}
	cred.Groups = groups
	return nil
}

// identityEnv returns the HOME/USER/LOGNAME entries for the target user. A
// dropped process otherwise inherits the daemon's HOME (often /root), which
// breaks tools that write under $HOME. Empty fields are skipped.
func identityEnv(username, home string) []string {
	var env []string
	if username != "" {
		env = append(env, "USER="+username, "LOGNAME="+username)
	}
	if home != "" {
		env = append(env, "HOME="+home)
	}
	return env
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
