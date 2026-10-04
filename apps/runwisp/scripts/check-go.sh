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
# `bun run ci` / `moon run runwisp:check`, not just the SonarCloud scan.
"${script_dir}/lint-go.sh" ./...

# Fail on known vulnerabilities reachable from our code, in the standard
# library (the toolchain pinned in go.mod) or in a module. Pinned, and kept out
# of go.mod like golangci-lint; installed into <repo>/.bin so CI can cache it.
GOVULNCHECK_VERSION="v1.8.0"
bin_dir=$(cd "${script_dir}/../../.." && pwd)/.bin
govulncheck="${bin_dir}/govulncheck"
if [[ ! -x "${govulncheck}" || "$(go version -m "${govulncheck}" | awk '$1 == "mod" { print $3 }')" != "${GOVULNCHECK_VERSION}" ]]; then
  mkdir -p "${bin_dir}"
  GOBIN="${bin_dir}" go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
fi
"${govulncheck}" ./...

unformatted_files=$(gofmt -l .)
if [[ -n "${unformatted_files}" ]]; then
  printf 'These Go files need gofmt:\n%s\n' "${unformatted_files}" >&2
  exit 1
fi
