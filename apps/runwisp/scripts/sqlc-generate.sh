#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: Apache-2.0
# Install sqlc at a pinned version into <repo>/.bin (if missing or outdated)
# and regenerate the Go storage code declared in sqlc.yaml.
set -euo pipefail

SQLC_VERSION="v1.29.0"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "${script_dir}/.."

bin_dir=$(cd "${script_dir}/../../.." && pwd)/.bin
sqlc="${bin_dir}/sqlc"

if [[ ! -x "${sqlc}" || "$("${sqlc}" version 2>/dev/null)" != "${SQLC_VERSION}" ]]; then
  mkdir -p "${bin_dir}"
  GOBIN="${bin_dir}" go install "github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION}"
fi

"${sqlc}" generate
