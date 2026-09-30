// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package bootid

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
)

// statLine is a real /proc/<pid>/stat shape with a comm holding spaces and a
// ')' to prove fields are counted from the last paren. starttime is 4242.
const statLine = "1 (my (odd) init) S 0 1 1 0 -1 4194560 100 200 0 0 5 6 7 8 20 0 1 0 4242 1000 50 18446744073709551615\n"

func fakeFS(files map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		if v, ok := files[name]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestFromProc(t *testing.T) {
	const bootPath, statPath = "/proc/sys/kernel/random/boot_id", "/proc/1/stat"
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"boot id and pid 1 start", map[string]string{bootPath: "abc-123\n", statPath: statLine}, "abc-123/4242"},
		{"pid 1 hidden falls back to boot id", map[string]string{bootPath: "abc-123\n"}, "abc-123"},
		{"malformed stat falls back to boot id", map[string]string{bootPath: "abc-123", statPath: "garbage"}, "abc-123"},
		{"no boot id means unknown", map[string]string{statPath: statLine}, ""},
		{"empty boot id means unknown", map[string]string{bootPath: "\n", statPath: statLine}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, fromProc(fakeFS(tc.files)))
		})
	}
}

// A container restart replaces PID 1, so the identity must change even though
// the kernel boot_id (the host's) does not.
func TestFromProcContainerRestartChangesIdentity(t *testing.T) {
	restarted := "1 (runwisp) S 0 1 1 0 -1 4194560 100 200 0 0 5 6 7 8 20 0 1 0 9999 1000 50 1\n"
	first := fromProc(fakeFS(map[string]string{"/proc/sys/kernel/random/boot_id": "host", "/proc/1/stat": statLine}))
	second := fromProc(fakeFS(map[string]string{"/proc/sys/kernel/random/boot_id": "host", "/proc/1/stat": restarted}))
	assert.NotEqual(t, first, second)
}

func TestFromProcReadError(t *testing.T) {
	assert.Empty(t, fromProc(func(string) ([]byte, error) { return nil, errors.New("boom") }))
}
