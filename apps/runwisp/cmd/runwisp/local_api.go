// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
)

// appDataDirCandidates are the data dirs the SDK adapters pick for a framework
// app (Laravel, Symfony, CodeIgniter), relative to the app root.
var appDataDirCandidates = []string{"storage/runwisp", "var/runwisp", "writable/runwisp"}

// daemonSocketPath is where a daemon binds its control socket: the explicit
// --socket / RUNWISP_SOCKET path when set, otherwise the default inside the
// data dir. Only `runwisp daemon` uses it; clients go through
// localAPISocketPath, which may look elsewhere.
func daemonSocketPath(f Flags) string {
	if f.Socket != "" {
		return f.Socket
	}
	return datadir.SocketPath(f.DataDir)
}

// localAPISocketPath resolves the control socket a CLI subcommand connects to.
// Every subcommand routes through here, so --socket lets them reach a daemon
// without also restating --data, and an app's data dir is found without it.
func localAPISocketPath(f Flags) string {
	if f.Socket != "" {
		return f.Socket
	}
	return datadir.SocketPath(clientDataDir(f))
}

// clientDataDir is the data dir a CLI subcommand looks for a running daemon in.
// It is f.DataDir, unless the operator named no data dir at all and the default
// holds no daemon socket: then it is the first conventional app data dir that
// does, so `runwisp status` works from an SDK app's root.
func clientDataDir(f Flags) string {
	if os.Getenv("RUNWISP_DATA") != "" || f.DataDir != defaultDataDir(os.Geteuid(), "") {
		return f.DataDir
	}
	return findAppDataDir(f.DataDir, appDataDirCandidates)
}

// findAppDataDir returns def when it holds a daemon socket, else the first
// candidate that does, else def. A socket another user's private data dir
// hides counts as found: connecting there then says whose daemon it is, where
// def would only say no daemon runs.
func findAppDataDir(def string, candidates []string) string {
	for _, dir := range append([]string{def}, candidates...) {
		if _, err := os.Stat(datadir.SocketPath(dir)); err == nil || errors.Is(err, fs.ErrPermission) {
			return dir
		}
	}
	return def
}

// otherUserDaemonError is non-nil when the data dir a client command looks in
// is another user's: its PID file can't even be looked at, so the lock check
// would say "no daemon" about a daemon that may well be up, and starting one
// here would make a second.
func otherUserDaemonError(f Flags) error {
	dir := clientDataDir(f)
	if _, err := os.Stat(datadir.PidFilePath(dir)); !errors.Is(err, fs.ErrPermission) {
		return nil
	}
	return &userFacingError{
		title:   fmt.Sprintf("cannot open the data dir %s", dir),
		details: "The daemon may be running, but " + otherUserHint(socketOwner(datadir.SocketPath(dir)), os.Args[1:]) + ".",
	}
}
