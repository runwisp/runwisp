#!/usr/bin/env bash
# Source this file or invoke it directly (./metadata.sh version|ldflags).
# When sourced, exposes runner_version / runner_ldflags as functions.

metadata_script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

runner_version() {
  local changelog version
  changelog="${metadata_script_dir}/../../../CHANGELOG.md"
  version=$(awk '
    /^## \[Unreleased\]/ { next }
    /^## \[[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9.+-]*\]/ {
      v = $0
      sub(/^## \[/, "", v)
      sub(/\].*$/, "", v)
      print v
      exit
    }
  ' "${changelog}")
  if [[ -z "${version}" ]]; then
    printf 'metadata.sh: no released version found in %s\n' "${changelog}" >&2
    return 1
  fi
  printf '%s' "${version}"
}

runner_ldflags() {
  local module version flags
  module=$(go list -m -f '{{.Path}}')
  version=$(runner_version)
  flags="-X ${module}/internal/version.Version=${version}"
  if [[ "${RELEASE:-}" == "1" ]]; then
    flags="-s -w ${flags}"
  fi
  printf '%s' "${flags}"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  set -euo pipefail
  case "${1:-version}" in
    version) runner_version ;;
    ldflags) runner_ldflags ;;
    *) printf 'usage: metadata.sh [version|ldflags]\n' >&2; exit 2 ;;
  esac
fi
