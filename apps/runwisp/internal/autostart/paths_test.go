// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package autostart

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// identitySymlinks ignores symlinks for tests where we don't care.
func identitySymlinks(p string) (string, error) { return p, nil }

func TestResolveBinary_Reject(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"tmp", "/tmp/runwisp", "/tmp"},
		{"var-tmp", "/var/tmp/runwisp", "/var/tmp"},
		{"dev-shm", "/dev/shm/runwisp", "/dev/shm"},
		{"go-build", "/tmp/go-build123/abc/exe/runwisp", "go run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ResolveBinary(ResolveBinaryOptions{
				ExecutablePath: tc.path,
				EvalSymlinks:   identitySymlinks,
				HomeDir:        "/home/alice",
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestResolveBinary_AcceptWithWarning(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		home    string
		warning string
	}{
		{
			name:    "go-bin",
			path:    "/home/alice/go/bin/runwisp",
			home:    "/home/alice",
			warning: "~/go/bin",
		},
		{
			name:    "cache",
			path:    "/home/alice/.cache/runwisp/runwisp",
			home:    "/home/alice",
			warning: "~/.cache",
		},
		{
			name:    "spaces",
			path:    "/opt/my runwisp/runwisp",
			home:    "/home/alice",
			warning: "spaces",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, w, err := ResolveBinary(ResolveBinaryOptions{
				ExecutablePath: tc.path,
				EvalSymlinks:   identitySymlinks,
				HomeDir:        tc.home,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.path, p)
			assert.Contains(t, w, tc.warning)
		})
	}
}

func TestResolveBinary_AcceptSilent(t *testing.T) {
	p, w, err := ResolveBinary(ResolveBinaryOptions{
		ExecutablePath: "/usr/local/bin/runwisp",
		EvalSymlinks:   identitySymlinks,
		HomeDir:        "/home/alice",
	})
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/runwisp", p)
	assert.Empty(t, w)
}

func TestResolveBinary_SymlinkResolution(t *testing.T) {
	p, _, err := ResolveBinary(ResolveBinaryOptions{
		ExecutablePath: "/home/alice/bin/runwisp",
		EvalSymlinks: func(in string) (string, error) {
			if in == "/home/alice/bin/runwisp" {
				return "/usr/local/bin/runwisp", nil
			}
			return "", errors.New("unexpected path")
		},
		HomeDir: "/home/alice",
	})
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/runwisp", p,
		"resolve binary must follow symlinks to the real installed path")
}

func TestResolveDataDir(t *testing.T) {
	type want struct {
		path   string
		action ResolveAction
	}
	cases := []struct {
		name string
		in   ResolveDataDirOptions
		want want
	}{
		{
			name: "explicit-absolute",
			in: ResolveDataDirOptions{
				Explicit: "/srv/runwisp", ExplicitSet: true,
				HomeDir: "/home/alice",
			},
			want: want{path: "/srv/runwisp", action: ResolveActionAccept},
		},
		{
			name: "explicit-relative-rejected",
			in: ResolveDataDirOptions{
				Explicit: "./mydata", ExplicitSet: true,
				HomeDir: "/home/alice",
			},
			want: want{action: ResolveActionReject},
		},
		{
			name: "explicit-tmp-rejected",
			in: ResolveDataDirOptions{
				Explicit: "/tmp/runwisp", ExplicitSet: true,
				HomeDir: "/home/alice",
			},
			want: want{action: ResolveActionReject},
		},
		{
			name: "default-with-db-prompts-keep",
			in: ResolveDataDirOptions{
				ExplicitSet:      false,
				HomeDir:          "/home/alice",
				BareDefaultHasDB: true,
			},
			want: want{action: ResolveActionPrompt},
		},
		{
			name: "default-without-db-prompts-choice-xdg",
			in: ResolveDataDirOptions{
				ExplicitSet:      false,
				HomeDir:          "/home/alice",
				BareDefaultHasDB: false,
			},
			want: want{
				path:   "/home/alice/.local/share/runwisp",
				action: ResolveActionPrompt,
			},
		},
		{
			name: "default-without-db-prefers-xdg-when-set",
			in: ResolveDataDirOptions{
				ExplicitSet:      false,
				HomeDir:          "/home/alice",
				XDGDataHome:      "/home/alice/.data",
				BareDefaultHasDB: false,
			},
			want: want{
				path:   "/home/alice/.data/runwisp",
				action: ResolveActionPrompt,
			},
		},
		{
			name: "explicit-with-spaces-warns",
			in: ResolveDataDirOptions{
				Explicit: "/srv/my runwisp", ExplicitSet: true,
				HomeDir: "/home/alice",
			},
			want: want{path: "/srv/my runwisp", action: ResolveActionWarn},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ResolveDataDir(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want.action, r.Action)
			if tc.want.path != "" {
				assert.Equal(t, tc.want.path, r.Path)
			}
		})
	}
}

func TestResolveDataDir_BareDefaultDBUsesAbsolutePath(t *testing.T) {
	r, err := ResolveDataDir(ResolveDataDirOptions{
		ExplicitSet:      false,
		HomeDir:          "/home/alice",
		BareDefaultHasDB: true,
	})
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(r.Path),
		"the PromptKeep path must be absolute so the unit baked into systemd resolves on cwd=/")
}

func TestResolveConfigPath(t *testing.T) {
	t.Run("xdg-preferred-when-both-exist", func(t *testing.T) {
		p, err := ResolveConfigPath(ResolveConfigOptions{
			HomeDir:    "/home/alice",
			XDGExists:  true,
			BareExists: true,
		})
		require.NoError(t, err)
		assert.Equal(t, "/home/alice/.config/runwisp/runwisp.toml", p)
	})
	t.Run("bare-when-only-bare-exists", func(t *testing.T) {
		p, err := ResolveConfigPath(ResolveConfigOptions{
			HomeDir:    "/home/alice",
			XDGExists:  false,
			BareExists: true,
		})
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(p))
	})
	t.Run("explicit-absolute-honored", func(t *testing.T) {
		p, err := ResolveConfigPath(ResolveConfigOptions{
			Explicit: "/etc/runwisp/runwisp.toml", ExplicitSet: true,
			HomeDir: "/home/alice",
		})
		require.NoError(t, err)
		assert.Equal(t, "/etc/runwisp/runwisp.toml", p)
	})
}

func TestDetectWSL(t *testing.T) {
	cases := []struct {
		name string
		in   WSLDetectInput
		want bool
	}{
		{"distro-name-set", WSLDetectInput{WSLDistroName: "Ubuntu"}, true},
		{"osrelease-microsoft", WSLDetectInput{OSRelease: "5.15.0-microsoft-WSL2"}, true},
		{"osrelease-wsl", WSLDetectInput{OSRelease: "5.10.16.3-WSL"}, true},
		{"osrelease-case-insensitive", WSLDetectInput{OSRelease: "Microsoft"}, true},
		{"plain-linux", WSLDetectInput{OSRelease: "6.5.0-generic"}, false},
		{"empty", WSLDetectInput{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, DetectWSL(tc.in))
		})
	}
}
