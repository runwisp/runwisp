#!/usr/bin/env bash
# Guarantee apps/runwisp/internal/ui/dist is a real, non-empty directory before
# any Go compilation that consumes the //go:embed all:dist directive in
# internal/ui/serve.go (go build / go run / go vet / go test).
#
# Two failure modes this prevents:
#
#  1. Symlinked dist. Git worktrees (e.g. Cline's) symlink the gitignored dist/
#     build artifact back to the main checkout so the heavy Svelte build isn't
#     repeated. Go's go:embed rejects a symlinked embed root with
#     "cannot embed irregular file dist", which breaks every Go build in the
#     worktree. We can't just delete the link — that would drop the already
#     built UI and serve a blank Web UI — so we replace it with a real directory
#     holding a copy of the link target's contents.
#
#  2. Missing/empty dist. A fresh checkout that has not run the UI build yet has
#     nothing to embed, and go:embed needs at least one file. We drop in a
#     placeholder so tooling that does not need the real UI (openapi generation,
#     go vet) still compiles.
#
# A no-op once dist is a real, populated directory, so it is safe (and cheap) to
# call from every Go entry point, including concurrently under make -j.

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

# When executed directly (not sourced), run the function. This lets Makefile
# recipes that invoke `go` without a wrapper script (e.g. the test target) use
# `./scripts/ensure-ui-dist.sh` as a guard.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  set -euo pipefail
  ensure_real_ui_dist
fi
