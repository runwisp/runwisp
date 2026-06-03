#!/usr/bin/env bash
# Guarantee internal/ui/dist is a real, non-empty directory before any Go build
# that consumes the //go:embed all:dist directive in internal/ui/serve.go.
# Handles two failure modes:
#
#  1. Symlinked dist. Git worktrees (e.g. Cline's) symlink the gitignored dist/
#     back to the main checkout to skip the heavy Svelte build, but go:embed
#     rejects a symlinked root ("cannot embed irregular file dist"). Deleting
#     the link would serve a blank UI, so we replace it with a real directory
#     copied from the link target.
#
#  2. Missing/empty dist. A fresh checkout has nothing to embed and go:embed
#     needs at least one file, so we drop in a placeholder — enough for tooling
#     that doesn't need the real UI (openapi generation, go vet).
#
# A no-op once dist is real and populated, so it's safe and cheap to call from
# every Go entry point, including concurrently under moon's parallel pipeline.

ensure_real_ui_dist() {
  local lib_dir dist target
  lib_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
  dist="${lib_dir}/../internal/ui/dist"

  # Materialize a symlinked dist into a real directory, preserving its contents.
  if [[ -L "${dist}" ]]; then
    target=$(readlink -f "${dist}" 2>/dev/null || true)
    rm -f "${dist}"
    mkdir -p "${dist}"
    if [[ -n "${target}" && -d "${target}" ]]; then
      cp -R "${target}/." "${dist}/"
    fi
  fi

  # Guarantee the embed root exists and is non-empty.
  mkdir -p "${dist}"
  if [[ -z "$(ls -A "${dist}")" ]]; then
    echo '<!-- placeholder for go:embed -->' >"${dist}/index.html"
  fi

  return 0
}
