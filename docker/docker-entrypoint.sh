#!/bin/sh
# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Container entrypoint. Normalizes the command so the container is easy to
# call several ways, then runs the auth/config sanity checks whenever the
# resolved command would bring a daemon up:
#   docker run runwisp/runwisp                      -> runwisp daemon
#   docker run runwisp/runwisp --host 0.0.0.0        -> runwisp daemon --host 0.0.0.0
#   docker run runwisp/runwisp runwisp validate ...  -> passes straight through
#   docker run runwisp/runwisp validate ...          -> runwisp validate ...
#   docker run runwisp/runwisp --version             -> root version, not daemon's
#
# POSIX sh only: this runs on busybox ash (alpine) and dash (debian). No
# arrays, no [[ ]], no ${var,,}.
set -eu

# Image ENV restated as defaults so the script stays correct when a derived
# image unsets them, and so `-e RUNWISP_CONFIG=` (empty) doesn't produce a
# check against "". := covers both empty and unset.
#
# The export matters: on a truly-unset var, := creates a shell variable that is
# NOT exported, so the binary we exec would fall back to ./runwisp.toml
# (cmd/runwisp/root.go) while we validated /etc/runwisp/runwisp.toml. Exporting
# keeps this script and the daemon looking at the same paths.
: "${RUNWISP_CONFIG:=/etc/runwisp/runwisp.toml}"
: "${RUNWISP_DATA:=/var/lib/runwisp}"
export RUNWISP_CONFIG RUNWISP_DATA

# --- normalize argv into a full "runwisp <args...>" command -------------------
if [ "$#" -eq 0 ]; then
	set -- runwisp daemon
else
	case "$1" in
	runwisp) : ;; # already explicit
	# Root-level help/version must not be rewritten into `runwisp daemon -h`,
	# which would print the daemon subcommand's help instead of the program's.
	-h | --help | -v | --version) set -- runwisp "$@" ;;
	-*) set -- runwisp daemon "$@" ;; # bare flags are daemon flags
	*) set -- runwisp "$@" ;;         # bare subcommand
	esac
fi

# --- resolve the subcommand cobra will dispatch to ---------------------------
# Mirrors cobra's stripFlags (spf13/cobra command.go): "--" ends flag parsing;
# a long flag without "=" consumes the next argument, and that holds for
# *unknown* long flags too; a bare two-character short flag does the same;
# "-xvalue" carries its own value. The first surviving positional is the
# subcommand. None of RunWisp's root persistent flags are booleans, so there is
# no no-value case to special-case here.
#
# Why not just grep the arguments for "daemon": it would false-positive on
# `--data daemon` and on `runwisp run daemon` (a task named "daemon"), and
# then demand a password from a one-shot command that needs none.
subcommand=""
extra_positional=""
positionals=0
config_flag=""
data_flag=""
socket_flag=""
capture=""
help_only=""
first=1

for arg in "$@"; do
	# Skip argv[0] ("runwisp"); everything after it is flags and subcommands.
	if [ -n "$first" ]; then
		first=""
		continue
	fi

	if [ -n "$capture" ]; then
		case "$capture" in
		config) config_flag=$arg ;;
		data) data_flag=$arg ;;
		socket) socket_flag=$arg ;;
		esac
		capture=""
		continue
	fi

	case "$arg" in
	--) break ;;
	-h | --help | -v | --version) help_only=1 ;;
	--config=* | -c=*) config_flag=${arg#*=} ;;
	--data=*) data_flag=${arg#*=} ;;
	--socket=*) socket_flag=${arg#*=} ;;
	-c | --config) capture=config ;;
	--data) capture=data ;;
	--socket) capture=socket ;;
	-c?*) config_flag=${arg#-c} ;;
	--*=*) : ;;           # some other long flag with its value attached
	--*) capture=other ;; # cobra consumes the next argument
	-?) capture=other ;;  # two-char short flag: same
	-*) : ;;              # -xvalue: value attached
	*)
		positionals=$((positionals + 1))
		if [ "$positionals" -eq 1 ]; then
			subcommand=$arg
		elif [ -z "$extra_positional" ]; then
			extra_positional=$arg
		fi
		;;
	esac
done

