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
