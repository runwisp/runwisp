#!/usr/bin/env bash
set -euo pipefail

source_dir="../ui/build"
target_dir="internal/ui/dist"

if [ ! -d "${source_dir}" ]; then
  printf 'Runner UI build output not found at %s\n' "${source_dir}" >&2
  exit 1
fi

rm -rf "${target_dir}"
mkdir -p "${target_dir}"
cp -R "${source_dir}/." "${target_dir}"