# --- does this invocation bring a daemon up? --------------------------------
# Daemon-starting: `daemon`, `cloud`, `restart`, `demo`, and *no subcommand at
# all* — bare `runwisp` spawns a background daemon before opening the TUI.
#
# So the list below is an EXEMPTION list of one-shot and client-only
# subcommands, and anything unrecognized is treated as daemon-starting. That
# fails closed: a subcommand added later can never silently skip these checks,
# while a new one-shot subcommand merely asks for a password until it's listed
# here — loud, and a one-line fix.
starts_daemon=1
if [ -n "$help_only" ]; then
	starts_daemon=""
else
	case "$subcommand" in
	validate | list | status | stop | reload | password | openapi | schema | \
		agent-guide | import | run | tui | service | help | completion)
		starts_daemon=""
		;;
	esac
fi

if [ -z "$starts_daemon" ]; then
	exec "$@"
fi

config=${config_flag:-$RUNWISP_CONFIG}
data=${data_flag:-$RUNWISP_DATA}

auth_lower=$(printf '%s' "${RUNWISP_AUTH:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
if [ -z "${RUNWISP_PASSWORD:-}" ] && [ "$auth_lower" != "off" ]; then
	cat >&2 <<'EOF'
error: refusing to start the daemon without an explicit auth setting.

Without one, RunWisp generates a random password in memory on every boot:
nobody can log into the Web UI, and every restart invalidates existing
sessions. In a container that is never what you wanted.

Set one of:
  -e RUNWISP_PASSWORD=<your-password>   require this password to log in
  -e RUNWISP_AUTH=off                   disable auth entirely (trusted network only)
EOF
	exit 1
fi

# `runwisp daemon` takes no positional arguments and cobra silently ignores
# extras, so a misplaced flag (`--config /x daemon`, which normalizes to
# `runwisp daemon --config /x daemon`) would otherwise start a daemon while
# quietly dropping part of what was asked for.
if [ "$subcommand" = daemon ] && [ -n "$extra_positional" ]; then
	printf 'error: unexpected argument "%s" after the daemon subcommand.\n\n' "$extra_positional" >&2
	printf 'Flags belong after the subcommand:\n  docker run ... runwisp daemon --config %s\n' "$config" >&2
	exit 1
fi

if [ -d "$config" ]; then
	cat >&2 <<EOF
error: $config is a directory, but --config must name a single TOML file.

Mount the file itself:
  -v /path/to/runwisp.toml:$config:ro

To split config across several files, mount the directory one level up, keep
runwisp.toml inside it, and pull the rest in from there:
  -v /path/to/conf:$(dirname "$config"):ro
  # runwisp.toml:
  #   [daemon]
  #   include = ["conf.d/*.toml"]
EOF
	exit 1
fi

if [ ! -f "$config" ]; then
	cat >&2 <<EOF
error: no runwisp.toml found at $config

Mount your config, e.g.:
  -v /path/to/runwisp.toml:$config:ro
EOF
	exit 1
fi

if [ ! -r "$config" ]; then
	printf 'error: %s exists but is not readable by uid %s.\n\n' "$config" "$(id -u)" >&2
	printf 'Check the bind mount and any --user override.\n' >&2
	exit 1
fi

if ! mkdir -p "$data" 2>/dev/null || [ ! -w "$data" ]; then
	printf 'error: data dir %s is not writable by uid %s.\n\n' "$data" "$(id -u)" >&2
	printf 'RunWisp needs it for the SQLite database, task logs, and the control socket.\n' >&2
	printf 'Check that the volume is not mounted :ro, and any --user override.\n' >&2
	exit 1
fi

# The image HEALTHCHECK runs `runwisp status` in a fresh process that sees only
# the image ENV, so a socket path chosen by these flags is invisible to it and
# the container would report unhealthy while the daemon is perfectly fine.
if [ -n "$data_flag" ] || [ -n "$socket_flag" ]; then
	printf 'warning: --data/--socket on the command line do not reach the container\n' >&2
	printf '         HEALTHCHECK, which resolves the control socket from RUNWISP_DATA /\n' >&2
	printf '         RUNWISP_SOCKET. Prefer -e RUNWISP_DATA=... / -e RUNWISP_SOCKET=...\n' >&2
fi

exec "$@"
