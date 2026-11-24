# Security Policy

## Reporting a vulnerability

If you find a security issue in RunWisp, please disclose it privately. **Do not open a public GitHub issue for security vulnerabilities.**

### How to report

Email **runwisp@poppycake.eu** with:

- A description of the vulnerability and its impact
- Steps to reproduce (a proof-of-concept is ideal)
- The version or commit you tested against
- Any suggested mitigation (optional)

If you'd like to encrypt the report, ask in the first message and we'll exchange a key out-of-band.

### What to expect

- **Acknowledgment** on a best-effort basis, typically within a few days.
- **Triage and severity assessment** typically within two weeks of acknowledgment.
- **Fix or mitigation timeline** communicated after triage; severity drives priority.
- **Re-ping welcome** if you haven't heard back within a week; a missed email shouldn't burn your report.
- **Credit** in the release notes and [CHANGELOG.md](CHANGELOG.md) unless you ask to remain anonymous.

We do not run a bug bounty program. We do gratefully accept reports.

## Supported versions

Security fixes ship only on the latest release; there are no patch backports for older releases. Stay current to stay patched.

| Version        | Supported |
| -------------- | --------- |
| Latest release | Yes       |
| Older releases | No        |

## Verifying a download

Verify a release tarball, checksum file or Docker image with `gh attestation verify <file> --repo runwisp/runwisp` (for an image: `gh attestation verify oci://runwisp/runwisp:<tag> --repo runwisp/runwisp`).

## Trust model

Before reporting, understand the boundaries RunWisp commits to:

- **The daemon runs with the privilege of whoever started it** and executes shell commands from `runwisp.toml`. Anyone who can write that file, or read the data directory, can execute arbitrary code as the daemon user. By design.
- **`runwisp.toml` is trusted input.** So is the config document an SDK app sends over the local socket (`runwisp daemon --app`); it goes through the same validation as the TOML. REST API and Web UI inputs are **not** trusted. The daemon never executes shell strings supplied over HTTP.
- **A system-wide service install refuses an untrusted config path.** `runwisp service install` (the default scope) runs the daemon as root, so it requires the resolved `runwisp.toml` to be root-owned and not group/world-writable before baking it into the unit.
- **Authentication, by client:**
  - **Local clients (CLI, TUI)** connect over a Unix domain socket at `<datadir>/runwisp.sock` (mode `0600` inside the `0700` data dir). The daemon verifies the peer UID at accept time via `SO_PEERCRED` (Linux) / `LOCAL_PEERCRED` (macOS) and closes a foreign UID immediately. No password is involved: access to the socket is the credential.
  - **Network clients (Web UI, remote REST, remote TUI)** log in with a password using a challenge-response handshake, so the password never travels in plaintext even over HTTP. A successful login issues a signed session cookie (`HttpOnly`, `SameSite=Strict`, `Secure` when served over HTTPS).
  - **Read-only access** is an optional second password (`RUNWISP_VIEWER_PASSWORD`, or minted with `runwisp password --viewer`). Its sessions can view everything and change nothing.
  - **Hook tokens** (`hook_tokens` in `runwisp.toml`) let CI or a webhook control a single task or service, limited to the actions the token allows, without the dashboard password. They are enforced even with `RUNWISP_AUTH=off`.
  - The TUI's "Open in browser" action mints a single-use launch ticket over the local socket; the browser redeems it on `127.0.0.1` to receive a session cookie. The password never leaves the host.
- **No password on disk.** The daemon password is either supplied via `RUNWISP_PASSWORD` (in-memory only) or freshly generated each boot (ephemeral). The session signing key is **derived** from the password and the per-install fingerprint via PBKDF2-HMAC-SHA-256 and never written. Setting a fresh `RUNWISP_PASSWORD` and restarting invalidates every existing session.
- **No required network.** The daemon works fully offline. Outbound calls (notifications, heartbeat pings, the update check) run in the background and never block scheduling or the UI. The update check is on by default and sends nothing about your tasks; turn it off with `[daemon] check_updates = false`.

This is single-tenant by design. RunWisp does not ship SSO, directory integration, or fine-grained RBAC; those are stated non-goals.

## In scope

We welcome reports against:

