#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
# Install pinned golangci-lint into <repo>/.bin if needed, then run it.
# Mirrors scripts/sqlc-generate.sh so the version is single-sourced and the
# tool stays out of go.mod (its dependency tree is enormous).
set -euo pipefail

GOLANGCI_VERSION="v2.12.2"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "${script_dir}/.."

# Ensure the embedded UI dist directory exists so the //go:embed directive
# resolves during type-checking, same as check-go.sh.
# shellcheck source=./ensure-ui-dist.sh
source "${script_dir}/ensure-ui-dist.sh"
ensure_real_ui_dist

bin_dir=$(cd "${script_dir}/../../.." && pwd)/.bin
golangci="${bin_dir}/golangci-lint"

installed_version=""
if [[ -x "${golangci}" ]]; then
  installed_version=$("${golangci}" version --short 2>/dev/null || true)
fi

if [[ "${installed_version}" != "${GOLANGCI_VERSION#v}" ]]; then
  mkdir -p "${bin_dir}"
  GOBIN="${bin_dir}" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION}"
fi

"${golangci}" run "$@"
