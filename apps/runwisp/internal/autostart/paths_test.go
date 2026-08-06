// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"errors"
	"path/filepath"
	"strings"
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
			// A relative --data is no longer rejected — it is resolved to an
			// absolute path (the current dir) and accepted with a notice, so
			// `--data .` installs into the current directory. Path is asserted
			// separately below because it depends on the test's cwd.
			name: "explicit-relative-absolutized",
			in: ResolveDataDirOptions{
				Explicit: "./mydata", ExplicitSet: true,
				HomeDir: "/home/alice",
			},
			want: want{action: ResolveActionNotice},
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

func TestResolveDataDir_RelativeAbsolutized(t *testing.T) {
	r, err := ResolveDataDir(ResolveDataDirOptions{
		Explicit:    "./mydata",
		ExplicitSet: true,
		HomeDir:     "/home/alice",
	})
	require.NoError(t, err)
	assert.Equal(t, ResolveActionNotice, r.Action)
	assert.True(t, filepath.IsAbs(r.Path),
		"a relative --data must be resolved to an absolute path for the unit (cwd=/ at boot)")
	assert.Truef(t, strings.HasSuffix(r.Path, filepath.FromSlash("/mydata")),
		"resolved path should end in the relative segment, got %q", r.Path)
	assert.Contains(t, r.Detail, "resolved from")
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

func TestResolveBinary_EmptyPathErrors(t *testing.T) {
	_, _, err := ResolveBinary(ResolveBinaryOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty executable path")
}

func TestResolveBinary_EvalSymlinksErrorIgnored(t *testing.T) {
	// On error the function falls back to the unresolved exe path —
	// confirm that flow doesn't surface the symlink error.
	p, _, err := ResolveBinary(ResolveBinaryOptions{
		ExecutablePath: "/usr/local/bin/runwisp",
		EvalSymlinks: func(string) (string, error) {
			return "", errors.New("nope")
		},
		HomeDir: "/home/alice",
	})
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/runwisp", p)
}

func TestResolveBinary_RelativeBecomesAbsolute(t *testing.T) {
	p, _, err := ResolveBinary(ResolveBinaryOptions{
		ExecutablePath: "relative/runwisp",
		EvalSymlinks:   identitySymlinks,
		HomeDir:        "/home/alice",
	})
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p), "relative paths must be made absolute")
}

func TestResolveDataDir_ErrorsWhenNoHomeNoXDG(t *testing.T) {
	_, err := ResolveDataDir(ResolveDataDirOptions{
		ExplicitSet:      false,
		HomeDir:          "",
		XDGDataHome:      "",
		BareDefaultHasDB: false,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HOME is unset")
}

func TestTransientDataDirReason(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/tmp/runwisp", "wiped on reboot"},
		{"/tmp", "wiped on reboot"},
		{"/var/tmp/runwisp", "tmpwatch"},
		{"/var/tmp", "tmpwatch"},
		{"/dev/shm/runwisp", "tmpfs"},
		{"/dev/shm", "tmpfs"},
		{"/srv/runwisp", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := transientDataDirReason(tc.path)
			if tc.want == "" {
				assert.Empty(t, got)
			} else {
				assert.Contains(t, got, tc.want)
			}
		})
	}
}

func TestXDGDataDir(t *testing.T) {
	assert.Equal(t, "/xdg/runwisp", xdgDataDir("/home/alice", "/xdg"))
	assert.Equal(t, "/home/alice/.local/share/runwisp", xdgDataDir("/home/alice", ""))
	assert.Empty(t, xdgDataDir("", ""))
}

func TestXDGConfigPath(t *testing.T) {
	assert.Equal(t, "/xdg/runwisp/runwisp.toml", XDGConfigPath("/home/alice", "/xdg"))
	assert.Equal(t, "/home/alice/.config/runwisp/runwisp.toml", XDGConfigPath("/home/alice", ""))
	assert.Empty(t, XDGConfigPath("", ""))
}

func TestResolveConfigPath_ExplicitRelativeBecomesAbsolute(t *testing.T) {
	p, err := ResolveConfigPath(ResolveConfigOptions{
		Explicit: "subdir/runwisp.toml", ExplicitSet: true,
	})
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p))
}

func TestResolveConfigPath_NeitherExistsFallsBackToXDG(t *testing.T) {
	p, err := ResolveConfigPath(ResolveConfigOptions{
		HomeDir:    "/home/alice",
		XDGExists:  false,
		BareExists: false,
	})
	require.NoError(t, err)
	assert.Equal(t, "/home/alice/.config/runwisp/runwisp.toml", p)
}

func TestResolveConfigPath_NeitherExistsNoHomeFallsBackToBareAbs(t *testing.T) {
	p, err := ResolveConfigPath(ResolveConfigOptions{
		HomeDir:    "",
		XDGExists:  false,
		BareExists: false,
	})
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p))
	assert.Contains(t, p, configFileName)
}

func TestResolveConfigPath_ExplicitDefaultIgnored(t *testing.T) {
	// Passing ExplicitSet=true with the default filename should be
	// treated as "no explicit value" and fall back to XDG.
	p, err := ResolveConfigPath(ResolveConfigOptions{
		Explicit: configFileName, ExplicitSet: true,
		HomeDir: "/home/alice", XDGExists: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "/home/alice/.config/runwisp/runwisp.toml", p)
}

func TestDetectWSLFromEnv_NoSignals(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "")
	// We can't unset /proc/sys/kernel/osrelease, but DetectWSL is the
	// pure logic — this just exercises the env-reading wrapper.
	_ = DetectWSLFromEnv()
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
