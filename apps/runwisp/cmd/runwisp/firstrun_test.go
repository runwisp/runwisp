// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptAndScaffoldAcceptsYes(t *testing.T) {
	cases := []string{"y\n", "Y\n", "yes\n", "YES\n", "\n", "  y  \n"}
	for _, answer := range cases {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runwisp.toml")
			var out bytes.Buffer

			require.NoError(t, promptAndScaffold(path, strings.NewReader(answer), &out))

			assert.FileExists(t, path)
			assert.Contains(t, out.String(), "No runwisp.toml at "+filepath.Dir(path))
			assert.Contains(t, out.String(), "Created "+path)
		})
	}
}

func TestPromptAndScaffoldDeclines(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "N\n", "anything-else\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runwisp.toml")
			var out bytes.Buffer

			err := promptAndScaffold(path, strings.NewReader(answer), &out)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "no runwisp.toml at "+filepath.Dir(path))
			assert.NoFileExists(t, path)
		})
	}
}

func TestScaffoldIfMissingNoopWhenFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte("[tasks.x]\nrun = \"true\"\n"), 0644))

	assert.NoError(t, scaffoldIfMissing(path))
}

func TestPromptAndScaffoldDetectsAdjacentCompose(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web: {image: nginx}\n"), 0644))
	path := filepath.Join(dir, "runwisp.toml")

	var out bytes.Buffer
	require.NoError(t, promptAndScaffold(path, strings.NewReader("\n"), &out))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "[compose.")
	assert.Contains(t, string(body), "file = \"./docker-compose.yml\"")
	assert.Contains(t, out.String(), "Detected docker-compose.yml")
}

func TestComposeAliasFromDir(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"simple", "/var/myapp", "myapp"},
		{"hyphenated", "/var/my-app", "my-app"},
		{"sanitized punctuation", "/var/my app!", "my-app"},
		{"root falls back to myapp", "/", "myapp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, composeAliasFromDir(tc.in))
		})
	}
}

func TestScaffoldIfMissing_NoTTYReturnsNilWithoutWritingFile(t *testing.T) {
	// go test invocation has no TTY on stdin, so missing config path
	// short-circuits to nil without creating a file.
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, scaffoldIfMissing(path))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file created, got %v", err)
	}
}

func TestScaffoldIfMissing_StatErrorBubblesUp(t *testing.T) {
	// Create a dir, then point scaffoldIfMissing at a path *through* a regular
	// file — that yields a non-ErrNotExist Stat error (ENOTDIR).
	dir := t.TempDir()
	regular := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(regular, []byte("x"), 0600))
	bad := filepath.Join(regular, "subdir", "runwisp.toml")
	if err := scaffoldIfMissing(bad); err == nil {
		t.Fatal("expected ENOTDIR-style error to bubble up")
	}
}

func TestConfigLocation_DefaultNameReturnsDir(t *testing.T) {
	dir := t.TempDir()
	got := configLocation(filepath.Join(dir, "runwisp.toml"))
	if got != dir {
		t.Fatalf("configLocation default name = %q, want dir %q", got, dir)
	}
}

func TestConfigLocation_CustomNameReturnsAbsPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.toml")
	got := configLocation(path)
	if got != path {
		t.Fatalf("configLocation custom = %q, want %q", got, path)
	}
}
