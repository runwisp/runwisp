#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Smoke-test a deployed apt/dnf repository the way a user meets it: in fresh
# Debian and Fedora containers, trust the committed public key, add the
# repository at <base-url> over HTTPS, install runwisp from it, and check the
# installed binary reports <version>.
#
# Usage: repo_smoke.sh <base-url> <version>
#   e.g. repo_smoke.sh https://pkg.runwisp.com 1.7.0
#
# Needs docker or podman and network access.

# The single-quoted scripts expand inside the containers, not here.
# shellcheck disable=SC2016
set -euo pipefail

usage='usage: repo_smoke.sh <base-url> <version>'
base=${1:?${usage}}
version=${2:?${usage}}
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
linux="${here}/.."
engine=${CONTAINER_ENGINE:-$(command -v docker || command -v podman)}

# A fresh deploy can take a moment to reach every edge.
for attempt in $(seq 1 30); do
  if curl -fsS "${base}/deb/dists/stable/main/binary-amd64/Packages" | grep -qx "Version: ${version//-/\~}"; then
    break
  fi
  if [[ ${attempt} == 30 ]]; then
    echo "${base} never served version ${version}" >&2
    exit 1
  fi
  sleep 10
done
curl -fsS "${base}/runwisp.asc" | cmp -s - "${linux}/runwisp.asc" ||
  { echo "${base}/runwisp.asc is not packaging/linux/runwisp.asc" >&2; exit 1; }
curl -fsS "${base}/" | grep -qF ">${version}<" ||
  { echo "${base}/ does not show version ${version}" >&2; exit 1; }

run() {
  local image=$1 script=$2
  "${engine}" run --rm -e BASE="${base}" -e VERSION="${version}" -v "${linux}:/linux:ro" "${image}" sh -euxc "${script}"
}

echo "--- Debian: install from ${base}/deb"
run debian:trixie-slim '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq && apt-get install -y -qq ca-certificates >/dev/null
  cp /linux/runwisp.asc /usr/share/keyrings/runwisp.asc
  sed "s|https://pkg.runwisp.com|${BASE}|" /linux/runwisp.sources >/etc/apt/sources.list.d/smoke.sources
  # The edge can still serve a stale Packages.gz after Packages is fresh.
  # The pool .deb can lag the same way, so retry the install along with the index.
  for attempt in 1 2 3 4 5 6; do
    apt-get update && apt-get install -y -o Dpkg::Options::=--force-confold runwisp && break
    [ "${attempt}" -lt 6 ]; sleep 10
  done
  test "$(cat /usr/lib/runwisp/.runwisp-source)" = deb
  runwisp --version | grep -F "${VERSION}"
'

echo "--- Fedora: install from ${base}/rpm"
run fedora:latest '
  install -D -m 0644 /linux/runwisp.asc /etc/pki/rpm-gpg/RPM-GPG-KEY-runwisp
  sed -e "s|https://pkg.runwisp.com|${BASE}|" -e "s|^\[runwisp\]|[runwisp-smoke]|" /linux/runwisp.repo \
    >/etc/yum.repos.d/smoke.repo
  dnf install -y --repo runwisp-smoke runwisp
  test "$(cat /usr/lib/runwisp/.runwisp-source)" = rpm
  runwisp --version | grep -F "${VERSION}"
'

echo "ok - ${base} serves a working repository for ${version}"
