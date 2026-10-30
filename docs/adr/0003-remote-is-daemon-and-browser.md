# ADR 0003: Remote access is the daemon and a browser

- Status: Accepted
- Date: 2026-10-13, after M1 (decision change)
- Specified in: `AGENTS.md`, decision change; `docs/ARCHITECTURE.md` section 3; `docs/PROPOSAL-REVIEW.md`, change 14

## Context

The first design had a GUI Remote mode and a Go HTTP client of the daemon.

## Decision

Remote use is `sunspec-explorerd` on the gateway, a user-managed SSH port forward, and a browser. The daemon embeds the web UI. The GUI is Local only.

## Consequences

`internal/api/client` is not to be built. There is no remote target UI and no token storage in the GUI. The frontend is built twice against the TypeScript `Service` and `Platform` interfaces.

The token, Host check and bind rules that protect this path are in `docs/ARCHITECTURE.md` section 9.
