// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.16.3", "0.17.0", -1},
		{"0.17.0", "0.16.3", 1},
		{"1.0.0", "1.0.0", 0},
		{"v1.2.3", "1.2.3", 0},
		{"0.0.0-dev", "0.17.0", -1}, // dev parses as 0.0.0; gating happens via IsRelease
		{"1.2.3-rc1", "1.2.3", 0},   // prerelease suffix ignored
		{"1.10.0", "1.9.0", 1},      // numeric, not lexical
		{"garbage", "1.0.0", 0},     // unparseable => equal, never "update"
		{"1.0.0", "garbage", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"0.17.0":     true,
		"v0.17.0":    true,
		"0.0.0-dev":  false,
		"":           false,
		"not-semver": false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q)=%v want %v", v, got, want)
		}
	}
}

func TestAssetName(t *testing.T) {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	want := fmt.Sprintf("runwisp-%s-%s.tar.gz", runtime.GOOS, arch)
	if got := AssetName(); got != want {
		t.Errorf("AssetName()=%q want %q", got, want)
	}
}

func TestDetectMethod(t *testing.T) {
	t.Run("docker via env", func(t *testing.T) {
		t.Setenv("RUNWISP_CONTAINER", "1")
		if got := DetectMethod(); got != MethodDocker {
			t.Errorf("got %q want docker", got)
		}
	})

	skipIfContainer := func(t *testing.T) {
		if inContainer() {
			t.Skip("running inside a container")
		}
	}

	t.Run("npm via path", func(t *testing.T) {
		skipIfContainer(t)
		withExecutable(t, filepath.Join(t.TempDir(), "node_modules", ".bin", "runwisp"))
		if got := DetectMethod(); got != MethodNpm {
			t.Errorf("got %q want npm", got)
		}
	})

	t.Run("self when writable", func(t *testing.T) {
		skipIfContainer(t)
		dir := t.TempDir()
		exe := filepath.Join(dir, "runwisp")
		if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		withExecutable(t, exe)
		if got := DetectMethod(); got != MethodSelf {
			t.Errorf("got %q want self", got)
		}
	})

	t.Run("manual when dir read-only", func(t *testing.T) {
		skipIfContainer(t)
		if os.Geteuid() == 0 {
			t.Skip("root bypasses directory permissions")
		}
		dir := t.TempDir()
		exe := filepath.Join(dir, "runwisp")
		if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		withExecutable(t, exe)
		if got := DetectMethod(); got != MethodManual {
			t.Errorf("got %q want manual", got)
		}
	})
}

func TestLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.17.0","name":"ignored"}`))
	}))
	defer srv.Close()
	swapVar(t, &latestReleaseURL, srv.URL)

	got, err := LatestRelease(context.Background(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.17.0" {
		t.Errorf("got %q want v0.17.0", got)
	}
}

func TestLatestReleaseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	swapVar(t, &latestReleaseURL, srv.URL)

	if _, err := LatestRelease(context.Background(), srv.Client()); err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestUpgradeCommand(t *testing.T) {
	cases := []struct {
		m    Method
		want string
	}{
		{MethodDocker, "docker pull runwisp/runwisp"},
		{MethodNpm, "npm update -g runwisp"},
		{MethodManual, "re-run the installer: curl -fsSL https://get.runwisp.com | sh"},
		{MethodSelf, "re-run the installer: curl -fsSL https://get.runwisp.com | sh"},
	}
	for _, c := range cases {
		if got := UpgradeCommand(c.m); got != c.want {
			t.Errorf("UpgradeCommand(%q)=%q want %q", c.m, got, c.want)
		}
	}
}

// --- Apply safety ---

const oldBinary = "OLD-BINARY-CONTENT"

func TestApplyHappyPath(t *testing.T) {
	exe := setupFakeInstall(t)
	srv := serveRelease(t, "v9.9.9", script("9.9.9"))
	defer srv.Close()

	reexecCalled := false
	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		reexecCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reexecCalled {
		t.Error("reexec was not called")
	}
	if !fileContains(t, exe, "9.9.9") {
		t.Error("live binary was not replaced")
	}
	if _, err := os.Stat(exe + ".bak"); err != nil {
		t.Errorf("expected .bak rollback copy: %v", err)
	}
}

func TestApplyChecksumMismatchLeavesBinary(t *testing.T) {
	exe := setupFakeInstall(t)
	srv := serveReleaseBadChecksum(t, "v9.9.9", script("9.9.9"))
	defer srv.Close()

	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		t.Fatal("reexec must not run on checksum mismatch")
		return nil
	})
	if err == nil {
		t.Fatal("expected checksum error")
	}
	assertUntouched(t, exe)
}

func TestApplySmokeTestFailureLeavesBinary(t *testing.T) {
	exe := setupFakeInstall(t)
	// Valid checksum, but the binary reports the wrong version.
	srv := serveRelease(t, "v9.9.9", script("1.1.1"))
	defer srv.Close()

	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		t.Fatal("reexec must not run when the smoke test fails")
		return nil
	})
	if err == nil {
		t.Fatal("expected smoke-test error")
	}
	assertUntouched(t, exe)
}

func TestApplyReexecFailureRestoresBinary(t *testing.T) {
	exe := setupFakeInstall(t)
	srv := serveRelease(t, "v9.9.9", script("9.9.9"))
	defer srv.Close()

	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		return fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("expected error from failed reexec")
	}
	// The swap happened, then reexec failed, so the old binary must be restored.
	if !fileContains(t, exe, oldBinary) {
		t.Error("old binary was not restored after reexec failure")
	}
}

