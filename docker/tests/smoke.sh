#!/usr/bin/env bash
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Container-level smoke tests for a built runwisp image. Lives here rather than
# inline in ci.yml so the same checks run locally:
#
#   docker/build-local.sh --base alpine --dist apps/runwisp/dist --load -t runwisp:dev
#   docker/tests/smoke.sh runwisp:dev
#
# Complements docker/tests/entrypoint_test.sh, which covers argv/gate logic
# offline with a stubbed binary. This file exercises the real image: the real
# binary, the entrypoint under the image's own shell, and the HEALTHCHECK.
set -euo pipefail

image=${1:-}
[[ -n "$image" ]] || {
	printf 'usage: %s <image>\n' "$0" >&2
	exit 2
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "${script_dir}/../.." && pwd)

work=$(mktemp -d)
container=""
cleanup() {
	if [[ -n "$container" ]]; then
		docker rm -f "$container" >/dev/null 2>&1 || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

printf '[tasks.hello]\nrun = "echo hello-from-container"\n' >"$work/runwisp.toml"

passed=0
failed=0

pass() {
	local name="$1"
	passed=$((passed + 1))
	printf '  ok   %s\n' "${name}"
}
fail() {
	local name="$1" detail="${2:-}"
	failed=$((failed + 1))
	printf '  FAIL %s\n' "${name}"
	[[ $# -gt 1 ]] && printf '       %s\n' "${detail}"
}

# run_image <expected exit> <expected substring> <name> -- <docker run args...>
run_image() {
	local want_exit=$1 want_text=$2 name=$3
	shift 3
	[[ ${1:-} == "--" ]] && shift

	local out got
	set +e
	out=$(docker run --rm "$@" 2>&1)
	got=$?
	set -e

	if [[ $got -ne $want_exit ]]; then
		fail "$name" "want exit $want_exit, got $got: $out"
		return
	fi
	if [[ -n "$want_text" && "$out" != *"$want_text"* ]]; then
		fail "$name" "output missing '$want_text': $out"
		return
	fi
	pass "$name"
}

# wait_for_log <container> <substring> <seconds>: echo the container's logs once
# they contain substring, or fail after the timeout. Polling beats a fixed sleep
# — a slow CI runner shouldn't turn into a flaky assertion.
wait_for_log() {
	# Split declarations: bash 3.2 does not reliably expose a variable assigned
	# earlier in the same `local` statement to a later one on the same line.
	local name=$1 want=$2 timeout=$3
	local logs=""
	local deadline=$((SECONDS + timeout))
	while ((SECONDS < deadline)); do
		logs=$(docker logs "$name" 2>&1 || true)
		if [[ "$logs" == *"$want"* ]]; then
			printf '%s' "$logs"
			return 0
		fi
		sleep 1
	done
	printf '%s' "$logs"
	return 1
}

mount_cfg=(-v "$work/runwisp.toml:/etc/runwisp/runwisp.toml:ro")

echo "== the auth gate refuses every daemon-starting form =="
for argv in "" "daemon" "runwisp daemon" "runwisp cloud" "runwisp restart" "runwisp demo" "runwisp"; do
	# shellcheck disable=SC2086 # argv is a deliberate word-split list
	run_image 1 "explicit auth setting" "no auth: '${argv:-<no args>}'" \
		-- "${mount_cfg[@]}" "$image" $argv
done
run_image 1 "explicit auth setting" "no auth: flags before the subcommand" \
	-- "${mount_cfg[@]}" "$image" runwisp --log-level debug daemon
run_image 1 "explicit auth setting" "no auth: --config before the subcommand" \
	-- "${mount_cfg[@]}" "$image" runwisp --config /etc/runwisp/runwisp.toml daemon
run_image 1 "explicit auth setting" "no auth: unknown subcommand fails closed" \
	-- "${mount_cfg[@]}" "$image" runwisp somefuturecommand

echo "== the config check =="
run_image 1 "no runwisp.toml found" "missing config" \
	-- -e RUNWISP_NO_AUTH=1 "$image"
run_image 1 "is a directory" "config path is a directory" \
	-- -e RUNWISP_NO_AUTH=1 -v "$work:/etc/runwisp/runwisp.toml:ro" "$image"

echo "== one-shot subcommands bypass the gate =="
run_image 0 "" "validate needs no password" \
	-- "${mount_cfg[@]}" "$image" runwisp validate
run_image 0 "hello-from-container" "exec needs no password" \
	-- "${mount_cfg[@]}" "$image" runwisp exec hello
run_image 0 "" "--version is root's, not the daemon's" \
	-- "$image" --version

echo "== version identity =="
image_version=$(docker run --rm "$image" runwisp --version 2>&1 | tr -d '\r')
expected_version=$("$repo_root/apps/runwisp/scripts/metadata.sh" version)
if [[ "$image_version" == *"$expected_version"* ]]; then
	pass "binary reports $expected_version"
else
	fail "version identity" "image says '$image_version', repo says '$expected_version'"
fi

label_version=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$image")
if [[ -n "$label_version" ]]; then
	pass "OCI version label is set ($label_version)"
else
	fail "OCI version label" "org.opencontainers.image.version is empty"
fi

echo "== the ephemeral-password warning reaches docker logs =="
# --entrypoint deliberately skips the gate: this asserts the daemon's *own*
# headless warning, the backstop if the gate is ever bypassed another way.
ephemeral="runwisp-smoke-ephemeral-$$"
docker run -d --name "$ephemeral" --entrypoint runwisp "${mount_cfg[@]}" "$image" \
	daemon --host 127.0.0.1 >/dev/null
out=$(wait_for_log "$ephemeral" "no RUNWISP_PASSWORD set" 30 || true)
docker rm -f "$ephemeral" >/dev/null 2>&1 || true
if [[ "$out" == *"no RUNWISP_PASSWORD set"* ]]; then
	pass "headless boot warns about the generated password"
else
	fail "ephemeral password warning" "not found in: $out"
fi

echo "== a real daemon comes up and reports healthy =="
container=$(docker run -d --name "runwisp-smoke-$$" \
	-e RUNWISP_PASSWORD=smoke-test-password \
	"${mount_cfg[@]}" \
	--health-interval=2s --health-start-period=1s --health-retries=5 \
	"$image")

deadline=$((SECONDS + 60))
status=""
while ((SECONDS < deadline)); do
	status=$(docker inspect --format '{{ .State.Health.Status }}' "$container" 2>/dev/null || echo unknown)
	[[ "$status" == "healthy" || "$status" == "unhealthy" ]] && break
	sleep 1
done
if [[ "$status" == "healthy" ]]; then
	pass "HEALTHCHECK reports healthy"
else
	fail "HEALTHCHECK" "status=$status; logs: $(docker logs "$container" 2>&1 | tail -20)"
fi

if docker exec "$container" runwisp status >/dev/null 2>&1; then
	pass "runwisp status works over the control socket"
else
	fail "docker exec runwisp status" "$(docker logs "$container" 2>&1 | tail -20)"
fi

if docker exec "$container" runwisp exec hello 2>&1 | grep -q hello-from-container; then
	pass "docker exec runwisp exec runs a task"
else
	fail "docker exec runwisp exec hello"
fi

logs=$(docker logs "$container" 2>&1)
if [[ "$logs" != *"no RUNWISP_PASSWORD set"* ]]; then
	pass "an operator-supplied password produces no ephemeral warning"
else
	fail "unexpected ephemeral warning with RUNWISP_PASSWORD set"
fi

echo "== a compose-backed unit fails loudly (the image ships no docker CLI) =="
# The image deliberately has no docker binary, so [compose.*] can never run in
# it. What matters is that this surfaces as a visible failed run rather than a
# unit that silently never starts — so assert on the daemon actually saying so.
printf '[compose.stack]\nfile = "/etc/runwisp/compose.yaml"\n' >"$work/compose-cfg.toml"
printf 'services:\n  web:\n    image: nginx:alpine\n' >"$work/compose.yaml"
compose_ct="runwisp-smoke-compose-$$"
docker run -d --name "$compose_ct" \
	-e RUNWISP_PASSWORD=smoke-test-password \
	-v "$work/compose-cfg.toml:/etc/runwisp/runwisp.toml:ro" \
	-v "$work/compose.yaml:/etc/runwisp/compose.yaml:ro" \
	"$image" >/dev/null
daemon_log=$(wait_for_log "$compose_ct" "run failed" 45 || true)
# The daemon's stdout reports *that* the run failed; the reason lands in the
# run's own log, which is the surface the UI, TUI and REST all read. Check both,
# because "a unit that silently never starts" is the failure mode that matters.
run_log=$(docker exec "$compose_ct" sh -c 'cat /var/lib/runwisp/logs/*/*.log 2>/dev/null' || true)
docker rm -f "$compose_ct" >/dev/null 2>&1 || true

if [[ "$daemon_log" == *"run failed"* ]]; then
	pass "a compose unit with no docker CLI produces visible failed runs"
else
	fail "compose failure is invisible" "logs: $(printf '%s' "$daemon_log" | tail -20)"
fi
if [[ "$run_log" == *"compose unavailable"* || "$run_log" == *"install Docker"* ]]; then
	pass "the run log names the missing docker dependency"
else
	fail "compose run log does not explain itself" "run log: $(printf '%s' "$run_log" | tail -10)"
fi

printf '\n%s passed, %s failed\n' "$passed" "$failed"
((failed == 0))
