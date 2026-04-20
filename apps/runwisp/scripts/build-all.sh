#!/usr/bin/env bash
# Cross-compile runwisp for all supported platforms.
#
# Output: dist/<target>/runwisp  (e.g. dist/linux-x64/runwisp)
#
# Environment variables:
#   TARGETS  – space-separated list of targets to build (default: all)
#              valid targets: linux-x64 linux-arm64 darwin-x64 darwin-arm64
#   RELEASE  – set to "1" to strip debug info (-s -w)

set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "${script_dir}/.."

# shellcheck source=./metadata.sh
source "${script_dir}/metadata.sh"

ALL_TARGETS="linux-x64 linux-arm64 darwin-x64 darwin-arm64"
targets=${TARGETS:-${ALL_TARGETS}}

resolve_target() {
  local target=$1
  case "${target}" in
    linux-x64)    echo "linux amd64" ;;
    linux-arm64)  echo "linux arm64" ;;
    darwin-x64)   echo "darwin amd64" ;;
    darwin-arm64) echo "darwin arm64" ;;
    *) printf 'Unknown target: %s\n' "${target}" >&2; exit 1 ;;
  esac
}

ldflags=$(runner_ldflags)

for target in ${targets}; do
  read -r goos goarch <<< "$(resolve_target "${target}")"

  outdir="dist/${target}"
  mkdir -p "${outdir}"

  printf '  Building %s/%s → %s/runwisp\n' "${goos}" "${goarch}" "${outdir}"
  GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 \
    go build -ldflags "${ldflags}" -o "${outdir}/runwisp" ./cmd/runwisp
done

printf 'Done. Binaries in dist/\n'
