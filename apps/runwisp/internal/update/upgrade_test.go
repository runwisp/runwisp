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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// releaseRT fakes GitHub's release endpoints: path → response.
type releaseRT map[string]*http.Response

func (r releaseRT) RoundTrip(req *http.Request) (*http.Response, error) {
	res, ok := r[req.URL.Path]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: http.NoBody, Header: make(http.Header)}, nil
	}
	return res, nil
}

func ok(body []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}

func tarball(t *testing.T, bin []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "runwisp", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write(bin)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func releaseServer(t *testing.T, sumOf []byte) *http.Client {
	archive := tarball(t, []byte("new binary"))
	sum := sha256.Sum256(sumOf)
	if sumOf == nil {
		sum = sha256.Sum256(archive)
	}
	return &http.Client{Transport: releaseRT{
		"/rel/download/v1.6.0/checksums-sha256.txt":     ok([]byte(hex.EncodeToString(sum[:]) + "  runwisp-linux-x64.tar.gz\n")),
		"/rel/download/v1.6.0/runwisp-linux-x64.tar.gz": ok(archive),
	}}
}

func oldBinary(t *testing.T) string {
	exe := filepath.Join(t.TempDir(), "runwisp")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestSelfReplaceSwapsVerifiedBinary(t *testing.T) {
	exe := oldBinary(t)
	if err := SelfReplace(context.Background(), releaseServer(t, nil), "http://gh.test/rel", "v1.6.0", "linux", "amd64", exe); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "new binary" {
		t.Fatalf("exe = %q, want the new binary", got)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Fatalf("leftover files next to exe: %v", entries)
	}
}

func TestSelfReplaceChecksumMismatchKeepsOldBinary(t *testing.T) {
	exe := oldBinary(t)
	err := SelfReplace(context.Background(), releaseServer(t, []byte("something else")), "http://gh.test/rel", "v1.6.0", "linux", "amd64", exe)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatalf("exe = %q, want it untouched", got)
	}
}

func TestLatestVersionReadsRedirect(t *testing.T) {
	redirect := &http.Response{StatusCode: http.StatusFound, Body: http.NoBody, Header: http.Header{
		"Location": {"https://github.com/runwisp/runwisp/releases/tag/v1.6.0"},
	}}
	client := &http.Client{Transport: releaseRT{"/rel/latest": redirect}}
	got, err := LatestVersion(context.Background(), client, "http://gh.test/rel")
	if err != nil || got != "v1.6.0" {
		t.Fatalf("LatestVersion = %q, %v; want v1.6.0", got, err)
	}

	client = &http.Client{Transport: releaseRT{}}
	if _, err := LatestVersion(context.Background(), client, "http://gh.test/rel"); err == nil {
		t.Fatal("want an error when there is no redirect")
	}
}

func TestMethod(t *testing.T) {
	cases := []struct {
		source, exe string
		argv        []string
		refuses     bool
	}{
		{"homebrew", "/opt/homebrew/Cellar/runwisp/1.5.0/libexec/runwisp", []string{"brew", "upgrade", "runwisp"}, false},
		{"npm", "/home/u/.bun/install/global/node_modules/@runwisp/linux-x64/runwisp", []string{"bun", "add", "-g", "runwisp@latest"}, false},
		{"npm", "/home/u/.local/share/pnpm/global/5/node_modules/.pnpm/@runwisp+linux-x64@1.5.0/node_modules/@runwisp/linux-x64/runwisp", []string{"pnpm", "add", "-g", "runwisp@latest"}, false},
		{"npm", "/home/u/.config/yarn/global/node_modules/@runwisp/linux-x64/runwisp", []string{"yarn", "global", "add", "runwisp@latest"}, false},
		{"npm", "/usr/local/lib/node_modules/runwisp/node_modules/@runwisp/linux-x64/runwisp", []string{"npm", "install", "-g", "runwisp@latest"}, false},
		{"npm", "/srv/app/node_modules/@runwisp/linux-x64/runwisp", nil, true},
		{"npx", "/home/u/.npm/_npx/abc/node_modules/@runwisp/linux-x64/runwisp", nil, true},
		{"docker", "/usr/local/bin/runwisp", nil, true},
		{"script", "/usr/local/bin/runwisp", nil, false},
		{"other", "/home/u/.local/bin/runwisp", nil, false},
	}
	for _, c := range cases {
		argv, refusal := Method(c.source, c.exe)
		if !slices.Equal(argv, c.argv) || (refusal != "") != c.refuses {
			t.Errorf("Method(%q, %q) = %v, %q; want %v, refuses=%v", c.source, c.exe, argv, refusal, c.argv, c.refuses)
		}
	}
	if _, refusal := Method("npm", "/srv/app/node_modules/@runwisp/linux-x64/runwisp"); !strings.Contains(refusal, "/srv/app") {
		t.Errorf("project-local refusal %q should name the project", refusal)
	}
}

func TestHelpersRejectBadInput(t *testing.T) {
	if _, err := assetName("windows", "amd64"); err == nil {
		t.Error("assetName: windows should have no build")
	}
	if _, err := assetName("linux", "386"); err == nil {
		t.Error("assetName: 386 should have no build")
	}
	if _, err := checksumFor([]byte("abc  other.tar.gz\n"), "runwisp-linux-x64.tar.gz"); err == nil {
		t.Error("checksumFor: missing asset should error")
	}
	if _, err := extractBinary([]byte("not gzip")); err == nil {
		t.Error("extractBinary: non-gzip should error")
	}
	if _, err := extractBinary(tarball(t, nil)[:10]); err == nil {
		t.Error("extractBinary: truncated archive should error")
	}
	var empty bytes.Buffer
	gz := gzip.NewWriter(&empty)
	tar.NewWriter(gz).Close()
	gz.Close()
	if _, err := extractBinary(empty.Bytes()); err == nil {
		t.Error("extractBinary: archive without runwisp should error")
	}
	if err := replaceFile(filepath.Join(t.TempDir(), "missing", "runwisp"), []byte("x")); err == nil {
		t.Error("replaceFile: missing directory should error")
	}
}

func TestSelfReplaceFailuresKeepOldBinary(t *testing.T) {
	exe := oldBinary(t)
	ctx := context.Background()
	if err := SelfReplace(ctx, releaseServer(t, nil), "https://x/rel", "v1.6.0", "windows", "amd64", exe); err == nil {
		t.Error("unsupported platform should error")
	}
	if err := SelfReplace(ctx, releaseServer(t, nil), "https://x/rel", "v9.9.9", "linux", "amd64", exe); err == nil {
		t.Error("404 on checksums should error")
	}
	sums := ok([]byte(strings.Repeat("0", 64) + "  other.tar.gz\n"))
	client := &http.Client{Transport: releaseRT{"/rel/download/v1.6.0/checksums-sha256.txt": sums}}
	if err := SelfReplace(ctx, client, "https://x/rel", "v1.6.0", "linux", "amd64", exe); err == nil {
		t.Error("checksums without the asset should error")
	}
	sum := sha256.Sum256([]byte("x"))
	client = &http.Client{Transport: releaseRT{"/rel/download/v1.6.0/checksums-sha256.txt": ok([]byte(hex.EncodeToString(sum[:]) + "  runwisp-linux-x64.tar.gz\n"))}}
	if err := SelfReplace(ctx, client, "https://x/rel", "v1.6.0", "linux", "amd64", exe); err == nil {
		t.Error("404 on the archive should error")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old binary" {
		t.Errorf("binary changed after failed upgrades: %q", b)
	}
}

func TestLatestVersionRejectsMissingRedirect(t *testing.T) {
	client := &http.Client{Transport: releaseRT{}}
	if _, err := LatestVersion(context.Background(), client, "https://x/rel"); err == nil {
		t.Error("no Location header should error")
	}
}

func TestIsNewerIgnoresInvalidVersions(t *testing.T) {
	if IsNewer("garbage", "v1.0.0") || IsNewer("v1.1.0", "") {
		t.Error("invalid versions are never newer")
	}
	if !IsNewer("1.1.0", "v1.0.0") || IsNewer("v1.0.0", "v1.0.0") {
		t.Error("IsNewer compares semver, with or without the v")
	}
}
