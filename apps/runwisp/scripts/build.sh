#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# shellcheck source=./ensure-ui-dist.sh
source "${script_dir}/ensure-ui-dist.sh"
ensure_real_ui_dist

# shellcheck source=./metadata.sh
source "${script_dir}/metadata.sh"

# A git worktree (e.g. Cline's) symlinks the gitignored binary back to the main
# checkout. Building through that link would clobber the main checkout's binary
# — or, if it is currently running, fail with "text file busy". Drop the link so
# the build writes a fresh binary local to this checkout.
if [[ -L runwisp ]]; then
  rm -f runwisp
fi

CGO_ENABLED=0 go build -ldflags "$(runner_ldflags)" -o runwisp ./cmd/runwisp
