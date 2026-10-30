# ADR 0002: Read-only is enforced, not implied

- Status: Accepted
- Date: 2026-10-13
- Specified in: `AGENTS.md`, settled decisions; `docs/ARCHITECTURE.md` section 9; `docs/PROPOSAL-REVIEW.md`, change 4

## Context

`sunspec.Discover` takes a concrete `*modbus.Client`, which exposes write methods, so "never call" is only a convention. A blocklist of function codes misses FC08 and vendor codes.

## Decision

Every Modbus client is created with go-modbus's allow-list (`Config.AllowedFunctionCodes` = FC03, FC04), with a lint ban on `Write*` and on go-sunspec's `Server` outside tests, and with wire-capture tests.

## Consequences

The application needs go-modbus v1.5.0 or later. New call paths must stay inside the allow-list.