- Authentication bypass or session forgery against the password challenge-response, the session cookie, or the password-derived session signing key
- Unix-socket trust bypass: anything that lets a foreign UID drive the daemon despite `SO_PEERCRED` / `LOCAL_PEERCRED`, or that lets a network caller reach socket-only endpoints (e.g. `GET /api/local/credentials`)
- Launch-ticket flaws: replay, forgery, or off-host redemption of a ticket meant for `127.0.0.1`
- Authorization flaws: unauthorized triggering, listing, stopping, or observing of tasks via REST or the Web UI
- A read-only session changing anything, or a hook token acting on another unit or beyond its `allow` list
- Injection into the REST API, SSE log stream, or notification channels (Slack, Discord, Telegram, ntfy, Gotify, Pushover, email/SMTP, generic webhooks, in-app)
- Path traversal or arbitrary file disclosure via the daemon
- TOML parser bugs that escalate privilege beyond what `run = "..."` already permits
- Misuse of `X-Forwarded-*` headers: spoofing the source IP or scheme past `trusted_proxies`
- Insecure defaults in the binary or the installer at `get.runwisp.com`
- Vulnerabilities in direct dependencies that meaningfully affect RunWisp

## Out of scope

We'll close these without a fix:

- **Arbitrary code execution via `runwisp.toml`.** That's the documented behaviour: `run =` is a shell command. Anyone with write access to the TOML can run shell as the daemon user.
- **Anything reachable by reading the data directory.** A user who can read `<datadir>` can connect to the Unix socket and drive the daemon. The data dir's `0700` perms are the boundary; if you've already crossed it, you're inside the trust circle.
- **Denial of service against the local daemon by the operator running it.** RunWisp is single-tenant.
- **Unrestricted access when the operator has explicitly set `RUNWISP_AUTH=off`.** That flag disables the auth boundary wholesale, on purpose, and warns loudly at startup. Anyone who can reach the bound address is meant to act as an authenticated user.
- **Unauthenticated `/metrics` once the operator enables it.** Like most exporters, the endpoint has no auth; use `metrics_listen` to keep it on loopback.
- Missing security headers (HSTS, etc.) when a reverse proxy is placed in front of the daemon. Those are the proxy's job.
- Missing rate limits or CAPTCHAs on the auth endpoint when bound to loopback.
- Findings against the marketing site (`runwisp.com`) or docs site (`docs.runwisp.com`). Those are separate properties, but email us and we'll route it.
- Social engineering, physical access, or supply-chain attacks against contributors.

## Deployment hardening

If you're running RunWisp in production, here's the short list:

1. **Keep the data directory at `0700`.** Local-socket trust assumes only the daemon's user can read it. Don't loosen perms; don't share the data dir across users.
2. **Set `RUNWISP_PASSWORD` for network clients.** If unset, the daemon mints a random ephemeral password every boot: fine for a local workstation, lousy for shared servers because every restart logs every browser session out. Source the password from a Docker secret, systemd `LoadCredential=`, or your sealed-secrets workflow.
3. **Keep `--host 127.0.0.1`.** The daemon binds to loopback by default. On a non-loopback address with `tls = "off"`, the session cookie travels in cleartext and anyone on path can ride your session until it expires (the challenge-response handshake protects only the password). If you must expose it, either set `daemon.tls = "auto"` (or supply `tls_cert`/`tls_key`) so the daemon serves HTTPS itself, or terminate TLS at a reverse proxy (nginx, Caddy, Traefik) on the same host and forward over loopback. The daemon prints a warning banner when it starts exposed over plain HTTP; don't ignore it.
4. **Set `trusted_proxies` (or `RUNWISP_TRUSTED_PROXIES`) to your proxy's CIDR** (e.g. `127.0.0.1/32`). The daemon will then honour `X-Forwarded-Proto: https` for secure cookie issuance and `X-Forwarded-For` for rate-limit accounting. Catch-all ranges (`0.0.0.0/0`, `::/0`) are rejected.
5. **Keep hook tokens out of `runwisp.toml`.** Load them with `${VAR}` or `${file:...}`, and give each caller its own token with the narrowest `allow` list it needs.
6. **Run as an unprivileged user** wherever possible. The daemon needs only the permissions required to execute its tasks and own its data dir.
7. **Stay on the latest release.** Watch [runwisp.com/releases](https://runwisp.com/releases/).
