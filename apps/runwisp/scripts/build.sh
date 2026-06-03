#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# shellcheck source=./ensure-ui-dist.sh
source "${script_dir}/ensure-ui-dist.sh"
ensure_real_ui_dist

# shellcheck source=./metadata.sh
source "${script_dir}/metadata.sh"

# A git worktree (e.g. Cline's) symlinks the gitignored binary back to the main
# checkout; building through it would clobber that binary or fail with "text
# file busy" if it's running. Drop the link so the build writes a fresh local one.
if [[ -L runwisp ]]; then
  rm -f runwisp
fi

CGO_ENABLED=0 go build -ldflags "$(runner_ldflags)" -o runwisp ./cmd/runwisp
