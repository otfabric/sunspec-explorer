# ADR 0004: Recordings default to a temporary directory

- Status: Accepted
- Date: 2026-10-13 (user decision)
- Specified in: `AGENTS.md`, settled decisions; `docs/PROPOSAL-REVIEW.md`, change 12

## Decision

The engine's data directory defaults to `<tmp>/sunspec-explorer-<uid>`, mode 0700, ownership checked. `--data-dir` selects persistent storage.

## Consequences

Default recordings are ephemeral: the OS may clear them, and Debian 13 mounts `/tmp` as tmpfs by default. So the default size limits are small and the UI prompts to save a copy. The systemd unit must not set `PrivateTmp=yes`, which would delete recordings when the service stops.
