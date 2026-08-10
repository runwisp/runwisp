#!/bin/sh
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Offline tests for docker-entrypoint.sh. No Docker, no network, no runwisp
# binary — a stub named `runwisp` goes first on PATH and prints the argv it was
# handed, so we can assert both the gate decisions and the exact command that
# would have been exec'd.
#
# Set ENTRYPOINT_SH to exercise the entrypoint under each shell the image might
# use — that is the shell under test, not the one running this file:
#   docker/tests/entrypoint_test.sh
#   ENTRYPOINT_SH=dash        docker/tests/entrypoint_test.sh
#   ENTRYPOINT_SH="busybox sh" docker/tests/entrypoint_test.sh
#
# Not part of the image: docker/.dockerignore excludes everything except
# `context` and `docker-entrypoint.sh`, so this directory never enters the
# build context.
set -eu

# shellcheck disable=SC1007 # `CDPATH= cd` clears CDPATH for this command only
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
entrypoint="$here/../docker-entrypoint.sh"
[ -f "$entrypoint" ] || { echo "cannot find $entrypoint" >&2; exit 2; }

# The shell the entrypoint itself runs under. Alpine gives it busybox ash,
# Debian gives it dash, and the two disagree often enough to be worth pinning.
ENTRYPOINT_SH=${ENTRYPOINT_SH:-sh}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

# Stub binary: echoes its own argv so we can assert on the exec'd command.
mkdir -p "$work/bin"
printf '#!/bin/sh\necho "ARGV: runwisp $*"\n' >"$work/bin/runwisp"
chmod +x "$work/bin/runwisp"
PATH="$work/bin:$PATH"
export PATH

# A real config file, and a directory where a config file should be.
good_config="$work/runwisp.toml"
printf '[tasks.hello]\nrun = "echo hi"\n' >"$good_config"
config_dir="$work/asdir"
mkdir -p "$config_dir"

passed=0
failed=0

# expect <name> <want_exit> <want_substring> -- <env assignments...> -- <argv...>
#
# want_substring is matched against combined stdout+stderr. Use "" to skip the
# text check. Env assignments are applied only to this case.
expect() {
	name=$1 want_exit=$2 want_text=$3
	shift 3
	sep=${1:-}
	[ "$sep" = "--" ] && shift

	env_args=""
	while [ "$#" -gt 0 ]; do
		arg=$1
		[ "$arg" = "--" ] && break
		env_args="$env_args $arg"
		shift
	done
	[ "${1:-}" = "--" ] && shift

	# `env -i` would drop PATH; instead start from a clean slate of the vars
	# the entrypoint reads, then apply the case's own assignments.
	set +e
	out=$(
		unset RUNWISP_PASSWORD RUNWISP_NO_AUTH RUNWISP_CONFIG RUNWISP_DATA RUNWISP_SOCKET
		# shellcheck disable=SC2086 # deliberate word splitting of the env list
		eval export $env_args 2>/dev/null || true
		# shellcheck disable=SC2086 # ENTRYPOINT_SH may be "busybox sh"
		$ENTRYPOINT_SH "$entrypoint" "$@" 2>&1
	)
	got_exit=$?
	set -e

	ok=1
	[ "$got_exit" = "$want_exit" ] || ok=""
	if [ -n "$want_text" ]; then
		case "$out" in
		*"$want_text"*) : ;;
		*) ok="" ;;
		esac
	fi

	if [ -n "$ok" ]; then
		passed=$((passed + 1))
	else
		failed=$((failed + 1))
		printf 'FAIL %s\n' "$name"
		printf '     argv:        %s\n' "$*"
		printf '     env:        %s\n' "$env_args"
		printf '     want exit:   %s   got: %s\n' "$want_exit" "$got_exit"
		[ -n "$want_text" ] && printf '     want text:   %s\n' "$want_text"
		printf '     output:      %s\n' "$out"
	fi
}

auth="RUNWISP_NO_AUTH=1"
cfg="RUNWISP_CONFIG=$good_config"
data="RUNWISP_DATA=$work/data"

# ---------------------------------------------------------------------------
# The auth gate. Every case here reaches the daemon; before the entrypoint
# resolved the subcommand properly, most of them slipped through ungated and
# booted a daemon whose random in-memory password nobody could ever use.
# ---------------------------------------------------------------------------
for argv in \
	"runwisp daemon" \
	"daemon" \
	"runwisp cloud" \
	"runwisp restart" \
	"runwisp demo" \
	"runwisp"; do
	# shellcheck disable=SC2086 # argv is a deliberate word-split list
	expect "auth gate: $argv" 1 "explicit auth setting" -- "$cfg" -- $argv
