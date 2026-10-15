#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# End-to-end test for the .deb/.rpm packages and the apt/dnf repository: build
# packages 0.0.1 and 0.0.2 around a stub binary and publish 0.0.2 as a
# repository, all signed with a throwaway key. Then, in Debian and Fedora
# containers, install 0.0.1 and upgrade it with the exact command
# `runwisp upgrade` runs (internal/update/upgrade.go Method).
#
# Needs Go (for nfpm), gpg, and docker or podman. Pulls container images.

# The single-quoted scripts expand inside the containers, not here.
# shellcheck disable=SC2016
set -euo pipefail

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
linux="${here}/.."
engine=${CONTAINER_ENGINE:-$(command -v docker || command -v podman)}

work=$(mktemp -d)
chmod 0755 "${work}" # apt fetches file: repositories as the unprivileged _apt user
# Files written by a rootful docker are root-owned; a CI runner is ephemeral.
trap 'gpgconf --kill gpg-agent 2>/dev/null; rm -rf "${work}" 2>/dev/null || true' EXIT

for target in linux-x64 linux-arm64; do
  mkdir -p "${work}/dist/${target}"
  printf '#!/bin/sh\necho "runwisp stub"\n' >"${work}/dist/${target}/runwisp"
done
export GNUPGHOME="${work}/gnupg"
mkdir -m 0700 "${GNUPGHOME}"
gpg --batch --quiet --passphrase "" --quick-gen-key "RunWisp Test <test@invalid>" rsa3072 sign never
gpg --armor --export >"${work}/test.asc"
gpg --armor --export-secret-keys >"${work}/test.key"
export RPM_SIGNING_KEY="${work}/test.key"

"${linux}/packages.sh" 0.0.1 "${work}/dist" "${work}/old"
"${linux}/packages.sh" 0.0.2 "${work}/dist" "${work}/new"

run() {
  local image=$1 script=$2
  "${engine}" run --rm -v "${work}:/work" -v "${linux}:/linux:ro" "${image}" sh -euxc "${script}"
}

echo "--- building the repository"
run debian:trixie-slim '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq && apt-get install -y -qq apt-utils createrepo-c gnupg >/dev/null
  export GNUPGHOME=$(mktemp -d)
  gpg --batch --quiet --import /work/test.key
  /linux/repo.sh 0.0.2 /work/new /work/site
'

echo "--- Debian: install 0.0.1, upgrade from the repository"
run debian:trixie-slim '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq && apt-get install -y -qq /work/old/runwisp-linux-x64.deb >/dev/null
  test "$(cat /usr/lib/runwisp/.runwisp-source)" = deb
  runwisp | grep -q "runwisp stub"
  grep -q "^URIs: https://pkg.runwisp.com/deb$" /etc/apt/sources.list.d/runwisp.sources
  sed -i -e "s|^URIs:.*|URIs: file:/work/site/deb|" -e "s|^Signed-By:.*|Signed-By: /work/test.asc|" \
    /etc/apt/sources.list.d/runwisp.sources
  apt-get update && apt-get install --only-upgrade -y runwisp
  test "$(dpkg-query -W -f "\${Version}" runwisp)" = 0.0.2
  grep -q "^URIs: file:/work/site/deb$" /etc/apt/sources.list.d/runwisp.sources
'

echo "--- Fedora: install 0.0.1, upgrade from the repository"
run fedora:latest '
  dnf install -y -q /work/old/runwisp-linux-x64.rpm
  test "$(cat /usr/lib/runwisp/.runwisp-source)" = rpm
  runwisp | grep -q "runwisp stub"
  grep -q "^baseurl=https://pkg.runwisp.com/rpm$" /etc/yum.repos.d/runwisp.repo
  sed -i -e "s|^baseurl=.*|baseurl=file:///work/site/rpm|" -e "s|^gpgkey=.*|gpgkey=file:///work/test.asc|" \
    /etc/yum.repos.d/runwisp.repo
  dnf upgrade -y --refresh runwisp
  test "$(rpm -q --qf "%{VERSION}" runwisp)" = 0.0.2
'

echo "ok - packages install and upgrade from the signed repository"
