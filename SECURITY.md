# Security Policy

## Reporting a vulnerability

If you find a security issue in RunWisp, please disclose it privately. **Do not open a public GitHub issue for security vulnerabilities.**

### How to report

Email **runwisp@poppycake.eu** with:

- A description of the vulnerability and its impact
- Steps to reproduce — a proof-of-concept is ideal
- The version or commit you tested against
- Any suggested mitigation (optional)

If you'd like to encrypt the report, ask in the first message and we'll exchange a key out-of-band.

### What to expect

- **Acknowledgment** on a best-effort basis, typically within a few days.
- **Triage and severity assessment** typically within two weeks of acknowledgment.
- **Fix or mitigation timeline** communicated after triage — severity drives priority.
- **Re-ping welcome** if you haven't heard back within a week; a missed email shouldn't burn your report.
- **Credit** in the release notes and [CHANGELOG.md](CHANGELOG.md) unless you ask to remain anonymous.

We do not run a bug bounty program. We do gratefully accept reports.

## Supported versions

RunWisp is **pre-1.0**. Security fixes ship only on the latest release — there are no patch backports for older pre-1.0 versions. Stay current to stay patched.

| Version        | Supported |
| -------------- | --------- |
| Latest release | Yes       |
| Older releases | No        |

## Trust model

Before reporting, understand the boundaries RunWisp commits to:

