#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Offline tests for package-all.sh. No Go, no network — a fake dist tree stands
# in for build-all.sh output so we can assert the packaged tarball carries an
# executable `runwisp` member regardless of the on-disk mode it was handed.
#
# The regression under guard: actions/upload-artifact strips the Unix
# executable bit, so the release job packages a mode-0644 binary. v0.14.0
# shipped four tarballs whose extracted `runwisp` was not runnable.
set -euo pipefail

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
script="${here}/../package-all.sh"
[[ -f "${script}" ]] || { echo "cannot find ${script}" >&2; exit 2; }

passed=0
failed=0

# Mode of the `runwisp` member inside a tarball, e.g. "-rwxr-xr-x".
member_mode() {
  tar tzvf "$1" | awk '$NF == "runwisp" { print $1 }'
}

pass() { passed=$((passed + 1)); printf 'ok   - %s\n' "$1"; }
fail() { failed=$((failed + 1)); printf 'FAIL - %s\n' "$1" >&2; }

# --- packs a 0644 binary as an executable member (the actual v0.14.0 bug) ---
work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT
mkdir -p "${work}/dist/linux-x64" "${work}/dist/darwin-arm64" "${work}/out"
for t in linux-x64 darwin-arm64; do
  printf '#!/bin/sh\necho hi\n' > "${work}/dist/${t}/runwisp"
  chmod 0644 "${work}/dist/${t}/runwisp" # as returned by download-artifact
done

if "${script}" "${work}/dist" "${work}/out" >/dev/null; then
  pass "package-all.sh exits 0 on a valid dist tree"
else
  fail "package-all.sh exits 0 on a valid dist tree"
fi

for t in linux-x64 darwin-arm64; do
  mode=$(member_mode "${work}/out/runwisp-${t}.tar.gz")
  case "${mode}" in
    -rwxr-xr-x*) pass "runwisp-${t}.tar.gz member is executable (${mode})" ;;
    *) fail "runwisp-${t}.tar.gz member is ${mode:-<missing>}, want -rwxr-xr-x" ;;
  esac
done

# --- fails loudly when a target directory has no binary ---
empty=$(mktemp -d)
mkdir -p "${empty}/dist/linux-x64"
if "${script}" "${empty}/dist" "${empty}/out" >/dev/null 2>&1; then
  fail "package-all.sh should reject a target dir with no runwisp binary"
else
  pass "package-all.sh rejects a target dir with no runwisp binary"
fi
rm -rf "${empty}"

# --- fails loudly when there are no targets at all ---
none=$(mktemp -d)
mkdir -p "${none}/dist"
if "${script}" "${none}/dist" "${none}/out" >/dev/null 2>&1; then
  fail "package-all.sh should reject an empty dist dir"
else
  pass "package-all.sh rejects an empty dist dir"
fi
rm -rf "${none}"

printf '\n%d passed, %d failed\n' "${passed}" "${failed}"
[[ "${failed}" -eq 0 ]]
