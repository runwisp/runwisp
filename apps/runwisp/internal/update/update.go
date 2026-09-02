// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package update discovers newer RunWisp releases on GitHub and, for standalone
// binary installs, replaces the running binary in place — safely. The live
// binary is never touched until a fully downloaded, checksum-verified,
// smoke-tested candidate exists, so no failure mode (network drop, partial
// write, corrupt/wrong-arch artifact, failed rename) can leave the installation
// without a working executable.
//
// Distribution shape (see scripts/install.sh): GitHub releases carry
// runwisp-<os>-<arch>.tar.gz plus checksums-sha256.txt at the same URL, with
// tags of the form v<semver>. Docker and npm installs are detected and refused
// for in-place swap — those are upgraded through their own package managers.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"log/slog"
)

const (
	// DevVersion mirrors version.Version's ldflag default. A binary still
	// carrying it was not built from a release, so update checks are skipped —
	// keep this in sync with internal/version.Version's zero value.
	DevVersion = "0.0.0-dev"

	binaryName = "runwisp"

	maxTarballBytes = 100 << 20 // generous cap; the real binary is a few MiB
	maxMetaBytes    = 1 << 20   // release JSON + checksums file
)

// GitHub endpoints. Package vars (not consts) only so tests can point them at a
// local httptest server; production never reassigns them.
var (
	latestReleaseURL    = "https://api.github.com/repos/runwisp/runwisp/releases/latest"
	releaseDownloadBase = "https://github.com/runwisp/runwisp/releases/download"
)

// applying is a process-wide single-flight guard: only one Apply runs at a time.
var applying atomic.Bool

// osExecutable is os.Executable, overridable in tests so Apply/DetectMethod
// don't operate on the test runner's own binary.
var osExecutable = os.Executable

// Method is how this installation should be upgraded.
type Method string

const (
	MethodSelf   Method = "self"   // writable standalone binary → in-place swap
	MethodDocker Method = "docker" // immutable image → docker pull
	MethodNpm    Method = "npm"    // package-manager owned → npm/bun update
	MethodManual Method = "manual" // binary dir not writable → re-run installer
)

// IsRelease reports whether v looks like a released version (parseable semver and
// not the dev default). Callers skip update checks entirely for dev builds.
func IsRelease(v string) bool {
	if v == DevVersion {
		return false
	}
	_, ok := parseVersion(v)
	return ok
}

// Compare returns -1, 0, or 1 as a is older than, equal to, or newer than b,
// on X.Y.Z (leading "v" and any -prerelease/+build suffix ignored). Unparseable
// input compares equal (0) so a garbled version never triggers an "update".
func Compare(a, b string) int {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0
	}
	for i := range 3 {
		switch {
		case av[i] < bv[i]:
			return -1
		case av[i] > bv[i]:
			return 1
		}
	}
	return 0
}

func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// DetectMethod classifies how this installation should be upgraded. Cheap enough
// to call once at boot; it probes the executable path and its directory.
func DetectMethod() Method {
	exe, err := osExecutable()
	if err != nil {
		return MethodManual
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved // npm ships the binary behind a node_modules/.bin symlink
	}
	switch {
	case inContainer():
		return MethodDocker
	case strings.Contains(exe, "node_modules"):
		return MethodNpm
	case !dirWritable(filepath.Dir(exe)):
		return MethodManual
	default:
		return MethodSelf
	}
}

// UpgradeCommand is the one-line instruction a non-self install should show
// instead of an in-app "update" button.
func UpgradeCommand(m Method) string {
	switch m {
	case MethodDocker:
		return "docker pull runwisp/runwisp"
	case MethodNpm:
		return "npm update -g runwisp"
	default:
		return "re-run the installer: curl -fsSL https://get.runwisp.com | sh"
	}
}

func inContainer() bool {
	if os.Getenv("RUNWISP_CONTAINER") == "1" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// dirWritable reports whether we can create (and thus rename) files in dir.
// Probes with a temp file rather than checking mode bits so it respects the
// effective uid/gid and any read-only mount. Note the running binary itself
// can't be opened for write on Linux (ETXTBSY), so directory writability — what
// an atomic rename actually needs — is the right thing to test.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".runwisp-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// LatestRelease returns the newest stable release tag (e.g. "v0.17.0"). The
// GitHub /releases/latest endpoint already excludes drafts and prereleases.
func LatestRelease(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases API returned %s", resp.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetaBytes)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode release metadata: %w", err)
	}
	if payload.TagName == "" {
		return "", errors.New("release metadata had no tag_name")
	}
	return payload.TagName, nil
}

// AssetName is the release asset for the running platform, e.g.
// "runwisp-linux-x64.tar.gz". Mirrors scripts/install.sh's target scheme.
func AssetName() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return fmt.Sprintf("%s-%s-%s.tar.gz", binaryName, runtime.GOOS, arch)
}

