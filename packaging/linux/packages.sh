#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build the .deb and .rpm packages for every linux target in a build-all.sh
# dist tree.
#
# Usage: packages.sh <version> <dist-dir> <out-dir>
#   writes <out-dir>/runwisp-linux-{x64,arm64}.{deb,rpm}
#
# The names carry no version so releases/latest/download/ links stay stable.
# RPM_SIGNING_KEY, the path to the armored private signing key, signs the rpms.

set -euo pipefail

version=${1:?usage: packages.sh <version> <dist-dir> <out-dir>}
export RPM_SIGNING_KEY=${RPM_SIGNING_KEY:+$(realpath "${RPM_SIGNING_KEY}")}
dist_dir=$(realpath "${2:?usage: packages.sh <version> <dist-dir> <out-dir>}")
mkdir -p "${3:?usage: packages.sh <version> <dist-dir> <out-dir>}"
out_dir=$(realpath "$3")

nfpm=github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# nfpm resolves `src:` paths against the working directory and doesn't expand
# env vars there, so each binary is staged as ./runwisp beside the config.
stage=$(mktemp -d)
trap 'rm -rf "${stage}"' EXIT
cp "${here}"/nfpm.yaml "${here}"/runwisp.* "${here}"/*.runwisp-source "${stage}/"
cd "${stage}"

for target in linux-x64 linux-arm64; do
  binary="${dist_dir}/${target}/runwisp"
  [[ -f "${binary}" ]] || continue
  case "${target}" in
    linux-x64) arch=amd64 ;;
    linux-arm64) arch=arm64 ;;
  esac
  install -m 0755 "${binary}" runwisp
  for packager in deb rpm; do
    target_file="${out_dir}/runwisp-${target}.${packager}"
    printf '  Packaging %s → %s\n' "${binary}" "${target_file}"
    VERSION="${version}" ARCH="${arch}" go run "${nfpm}" package \
      --config nfpm.yaml --packager "${packager}" --target "${target_file}" >/dev/null
  done
done
