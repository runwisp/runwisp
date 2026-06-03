#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: Apache-2.0
# Run the Go test suite with coverage. The e2e tests spawn real runwisp
# binaries that write binary coverage into RUNWISP_E2E_COVDIR (GOCOVERDIR);
# that profile is merged with the unit profile into coverage.out, keeping the
# max count per block (see merge-coverage.sh).
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
