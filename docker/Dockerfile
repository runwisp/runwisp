# SPDX-FileCopyrightText: PoppyCake, s.r.o.
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Ships the prebuilt runwisp binary (CGO_ENABLED=0, cross-compiled by
# apps/runwisp/scripts/build-all.sh) on top of a minimal base. The binary
# requires a real shell (`run =` tasks execute via /bin/sh -c) so scratch and
# distroless are not viable bases; alpine/debian-slim are the smallest bases
# that provide one. bash gives `run =` tasks a real shell to script against;
# tzdata gives them real IANA zoneinfo data (the daemon's own scheduler embeds
# tzdata separately, see cmd/runwisp/main.go). The Docker-CLI variant (see
# ARG VARIANT below) builds from this same file rather than a second one.
#
# Build context is arranged by docker/build-local.sh (local builds) or the
# release workflow (CI): docker/context/<amd64|arm64>/runwisp must exist
# before `docker build` runs.

# ARGs referenced in a FROM must be declared before the first FROM.
ARG BASE=alpine
ARG VARIANT=plain

FROM alpine:3.22 AS base-alpine
RUN apk add --no-cache ca-certificates bash tzdata

FROM debian:trixie-slim AS base-debian
RUN DEBIAN_FRONTEND=noninteractive apt-get update \
	&& DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates bash tzdata \
	&& rm -rf /var/lib/apt/lists/*

FROM base-${BASE} AS runtime

ARG TARGETARCH
ARG VERSION=0.0.0-dev

LABEL org.opencontainers.image.title="RunWisp" \
	org.opencontainers.image.description="Lightweight cron daemon and process supervisor with a web UI" \
	org.opencontainers.image.url="https://runwisp.com" \
	org.opencontainers.image.documentation="https://docs.runwisp.com" \
	org.opencontainers.image.source="https://github.com/runwisp/runwisp" \
	org.opencontainers.image.licenses="GPL-3.0-or-later" \
	org.opencontainers.image.version="${VERSION}"

COPY context/${TARGETARCH}/runwisp /usr/local/bin/runwisp
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/runwisp /usr/local/bin/docker-entrypoint.sh

# Bind 0.0.0.0 and rely on the daemon's own plain-HTTP-by-default: a
# container is meant to sit behind a reverse proxy or be port-mapped
# directly. Operators wanting the daemon to terminate TLS itself (self-signed
# HTTPS) can set RUNWISP_TLS=auto.
ENV RUNWISP_CONFIG=/etc/runwisp/runwisp.toml \
	RUNWISP_DATA=/var/lib/runwisp \
	RUNWISP_HOST=0.0.0.0 \
	RUNWISP_LOG_FORMAT=text

EXPOSE 9477
VOLUME ["/var/lib/runwisp"]

# Runs as root deliberately: run_user needs root to drop privileges per task
# (internal/executor), and the daemon must be able to write to whatever the
# operator bind-mounts for data/config. Drop to an unprivileged user in your
# own compose file with `user:` if none of your tasks need run_user or root.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD runwisp status >/dev/null 2>&1 || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["runwisp", "daemon"]

FROM runtime AS variant-plain

# Adds a Docker CLI (with the Compose plugin) for tasks that reach into other
# containers over a mounted /var/run/docker.sock — see
# [compose.*] and docs/getting-started/docker#run-the-task-in-another-container.
# Opt-in (docker build --build-arg VARIANT=docker, published as the `-docker`
# tag) because docker-cli-compose alone is ~65MB and most tasks never touch
# Docker.
FROM runtime AS variant-docker
ARG BASE
RUN if [ "${BASE}" = "alpine" ]; then \
		apk add --no-cache docker-cli docker-cli-compose; \
	else \
		DEBIAN_FRONTEND=noninteractive apt-get update \
		&& DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends docker-cli docker-compose \
		&& rm -rf /var/lib/apt/lists/*; \
	fi

FROM variant-${VARIANT}
