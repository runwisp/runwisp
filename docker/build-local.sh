#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Builds the runwisp Docker image. Arranges docker/context/<arch>/runwisp
# (from a prebuilt dist/ dir, or by cross-compiling from source via
# apps/runwisp/scripts/build-all.sh) and invokes `docker buildx build`.
#
# The PR smoke-test job in ci.yml drives this script. The release publish job
# does NOT: it needs a multi-platform push with layer caching, so it lays the
# same context out itself and hands it to docker/build-push-action. The
# `docker/context/<amd64|arm64>/runwisp` layout below is therefore a contract
# shared with .github/workflows/release.yml — change one, change both.
#
# Usage:
#   docker/build-local.sh [options]
#
# Options:
#   --base alpine|debian     base image to build (default: alpine)
#   --platform <list>        comma-separated platforms (default: linux/amd64)
#   --dist <dir>             reuse an existing dist/ dir (dist/linux-x64/runwisp,
#                            dist/linux-arm64/runwisp) instead of building from source
#   --version <version>      OCI version label (default: apps/runwisp/scripts/metadata.sh version)
#   -t, --tag <tag>          image tag, e.g. runwisp:dev (repeatable)
#   --load                   load the result into the local docker daemon (single-platform only)
#   --push                   push the result to the registry
#
# Examples:
#   docker/build-local.sh --base alpine --load -t runwisp:dev
#   docker/build-local.sh --base alpine --platform linux/amd64,linux/arm64 --dist apps/runwisp/dist --push -t runwisp/runwisp:latest

set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "${script_dir}/.." && pwd)

base="alpine"
platform="linux/amd64"
dist_dir=""
version=""
tags=()
load=0
push=0

while [[ $# -gt 0 ]]; do
	case "$1" in
	--base)
		base="$2"
		shift 2
		;;
	--platform)
		platform="$2"
		shift 2
		;;
	--dist)
		dist_dir="$2"
		shift 2
		;;
	--version)
		version="$2"
		shift 2
		;;
	-t | --tag)
		tags+=("$2")
		shift 2
		;;
	--load)
		load=1
		shift
		;;
	--push)
		push=1
		shift
		;;
	*)
		printf 'Unknown option: %s\n' "$1" >&2
		exit 2
		;;
	esac
done

case "${base}" in
alpine | debian) ;;
*)
	printf 'Unknown --base %s (must be alpine or debian)\n' "${base}" >&2
	exit 2
	;;
esac

# An untagged build produces an image you can't refer to afterwards, so require
# a tag rather than building one. This also sidesteps bash 3.2 (still the system
# bash on macOS), where expanding an empty array under `set -u` is an error.
if [[ ${#tags[@]} -eq 0 ]]; then
	printf 'At least one -t/--tag is required, e.g. -t runwisp:dev\n' >&2
	exit 2
fi

if [[ -z "${version}" ]]; then
	version=$("${repo_root}/apps/runwisp/scripts/metadata.sh" version)
fi

# Map each requested platform to the TARGETARCH/dist-target pair it needs, and
# arrange docker/context/<TARGETARCH>/runwisp for it.
context_dir="${script_dir}/context"
rm -rf "${context_dir}"
mkdir -p "${context_dir}"

IFS=',' read -ra platforms <<<"${platform}"
build_targets=()
for p in "${platforms[@]}"; do
	case "${p}" in
	linux/amd64)
		targetarch="amd64"
		dist_target="linux-x64"
		;;
	linux/arm64)
		targetarch="arm64"
		dist_target="linux-arm64"
		;;
	*)
		printf 'Unsupported platform: %s (must be linux/amd64 or linux/arm64)\n' "${p}" >&2
		exit 2
		;;
	esac
	build_targets+=("${dist_target}")
	mkdir -p "${context_dir}/${targetarch}"

	if [[ -n "${dist_dir}" ]]; then
		src="${dist_dir}/${dist_target}/runwisp"
		if [[ ! -f "${src}" ]]; then
			printf 'Expected prebuilt binary at %s\n' "${src}" >&2
			exit 1
		fi
		cp "${src}" "${context_dir}/${targetarch}/runwisp"
	fi
done

if [[ -z "${dist_dir}" ]]; then
	printf 'No --dist given; cross-compiling from source (RELEASE=1)...\n'
	(
		cd "${repo_root}/apps/runwisp"
		TARGETS="${build_targets[*]}" RELEASE=1 ./scripts/build-all.sh
	)
	for i in "${!platforms[@]}"; do
		case "${platforms[$i]}" in
		linux/amd64) targetarch="amd64"; dist_target="linux-x64" ;;
		linux/arm64) targetarch="arm64"; dist_target="linux-arm64" ;;
		esac
		cp "${repo_root}/apps/runwisp/dist/${dist_target}/runwisp" "${context_dir}/${targetarch}/runwisp"
	done
fi

build_args=(
	--build-arg "BASE=${base}"
	--build-arg "VERSION=${version}"
	--platform "${platform}"
	-f "${script_dir}/Dockerfile"
)

for tag in "${tags[@]}"; do
	build_args+=(-t "${tag}")
done

if [[ "${load}" -eq 1 ]]; then
	build_args+=(--load)
fi
if [[ "${push}" -eq 1 ]]; then
	build_args+=(--push)
fi

printf 'Building %s image for %s (version %s)...\n' "${base}" "${platform}" "${version}"
docker buildx build "${build_args[@]}" "${script_dir}"
