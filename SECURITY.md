# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in RunWisp, please report it responsibly. **Do not open a public GitHub issue for security vulnerabilities.**

### How to Report

Send an email to **runwisp@poppycake.eu** with:

- A description of the vulnerability
- Steps to reproduce
- Potential impact assessment
- Any suggested fixes (optional)

### What to Expect

- **Acknowledgment** within 48 hours of your report.
- **Status update** within 7 days with an initial assessment.
- **Resolution timeline** communicated once the issue is triaged.
- **Credit** in the release notes (unless you prefer to remain anonymous).

## Supported Versions

| Version | Supported |
| ------- | --------- |
| Latest  | Yes       |

## Scope

The following are in scope for security reports:

- **Daemon API** — Authentication bypass, authorization flaws, injection attacks
- **WebSocket protocol** — Message tampering, unauthorized access
- **Configuration** — Secrets exposure, insecure defaults
- **Dependencies** — Known vulnerabilities in direct dependencies

## Security Best Practices

When deploying RunWisp:

- Always set a strong `RUNWISP_PASSWORD` for production deployments. If unset, a random password is generated and displayed in the TUI.
- Run the daemon in a container or with restricted OS permissions.
- Use HTTPS (via a reverse proxy) for all API and WebSocket connections.
- Keep the RunWisp binary and dependencies up to date.
- Set `RUNWISP_TRUST_PROXY` only to your actual reverse proxy CIDR ranges.

## Network Exposure

By default the daemon binds to `127.0.0.1` and is reachable only from the same
host. If you bind to a non-loopback address (`--host 0.0.0.0`, a public IP,
etc.) the API, the JWT cookie, and the auth challenge response all travel in
**cleartext HTTP** — anyone on path can capture them. The recommended
deployment for any non-loopback bind is:

1. Keep `--host 127.0.0.1` (or `::1`).
2. Run a reverse proxy (nginx, Caddy, Traefik, …) on the same host that
   terminates TLS and forwards to the daemon over the loopback interface.
3. Set `RUNWISP_TRUST_PROXY` to the CIDR of that proxy (e.g. `127.0.0.1/32`)
   so the daemon honors `X-Forwarded-Proto: https` for cookie issuance.

The daemon prints a banner to stderr when it starts on a non-loopback address
without a trusted proxy configured. Don't ignore it.

### `RUNWISP_TRUST_PROXY`

Comma-separated list of CIDR ranges whose `X-Forwarded-For` and
`X-Forwarded-Proto` headers the daemon will trust. Only set this to the
addresses of reverse proxies you control. The variable rejects catch-all
ranges (`0.0.0.0/0`, `::/0`) — trusting the entire internet would let any
client spoof their IP for rate-limit and loopback checks.