// Apply downloads release `tag`, verifies it, atomically swaps it in for the
// running binary, and calls reexec to restart into it. reexec is injected so it
// can trigger the daemon's own graceful-shutdown-then-exec path (and so tests
// can observe the swap without replacing their own process). Every step before
// the swap is reversible; see the package doc for the safety guarantee.
func Apply(ctx context.Context, client *http.Client, tag string, reexec func() error) error {
	if !applying.CompareAndSwap(false, true) {
		return errors.New("an update is already in progress")
	}
	defer applying.Store(false)

	exe, err := osExecutable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	base := releaseDownloadBase + "/" + tag
	asset := AssetName()

	tarball, err := downloadToTemp(ctx, client, base+"/"+asset)
	if err != nil {
		return err
	}
	defer os.Remove(tarball)

	want, err := fetchChecksum(ctx, client, base+"/checksums-sha256.txt", asset)
	if err != nil {
		return err
	}
	if err := verifySHA256(tarball, want); err != nil {
		return err
	}

	candidate, err := extractBinary(tarball, filepath.Dir(exe))
	if err != nil {
		return err
	}
	// Removed only if the swap below never consumes it (rename moves it to exe).
	defer os.Remove(candidate)

	if err := smokeTest(ctx, candidate, tag); err != nil {
		return err
	}

	backup := exe + ".bak"
	if err := swapBinary(exe, candidate, backup); err != nil {
		return err
	}

	if err := reexec(); err != nil {
		// Restarting failed after the swap; put the old binary back so a manual
		// restart doesn't come up on a half-applied state.
		if rbErr := os.Rename(backup, exe); rbErr != nil {
			return fmt.Errorf("restart after update failed: %w (and restoring %s failed: %v)", err, backup, rbErr)
		}
		return fmt.Errorf("restart after update failed, restored previous binary: %w", err)
	}
	return nil
}

func downloadToTemp(ctx context.Context, client *http.Client, url string) (path string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}

	f, err := os.CreateTemp("", "runwisp-update-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()

	n, err := io.Copy(f, io.LimitReader(resp.Body, maxTarballBytes+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if n > maxTarballBytes {
		return "", fmt.Errorf("download %s: exceeds %d byte cap", url, maxTarballBytes)
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// fetchChecksum returns the expected sha256 hex for asset from a
// "sha256  filename" checksums file.
func fetchChecksum(ctx context.Context, client *http.Client, url, asset string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch checksums: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetaBytes))
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && filepath.Base(fields[1]) == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in checksums file", asset)
}

func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// extractBinary pulls the "runwisp" entry out of the gzipped tarball and writes
// it, executable, to a temp file in destDir (same filesystem as the target so
// the final rename is atomic). Returns the candidate path.
func extractBinary(tarball, destDir string) (path string, err error) {
	f, err := os.Open(tarball)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("archive did not contain %q", binaryName)
		}
		if err != nil {
			return "", fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != binaryName {
			continue
		}
		return writeCandidateBinary(tr, destDir)
	}
}

// writeCandidateBinary copies the current tar entry (the matched binaryName
// header) into an executable temp file in destDir (same filesystem as the
// target so the final rename is atomic). Returns the candidate path.
func writeCandidateBinary(tr *tar.Reader, destDir string) (path string, err error) {
	out, err := os.CreateTemp(destDir, ".runwisp.new-*")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = out.Close()
		if err != nil {
			_ = os.Remove(out.Name())
		}
	}()
	if _, err = io.Copy(out, io.LimitReader(tr, maxTarballBytes)); err != nil {
		return "", fmt.Errorf("extract binary: %w", err)
	}
	if err = out.Chmod(0o755); err != nil {
		return "", err
	}
	if err = out.Sync(); err != nil {
		return "", err
	}
	return out.Name(), nil
}

// smokeTest runs `<candidate> --version` and requires it to exit 0 and report
// the version we think we downloaded. This is the last line of defense: a
// corrupt, truncated, or wrong-architecture binary fails here, before it can
// ever replace the live one.
func smokeTest(ctx context.Context, bin, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("candidate binary failed to run: %w (output: %q)", err, string(out))
	}
	want := strings.TrimPrefix(tag, "v")
	if !strings.Contains(string(out), want) {
		return fmt.Errorf("candidate binary reported the wrong version (wanted %s): %q", want, string(out))
	}
	return nil
}

// swapBinary atomically replaces exe with candidate, keeping a .bak rollback.
// On Unix, renaming over a running executable is safe: the running process keeps
// the old inode while new starts get the new one.
func swapBinary(exe, candidate, backup string) error {
	_ = os.Remove(backup) // clear a stale backup from a previous update
	if err := os.Rename(exe, backup); err != nil {
		return fmt.Errorf("back up current binary: %w", err)
	}
	if err := os.Rename(candidate, exe); err != nil {
		if rbErr := os.Rename(backup, exe); rbErr != nil {
			return fmt.Errorf("install new binary: %w (ROLLBACK FAILED: %v — restore %s manually)", err, rbErr, backup)
		}
		return fmt.Errorf("install new binary: %w (rolled back)", err)
	}
	slog.Info("swapped in updated binary", "path", exe, "backup", backup)
	return nil
}