func TestApplyAlreadyInProgress(t *testing.T) {
	if !applying.CompareAndSwap(false, true) {
		t.Fatal("applying was already true at test start")
	}
	defer applying.Store(false)

	err := Apply(context.Background(), http.DefaultClient, "v9.9.9", func() error {
		t.Fatal("reexec must not run when an update is already in progress")
		return nil
	})
	if err == nil {
		t.Fatal("expected an error when an update is already in progress")
	}
}

func TestApplyLocateExecutableError(t *testing.T) {
	orig := osExecutable
	osExecutable = func() (string, error) { return "", fmt.Errorf("boom") }
	defer func() { osExecutable = orig }()

	err := Apply(context.Background(), http.DefaultClient, "v9.9.9", func() error {
		t.Fatal("reexec must not run when the executable can't be located")
		return nil
	})
	if err == nil {
		t.Fatal("expected an error when the executable can't be located")
	}
}

func TestApplyDownloadFailure(t *testing.T) {
	exe := setupFakeInstall(t)
	// No handlers registered: every request 404s.
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	swapVar(t, &releaseDownloadBase, srv.URL)

	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		t.Fatal("reexec must not run when the download fails")
		return nil
	})
	if err == nil {
		t.Fatal("expected a download error")
	}
	assertUntouched(t, exe)
}

func TestApplyChecksumFetchFailure(t *testing.T) {
	exe := setupFakeInstall(t)
	tarball := buildTarball(t, script("9.9.9"))
	mux := http.NewServeMux()
	mux.HandleFunc("/v9.9.9/"+AssetName(), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarball)
	})
	mux.HandleFunc("/v9.9.9/checksums-sha256.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	swapVar(t, &releaseDownloadBase, srv.URL)

	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		t.Fatal("reexec must not run when the checksum fetch fails")
		return nil
	})
	if err == nil {
		t.Fatal("expected a checksum-fetch error")
	}
	assertUntouched(t, exe)
}

func TestApplyChecksumNotFoundForAsset(t *testing.T) {
	exe := setupFakeInstall(t)
	tarball := buildTarball(t, script("9.9.9"))
	mux := http.NewServeMux()
	mux.HandleFunc("/v9.9.9/"+AssetName(), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarball)
	})
	mux.HandleFunc("/v9.9.9/checksums-sha256.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  some-other-file.tar.gz\n", sha256hex(tarball))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	swapVar(t, &releaseDownloadBase, srv.URL)

	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		t.Fatal("reexec must not run when no checksum matches the asset")
		return nil
	})
	if err == nil {
		t.Fatal("expected a 'no checksum for asset' error")
	}
	assertUntouched(t, exe)
}

func TestApplyArchiveMissingBinary(t *testing.T) {
	exe := setupFakeInstall(t)
	// A well-formed tarball whose only entry is not named "runwisp".
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("not the binary")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "README",
		Mode:     0o644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	tarball := buf.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/v9.9.9/"+AssetName(), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarball)
	})
	mux.HandleFunc("/v9.9.9/checksums-sha256.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", sha256hex(tarball), AssetName())
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	swapVar(t, &releaseDownloadBase, srv.URL)

	err := Apply(context.Background(), srv.Client(), "v9.9.9", func() error {
		t.Fatal("reexec must not run when the archive lacks the binary")
		return nil
	})
	if err == nil {
		t.Fatal("expected an 'archive did not contain' error")
	}
	assertUntouched(t, exe)
}

// --- helpers ---

func withExecutable(t *testing.T, path string) {
	t.Helper()
	orig := osExecutable
	osExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { osExecutable = orig })
}

func swapVar(t *testing.T, p *string, v string) {
	t.Helper()
	orig := *p
	*p = v
	t.Cleanup(func() { *p = orig })
}

// setupFakeInstall creates a throwaway "install dir" with an old runwisp binary
// and points osExecutable at it, returning the exe path.
func setupFakeInstall(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "runwisp")
	if err := os.WriteFile(exe, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	withExecutable(t, exe)
	return exe
}

// script returns a tiny executable shell script that emulates `runwisp
// --version` reporting the given version.
func script(version string) []byte {
	return []byte("#!/bin/sh\necho \"runwisp version " + version + "\"\n")
}

func buildTarball(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "runwisp",
		Mode:     0o755,
		Size:     int64(len(binary)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func serveRelease(t *testing.T, tag string, binary []byte) *httptest.Server {
	return serveReleaseWith(t, tag, binary, true)
}

func serveReleaseBadChecksum(t *testing.T, tag string, binary []byte) *httptest.Server {
	return serveReleaseWith(t, tag, binary, false)
}

func serveReleaseWith(t *testing.T, tag string, binary []byte, goodChecksum bool) *httptest.Server {
	t.Helper()
	tarball := buildTarball(t, binary)
	asset := AssetName()
	sum := sha256hex(tarball)
	if !goodChecksum {
		sum = sha256hex([]byte("something else"))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+tag+"/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(tarball)
	})
	mux.HandleFunc("/"+tag+"/checksums-sha256.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", sum, asset)
	})
	srv := httptest.NewServer(mux)
	swapVar(t, &releaseDownloadBase, srv.URL)
	return srv
}

func assertUntouched(t *testing.T, exe string) {
	t.Helper()
	if !fileContains(t, exe, oldBinary) {
		t.Error("live binary was modified despite failed update")
	}
	if _, err := os.Stat(exe + ".bak"); err == nil {
		t.Error("a .bak was left behind by a failed update")
	}
}

func fileContains(t *testing.T, path, substr string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(b, []byte(substr))
}
