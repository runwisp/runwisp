// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

// ReleasesURL is where release assets live, the same place get.runwisp.com
// downloads from.
const ReleasesURL = "https://github.com/runwisp/runwisp/releases"

// maxArchiveSize bounds a release download held in memory; real archives are
// a fraction of this.
const maxArchiveSize = 256 << 20

// Method decides how to upgrade a binary installed via source (InstallSource)
// that lives at exe (Executable). A non-nil argv is the package-manager command
// that upgrades it; a non-empty refusal explains why `runwisp upgrade` can't;
// both empty means the binary is ours to replace in place (SelfReplace).
//
// npm-family installs are told apart by path rather than recorded at install
// time: bun and pnpm skip lifecycle scripts by default, so the npm wrapper has
// no install hook, but each manager's global tree has a fixed shape.
func Method(source, exe string) (argv []string, refusal string) {
	p := filepath.ToSlash(exe)
	switch source {
	case "homebrew":
		return []string{"brew", "upgrade", "runwisp"}, ""
	case "docker":
		return nil, "This RunWisp runs from the Docker image. Pull the new image and recreate the container:\n\n  docker compose pull && docker compose up -d"
	case "npx":
		return nil, "npx runs whichever version you ask for. Use:\n\n  npx runwisp@latest"
	case "npm":
		switch {
		case strings.Contains(p, "/.bun/install/global/"):
			return []string{"bun", "add", "-g", "runwisp@latest"}, ""
		case strings.Contains(p, "/pnpm/global/"):
			return []string{"pnpm", "add", "-g", "runwisp@latest"}, ""
		case strings.Contains(p, "/yarn/global/"):
			return []string{"yarn", "global", "add", "runwisp@latest"}, ""
		case strings.Contains(p, "/lib/node_modules/"):
			return []string{"npm", "install", "-g", "runwisp@latest"}, ""
		}
		project, _, _ := strings.Cut(p, "/node_modules/")
		return nil, fmt.Sprintf("RunWisp is a dependency of the project in %s. Update it there with your package manager, e.g.:\n\n  npm install runwisp@latest", project)
	}
	return nil, ""
}

// LatestVersion returns the tag of the newest stable release (e.g. "v1.6.0").
// GitHub answers /releases/latest with a redirect to /releases/tag/<tag> and
// never points it at a draft or prerelease.
func LatestVersion(ctx context.Context, client *http.Client, releasesURL string) (string, error) {
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, releasesURL+"/latest", nil)
	if err != nil {
		return "", err
	}
	res, err := noFollow.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()

	_, tag, ok := strings.Cut(res.Header.Get("Location"), "/releases/tag/")
	if !ok || !semver.IsValid(normalize(tag)) {
		return "", fmt.Errorf("can't tell the latest release from %s/latest (%s)", releasesURL, res.Status)
	}
	return tag, nil
}

// SelfReplace downloads the release archive for tag and goos/goarch, verifies
// it against the release's checksums-sha256.txt, and atomically swaps the
// runwisp binary inside it over exe. On any error exe is left untouched. A
// running daemon keeps executing its old inode until it restarts.
func SelfReplace(ctx context.Context, client *http.Client, releasesURL, tag, goos, goarch, exe string) error {
	asset, err := assetName(goos, goarch)
	if err != nil {
		return err
	}
	base := releasesURL + "/download/" + tag + "/"
	sums, err := fetch(ctx, client, base+"checksums-sha256.txt")
	if err != nil {
		return err
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}
	archive, err := fetch(ctx, client, base+asset)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s: refusing to install it", asset)
	}
	bin, err := extractBinary(archive)
	if err != nil {
		return fmt.Errorf("%s: %w", asset, err)
	}
	return replaceFile(exe, bin)
}

// assetName maps Go's platform names onto the release archive naming
// (runwisp-linux-x64.tar.gz, …).
func assetName(goos, goarch string) (string, error) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if (goos != "linux" && goos != "darwin") || arch == "" {
		return "", fmt.Errorf("no release build for %s/%s", goos, goarch)
	}
	return "runwisp-" + goos + "-" + arch + ".tar.gz", nil
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", path.Base(url), res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, maxArchiveSize))
}

// checksumFor finds asset's hash in `sha256sum` output ("<hex>  <file>").
func checksumFor(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in checksums-sha256.txt", asset)
}

// extractBinary returns the "runwisp" member of a release .tar.gz.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("archive has no runwisp binary")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && hdr.Name == "runwisp" {
			return io.ReadAll(io.LimitReader(tr, maxArchiveSize))
		}
	}
}

// replaceFile writes data next to dst and renames it over dst, so dst is
// either the old binary or the complete new one, never a partial write.
func replaceFile(dst string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".runwisp-upgrade-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
