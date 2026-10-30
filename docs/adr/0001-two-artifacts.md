# ADR 0001: Two artifacts from one module

- Status: Accepted
- Date: 2026-10-13
- Specified in: `AGENTS.md`, settled decisions; `docs/ARCHITECTURE.md` section 2; `docs/PROPOSAL-REVIEW.md`, change 1

## Context

Gateways run Debian 11, 12 and 13 on amd64, arm64 and armv7. A dynamically linked cgo GUI binary fails in the loader on a machine without the GTK and WebKit libraries, before `main` runs.

## Decision

`sunspec-explorer` is the Wails GUI (cgo, Local mode only). `sunspec-explorerd` is a static daemon (`CGO_ENABLED=0`). One `Service` interface and one Go engine serve both.

## Consequences

The daemon must never import Wails: a test on `go list -deps` and a CI step enforce it. armv7 is daemon-only, since Wails v3 supports Linux amd64 and arm64 only.
