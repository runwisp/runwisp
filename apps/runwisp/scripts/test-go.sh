#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: Apache-2.0
# Go tests with coverage. E2E tests spawn real binaries that write coverage
# into RUNWISP_E2E_COVDIR; merge-coverage.sh merges it with the unit profile
# into coverage.out.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "${script_dir}/.."

# shellcheck source=./ensure-ui-dist.sh
source "${script_dir}/ensure-ui-dist.sh"
ensure_real_ui_dist

covdir="$(pwd)/.gocoverdir"
rm -rf "${covdir}"
mkdir -p "${covdir}"

RUNWISP_E2E_COVDIR="${covdir}" go test -covermode=atomic -coverpkg=./... \
  -coverprofile=.coverage_unit.out ./...

go tool covdata textfmt -i "${covdir}" -o .coverage_e2e.out 2>/dev/null || true
cat .coverage_unit.out >.coverage_raw.out
grep -v '^mode:' .coverage_e2e.out >>.coverage_raw.out 2>/dev/null || true
./scripts/merge-coverage.sh
rm -f .coverage_unit.out .coverage_e2e.out .coverage_raw.out
rm -rf "${covdir}"
