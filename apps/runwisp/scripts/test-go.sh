#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
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

# Run the unit suite under the race detector: several fixes are data-race /
# shutdown-timing hardening that only a -race run can catch (-covermode=atomic
# makes coverage counters atomic; it does NOT enable the detector). The detector
# needs CGO and a C toolchain, so CGO is forced on rather than inherited: an
# ambient CGO_ENABLED=0 would otherwise make `go test -race` refuse to run.
# macOS skips it, so the CI Unit Tests job races on ubuntu-latest only. A scalar
# (not an array) keeps this safe under `set -u` on macOS's bash 3.2 when the
# flag is empty.
race_flag=-race
if [[ "$(uname -s)" == "Darwin" ]]; then
  race_flag=
else
  export CGO_ENABLED=1
fi

RUNWISP_E2E_COVDIR="${covdir}" go test ${race_flag} -covermode=atomic -coverpkg=./... \
  -coverprofile=.coverage_unit.out ./...

go tool covdata textfmt -i "${covdir}" -o .coverage_e2e.out 2>/dev/null || true
cat .coverage_unit.out >.coverage_raw.out
grep -v '^mode:' .coverage_e2e.out >>.coverage_raw.out 2>/dev/null || true
./scripts/merge-coverage.sh
rm -f .coverage_unit.out .coverage_e2e.out .coverage_raw.out
rm -rf "${covdir}"
