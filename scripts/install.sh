#!/bin/sh
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Installs the runwisp binary: resolve this platform, download the matching
# release tarball, verify its SHA-256 against the release's checksums file,
# and place it on PATH. That's the whole job.
#
# This script never reads a crontab, never writes runwisp.toml, and never
# calls systemctl/launchctl — every one of those decisions belongs to the
# binary itself, which is the only thing that can apply runwisp's trust
# gates. Run `runwisp` once you have it; see
# https://docs.runwisp.com/operations/autostart/ for autostart and
# https://docs.runwisp.com/coming-from/cron/ for taking over from cron.
#
# Usage:
#   curl -fsSL https://get.runwisp.com | sh
#   curl -fsSL https://get.runwisp.com | RUNWISP_VERSION=v0.13.0 sh
#
# Env vars:
#   RUNWISP_VERSION      release tag to install, e.g. v0.13.0 (default: latest)
#   RUNWISP_INSTALL_DIR  directory to install into (default: /usr/local/bin as
#                        root, otherwise ~/.local/bin)

set -eu

REPO="runwisp/runwisp"

log() {
  printf '%s\n' "$*" >&2
}

die() {
  log "error: $*"
  exit 1
}

detect_target() {
  os=$(uname -s)
  arch=$(uname -m)

  case "${os}" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) die "unsupported OS: ${os} (runwisp ships linux and darwin binaries only)" ;;
  esac

  case "${arch}" in
    x86_64 | amd64) arch="x64" ;;
    arm64 | aarch64) arch="arm64" ;;
    *) die "unsupported architecture: ${arch}" ;;
  esac

  printf '%s-%s\n' "${os}" "${arch}"
}

download() {
  url=$1
  dest=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "${dest}" "${url}"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "${dest}" "${url}"
  else
    die "neither curl nor wget is available"
  fi
}

sha256_of() {
  file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${file}" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${file}" | awk '{print $1}'
  else
    die "neither sha256sum nor shasum is available to verify the download"
  fi
}

install_dir() {
  if [ -n "${RUNWISP_INSTALL_DIR:-}" ]; then
    printf '%s\n' "${RUNWISP_INSTALL_DIR}"
  elif [ "$(id -u)" = "0" ]; then
    printf '/usr/local/bin\n'
  else
    printf '%s/.local/bin\n' "${HOME}"
  fi
}

main() {
  target=$(detect_target)
  version=${RUNWISP_VERSION:-latest}

  if [ "${version}" = "latest" ]; then
    base_url="https://github.com/${REPO}/releases/latest/download"
  else
    base_url="https://github.com/${REPO}/releases/download/${version}"
  fi

  asset="runwisp-${target}.tar.gz"
  tmpdir=$(mktemp -d)
  trap 'rm -rf "${tmpdir}"' EXIT

  log "Downloading ${asset} (${version})..."
  download "${base_url}/${asset}" "${tmpdir}/${asset}"
  download "${base_url}/checksums-sha256.txt" "${tmpdir}/checksums-sha256.txt"

  expected=$(grep " ${asset}\$" "${tmpdir}/checksums-sha256.txt" | awk '{print $1}')
  [ -n "${expected}" ] || die "no checksum entry for ${asset} in checksums-sha256.txt"

  actual=$(sha256_of "${tmpdir}/${asset}")
  [ "${expected}" = "${actual}" ] ||
    die "checksum mismatch for ${asset}: expected ${expected}, got ${actual}"

  tar -xzf "${tmpdir}/${asset}" -C "${tmpdir}" runwisp

  dir=$(install_dir)
  mkdir -p "${dir}"
  install -m 0755 "${tmpdir}/runwisp" "${dir}/runwisp"

  log "Installed runwisp to ${dir}/runwisp"

  case ":${PATH}:" in
    *":${dir}:"*) ;;
    *) log "Note: ${dir} is not on your PATH. Add it, then run 'runwisp' to get started." ;;
  esac
}

main "$@"