done

expect "auth gate: --config <path> daemon (was a bypass)" 1 "explicit auth setting" \
	-- "$cfg" -- runwisp --config "$good_config" daemon
expect "auth gate: --config=<path> daemon (was a bypass)" 1 "explicit auth setting" \
	-- "$cfg" -- runwisp --config="$good_config" daemon
expect "auth gate: -c <path> daemon (was a bypass)" 1 "explicit auth setting" \
	-- "$cfg" -- runwisp -c "$good_config" daemon
expect "auth gate: --log-level debug daemon (was a bypass)" 1 "explicit auth setting" \
	-- "$cfg" -- runwisp --log-level debug daemon
expect "auth gate: bare flags imply daemon" 1 "explicit auth setting" \
	-- "$cfg" -- --host 0.0.0.0
expect "auth gate: no argv at all" 1 "explicit auth setting" \
	-- "$cfg" --
expect "auth gate: fails closed on an unknown subcommand" 1 "explicit auth setting" \
	-- "$cfg" -- runwisp somefuturecommand

# ---------------------------------------------------------------------------
# Config checks, with auth satisfied.
# ---------------------------------------------------------------------------
expect "config: missing file" 1 "no runwisp.toml found" \
	-- "$auth" "RUNWISP_CONFIG=$work/nope.toml" -- runwisp daemon
expect "config: a directory gets its own message" 1 "is a directory" \
	-- "$auth" "RUNWISP_CONFIG=$config_dir" -- runwisp daemon
expect "config: directory message suggests include" 1 'include = ["conf.d/*.toml"]' \
	-- "$auth" "RUNWISP_CONFIG=$config_dir" -- runwisp daemon
expect "config: empty RUNWISP_CONFIG falls back to the image default" 1 "/etc/runwisp/runwisp.toml" \
	-- "$auth" "RUNWISP_CONFIG=" -- runwisp daemon
expect "config: unset RUNWISP_CONFIG falls back to the image default" 1 "/etc/runwisp/runwisp.toml" \
	-- "$auth" -- runwisp daemon
expect "config: stray positional after daemon" 1 'unexpected argument "daemon"' \
	-- "$auth" "$cfg" -- --config "$good_config" daemon

# ---------------------------------------------------------------------------
# Ungated pass-through. The argv assertions are the point: these must reach the
# binary byte-for-byte, and must not be rewritten into `runwisp daemon ...`.
# ---------------------------------------------------------------------------
expect "passthrough: validate needs no password" 0 "ARGV: runwisp validate" \
	-- -- runwisp validate
expect "passthrough: bare subcommand gets the runwisp prefix" 0 "ARGV: runwisp list" \
	-- -- list
expect "passthrough: a task named 'daemon' is not the daemon" 0 "ARGV: runwisp exec daemon" \
	-- -- runwisp exec daemon
expect "passthrough: --data value named 'daemon' is not the subcommand" 0 "ARGV: runwisp --data daemon list" \
	-- -- runwisp --data daemon list
expect "passthrough: --version is root's, not the daemon's" 0 "ARGV: runwisp --version" \
	-- -- --version
expect "passthrough: -v is root's, not the daemon's" 0 "ARGV: runwisp -v" \
	-- -- -v
expect "passthrough: --help is root's, not the daemon's" 0 "ARGV: runwisp --help" \
	-- -- --help

# ---------------------------------------------------------------------------
# The happy daemon path, and the healthcheck warning.
# ---------------------------------------------------------------------------
expect "daemon: starts when auth and config are both satisfied" 0 "ARGV: runwisp daemon" \
	-- "$auth" "$cfg" "$data" -- runwisp daemon
expect "daemon: flags are preserved through the gate" 0 "ARGV: runwisp daemon --host 0.0.0.0" \
	-- "$auth" "$cfg" "$data" -- runwisp daemon --host 0.0.0.0
expect "daemon: RUNWISP_PASSWORD also satisfies the gate" 0 "ARGV: runwisp daemon" \
	-- "RUNWISP_PASSWORD=hunter2" "$cfg" "$data" -- runwisp daemon
expect "daemon: --data warns about the healthcheck" 0 "HEALTHCHECK" \
	-- "$auth" "$cfg" "$data" -- runwisp daemon --data "$work/other"

printf '\n%s passed, %s failed\n' "$passed" "$failed"
[ "$failed" -eq 0 ]
