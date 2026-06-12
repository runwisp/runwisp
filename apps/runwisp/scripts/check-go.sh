#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# Ensure the embedded UI dist directory is a real, non-empty directory so go vet
# can resolve the //go:embed directive without a full UI build.
# shellcheck source=./ensure-ui-dist.sh
source "${script_dir}/ensure-ui-dist.sh"
ensure_real_ui_dist

go vet ./...

# Focused golangci-lint pass (pinned, installed on demand) mirroring the
# SonarCloud rules we enforce — see .golangci.yml. Runs here so it gates
# `bun run ci` / `moon run runwisp:check`, not just the cloud scan.
"${script_dir}/lint-go.sh" ./...

unformatted_files=$(gofmt -l .)
if [[ -n "${unformatted_files}" ]]; then
  printf 'These Go files need gofmt:\n%s\n' "${unformatted_files}" >&2
  exit 1
fi