- **The daemon runs with the privilege of whoever started it** and executes shell commands from `runwisp.toml`. Anyone who can write that file — or anyone who can read the data directory — can execute arbitrary code as the daemon user. By design.
- **`runwisp.toml` is trusted input.** REST API and Web UI inputs are **not**. The daemon never executes shell strings supplied over HTTP.
- **A system-wide service install refuses an untrusted config path.** `runwisp service install` (the default scope) runs the daemon as root, so it checks the resolved `runwisp.toml` path the same way cron-sourced files are checked — root-owned, not group/world-writable — before baking it into the unit. Retiring cron is additionally refused if any configured cron source failed to load, or would run a task as a user the daemon cannot become; see [Retiring cron](https://docs.runwisp.com/coming-from/cron/).
- **Two authentication paths, by client location:**
  - **Local clients (CLI, TUI)** connect to the daemon over a Unix domain socket at `<datadir>/runwisp.sock` (mode `0600` inside the `0700` data dir). The daemon verifies the peer UID at accept time via `SO_PEERCRED` (Linux) / `LOCAL_PEERCRED` (macOS); a foreign UID is closed immediately. No password is involved — the socket presence is the credential.
  - **Network clients (Web UI, remote REST)** log in with a password using a challenge-response handshake, so the password never travels in plaintext even over HTTP. A successful login issues a session in a secure cookie.
  - The TUI's "Open in browser" action mints a single-use launch ticket over the local socket; the browser redeems it on `127.0.0.1` to receive a session cookie. The password never leaves the host.
- **No secrets on disk.** The daemon password is either supplied via `RUNWISP_PASSWORD` (in-memory only) or freshly generated each boot (ephemeral). The JWT signing key is **derived** from the password and the per-install fingerprint via HKDF-SHA-256; it is never written. Setting a fresh `RUNWISP_PASSWORD` and restarting invalidates every existing session.
- **No required network.** The daemon must work fully offline; outbound integrations (`internal/cloud/`, notification channels, TLS cert lookups) are strictly opt-in.

This is single-tenant by design. RunWisp does not ship SSO, directory integration, or fine-grained RBAC — those are stated non-goals.

## In scope

We welcome reports against:

- Authentication bypass or session forgery against the password challenge-response, the session cookie, or the HKDF-derived JWT signing key
- Unix-socket trust bypass — anything that lets a foreign UID drive the daemon despite `SO_PEERCRED` / `LOCAL_PEERCRED`, or that lets a network caller reach socket-only endpoints (e.g. `GET /api/local/credentials`)
- Launch-ticket flaws — replay, forgery, or off-host redemption of a ticket meant for `127.0.0.1`
- Authorization flaws — unauthorized triggering, listing, stopping, or observing of tasks via REST or the control-plane protocol
- Injection into the REST API, SSE log stream, or notification channels (Slack, Discord, Telegram, email/SMTP, generic webhooks, in-app)
- Path traversal or arbitrary file disclosure via the daemon
- TOML parser bugs that escalate privilege beyond what `run = "..."` already permits
- Misuse of `X-Forwarded-*` headers — spoofing the source IP or scheme past `RUNWISP_TRUSTED_PROXIES`
- Insecure defaults in the binary or the installer at `get.runwisp.com`
- Vulnerabilities in direct dependencies that meaningfully affect RunWisp

## Out of scope

We'll close these without a fix:

- **Arbitrary code execution via `runwisp.toml`.** That's the documented behaviour — `run =` is a shell command. Anyone with write access to the TOML can run shell as the daemon user.
- **Anything reachable by reading the data directory.** A user who can read `<datadir>` can connect to the Unix socket and drive the daemon. The data dir's `0700` perms are the boundary; if you've already crossed it, you're inside the trust circle.
- **Denial of service against the local daemon by the operator running it.** RunWisp is single-tenant.
- **Unrestricted access when the operator has explicitly set `RUNWISP_AUTH=off`.** That flag disables the auth boundary wholesale, on purpose, and warns loudly at startup. Anyone who can reach the bound address is meant to act as an authenticated user.
- Missing security headers (HSTS, etc.) when a reverse proxy is placed in front of the daemon — those are the proxy's job. Building on the daemon's own HTTPS (`tls = "auto"` or a supplied certificate) doesn't change this: the proxy, if present, still owns headers for whatever it terminates.
- Missing rate limits or CAPTCHAs on the auth endpoint when bound to loopback.
- Findings against the marketing site (`runwisp.com`) or docs site (`docs.runwisp.com`) — those are separate properties, but email us and we'll route it.
- Social engineering, physical access, or supply-chain attacks against contributors.

## Deployment hardening

If you're running RunWisp in production, here's the short list:

1. **Keep the data directory at `0700`.** Local-socket trust assumes only the daemon's user can read it. Don't loosen perms; don't share the data dir across users.
2. **Set `RUNWISP_PASSWORD` for network clients.** If unset, the daemon mints a random ephemeral password every boot — fine for a local workstation, lousy for shared servers because every restart logs every browser session out. Source the password from a Docker secret, systemd `LoadCredential=`, or your sealed-secrets workflow.
3. **Keep `--host 127.0.0.1`.** The daemon binds to loopback by default. If you must expose it, either set `daemon.tls = "auto"` (or supply `tls_cert`/`tls_key`) so the daemon serves HTTPS itself, or terminate TLS at a reverse proxy (nginx, Caddy, Traefik) and forward over the loopback interface — the challenge-response handshake protects the password on the wire, but the session cookie does not.
4. **Set `RUNWISP_TRUSTED_PROXIES` to your proxy's CIDR** (e.g. `127.0.0.1/32`). The daemon will then honour `X-Forwarded-Proto: https` for secure cookie issuance and `X-Forwarded-For` for rate-limit accounting. Catch-all ranges (`0.0.0.0/0`, `::/0`) are rejected — trusting the entire internet would let any client spoof their IP.
5. **Don't ignore the non-loopback warning banner.** The daemon prints it to stderr when it starts on a non-loopback address serving plain HTTP (`tls = "off"`). It exists for a reason.
6. **Run as an unprivileged user** wherever possible. The daemon needs only the permissions required to execute its tasks and own its data dir.
7. **Stay on the latest release.** Watch [GitHub Releases](https://github.com/runwisp/runwisp/releases) — pre-1.0, security fixes ship on the moving train.

## Network exposure

By default the daemon binds to `127.0.0.1` and is reachable only from the same host. If you bind to a non-loopback address (`--host 0.0.0.0`, a public IP, …) with `tls` left at its default (`"off"`), the API and the session cookie travel in **cleartext HTTP** — anyone on path can capture the cookie and ride your session. (The password itself is protected by the challenge-response handshake, but a captured session cookie is sufficient to act as you until it expires.)

For any non-loopback exposure, pick one:

- **Built-in TLS.** Set `daemon.tls = "auto"` to have the daemon self-sign and serve HTTPS on its own, or supply `tls_cert`/`tls_key` for a certificate you manage. No reverse proxy required.
- **Reverse proxy.** Keep `--host 127.0.0.1` (or `::1`) and `tls = "off"`, run a reverse proxy on the same host that terminates TLS and forwards to the daemon over loopback, and set `RUNWISP_TRUSTED_PROXIES` to that proxy's CIDR.

## Acknowledgments

Reporters who help improve RunWisp's security will be credited in [CHANGELOG.md](CHANGELOG.md) alongside the fix, unless they prefer to remain anonymous.
