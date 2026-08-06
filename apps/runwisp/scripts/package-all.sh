#!/usr/bin/env bash
# Package cross-compiled binaries (from build-all.sh) into release tarballs.
#
# Usage: package-all.sh <dist-dir> [out-dir]
#
# For each <dist-dir>/<target>/runwisp it writes
# <out-dir>/runwisp-<target>.tar.gz with the `runwisp` member forced to mode
# 0755, then verifies every archive actually contains an executable member.
#
# Why force the mode: the release pipeline round-trips the binaries through
# actions/upload-artifact, which does not preserve the Unix executable bit, so
# the downloaded binary is mode 0644. Packaging that as-is ships a tarball whose
# extracted `runwisp` is not runnable — the documented `tar -xzf … && mv`
# install then fails with "permission denied". The final verification pass is a
# release-time guard so a future regression fails the build instead of the user.

set -euo pipefail

dist_dir=${1:?usage: package-all.sh <dist-dir> [out-dir]}
out_dir=${2:-.}
mkdir -p "${out_dir}"

shopt -s nullglob
archives=()
for dir in "${dist_dir}"/*/; do
  target=$(basename "${dir}")
  binary="${dir}runwisp"
  if [[ ! -f "${binary}" ]]; then
    printf 'No runwisp binary in %s\n' "${dir}" >&2
    exit 1
  fi
  chmod 0755 "${binary}"
  archive="${out_dir}/runwisp-${target}.tar.gz"
  printf '  Packaging %s → %s\n' "${binary}" "${archive}"
  tar czf "${archive}" -C "${dir}" runwisp
  archives+=("${archive}")
done

if [[ ${#archives[@]} -eq 0 ]]; then
  printf 'No target directories found under %s\n' "${dist_dir}" >&2
  exit 1
fi

# Independently confirm every packaged archive carries an executable member; a
# release must never ship a runwisp the operator then has to chmod by hand.
status=0
for archive in "${archives[@]}"; do
  mode=$(tar tzvf "${archive}" | awk '$NF == "runwisp" { print $1 }')
  printf '  %s  %s\n' "${mode:-<missing>}" "${archive}"
  case "${mode}" in
    -rwxr-xr-x*) ;;
    *)
      printf 'ERROR: %s packages runwisp as %s, expected -rwxr-xr-x\n' \
        "${archive}" "${mode:-<missing>}" >&2
      status=1
      ;;
  esac
done

exit "${status}"
