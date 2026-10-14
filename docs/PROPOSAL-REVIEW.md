# Review of the original proposal

Date: 2026-10-13. Subject: "OT Fabric SunSpec Explorer, Application Proposal (Proposed PoC)". Reviewed against go-sunspec and sunspecctl (public repos, then the local checkouts), the Wails v3 documentation, and the website repo.

## Verdict

Approve with changes. Scope and layering are right: shared service, backend-side polling, headless engine owns recordings, loopback by default, honest about non-atomic snapshots, SSE instead of a custom WebSocket protocol. The single-binary goal does not work for gateways, and several things go-sunspec and the brand already provide (or require) were missing.

## What held up

- go-sunspec: 112 built-in models, scale factors and units, enum/bitfield symbols, nested and repeating groups, not-implemented detection, raw fallback for unknown models, `Device` reads only.
- The point-table fields are all obtainable: labels, descriptions, units, access, mandatory, static, symbols from `registry.PointMeta`; values and flags from `DecodedPoint`; instance index from `DecodedGroup.Index`.
- Wails v3 does have a compile-time `server` build tag (no cgo, browser windows, `/health`, WebSocket events, binds `localhost` by default).

## Changes made, with reasons (all reflected in the other docs)

| # | Change | Why |
|---|---|---|
| 1 | Two artifacts (GUI + plain Go daemon) instead of one binary | A cgo GTK/WebKit binary cannot start without those libraries; Wails v3 is Beta; Linux default is GTK4 + WebKitGTK 6.0 (Debian 12 and Ubuntu 22.04 lack it). Also matches the org's "pure Go, no cgo" positioning. |
| 2 | `Service` interface ~~with two implementations, remote client in Go~~ (superseded by #14: one Go implementation) | The interface still decouples the user interfaces from the engine, and the contract tests still prevent drift between the engine and the HTTP handlers. |
| 3 | Session-based API (`/sessions/{id}/...`) | The original endpoints had no session handle although the text speaks of sessions; one serialized connection per target (inverters often allow 1 to 3 TCP clients). |
| 4 | Read-only by allow-list + lint + wire test | `sunspec.Discover` takes a concrete `*modbus.Client`, which exposes write methods; "never call" is only convention. Blocklist of FCs misses FC08 and vendor codes. |
| 5 | R1 widened to URL-based targets, serial, RTU-over-TCP, TLS, register type, base addresses; probe attempts shown on failure | go-sunspec and sunspecctl support them; a gateway with RS-485 is the natural headless case; wrong unit ID is the common failure. |
| 6 | Daemon token, Host check, target allow-list, concrete limits | Loopback is not an auth boundary for other local users or browsers; the daemon connects wherever told (network-scanner risk on an OT gateway). |
| 7 | Per-model read loop, `ErrModelChanged` handling, selected-model polling, static-model skip, start-to-start timing | `ReadAll` returns only the first error; a re-flashed device must not be decoded with a stale layout; a full chain cannot be read at 1 s on 9600-baud serial. |
| 8 | Device-file export compatible with `sunspecctl capture`/`serve`; CSV long format; compact vs full metadata | Turns a field capture into a replayable fixture; the original's "CSV is awkward for nested models" disappears in long format; embedding schema per sample would bloat recordings. |
| 9 | Version handshake, clock-skew warning, systemd unit, signing, standard repo files | Gateways update rarely; sample times come from the daemon clock; field tool needs signed installers. |
| 10 | Brand tokens, icon rules, semantic colors | See `BRANDING.md`. |
| 11 | Daemon embeds and serves the web UI; frontend built twice (Wails, web) behind TS `Service`/`Platform` interfaces | User decision 2026-10-13. Also covers workstations where the Wails GUI cannot run (Debian 11/12). Replaces the earlier idea of using Wails' `-tags server` for this. |
| 12 | Recordings default to a temporary data directory with small limits, a save-a-copy prompt, a hardened directory check, and a systemd unit without `PrivateTmp` | User decision. Temp storage is ephemeral (Debian 13 `/tmp` is tmpfs, RAM-backed) and a private tmp would delete recordings whenever the service stops. |
| 13 | Unsigned releases for now, with checksums, provenance attestations and install notes | No macOS or Windows signing identity. |
| 14 | Remote access is browser-only: daemon + user-managed SSH port forward + browser. The desktop GUI is Local only; the Go HTTP client (`internal/api/client`) and the GUI remote mode are dropped. M3 gets `sunspec-explorerd url` and the token lifecycle instead | User decision 2026-10-13, after M1. The daemon already serves the full UI (#11), so a second remote client duplicated it: one more `Service` implementation, token storage on the workstation and a version handshake between two installed programs. Nothing had been built for it (no client package, no remote UI, no token storage existed). |

## Verified in the local checkouts (2026-10-13)

- go-sunspec: `go 1.23`, go-modbus v1.4.2; `ModelInstance`, `DiscoveryResult`, `Device`, `DiscoverOptions` as described in `ARCHITECTURE.md` section 5; addresses zero-based; `PointMeta` fields (`Label`, `Desc`, `Units`, `Access`, `Mandatory`, `Static`, `Symbols`, `Offset`); sentinel errors (`ErrNotSunSpec`, `ErrPartialRead`, `ErrModelChanged`, ...); no client-side write calls in non-test code.
- Website: `tokens.css` values, logo files (weave mark, dark/light), `favicon.svg`, 12 project icons, inline UI icon conventions, `projects.json` categories including the planned `ui-apps` entry, deps (Astro, Starlight, Tailwind 4, fontsource; no icon library).
- Contrast ratios in `BRANDING.md` were computed, not estimated.

## Not verified (do before relying on them)

| Item | How to settle |
|---|---|
| ~~Whether go-modbus has a transport/PDU hook for a FC allow-list~~ | **Settled 2026-10-13**: it has none (v1.4.2). The guard sits on the `ClientMetrics.OnRequest` callback instead (`ARCHITECTURE.md` section 5). go-modbus v1.5.0 (2026-10-14) added `Config.AllowedFunctionCodes`, and the Explorer uses it since v0.0.2 |
| Wails v3 specifics: asset handler details, packaging behavior, exact beta version to pin | Create the M1 skeleton with `wails3 init` and run `wails3 doctor` on each target OS |
| Static daemon on real gateways: Debian 11, 12, 13 on amd64, arm64, armv7 (targets confirmed by the user 2026-10-13) | Run acceptance test A12 on at least one real device per architecture |
| WebKitGTK 6.0 on Debian 13, and any GUI viability on Debian 11 | `wails3 doctor` on each; only matters for Linux workstations, not gateways |
| Two citations in the original proposal (Wails FAQ page, pkg.go.dev server example) | Not opened during this review; not needed for the decisions above |
| Wails Beta status and GTK4 default | Taken from the Wails v3 status and Linux packaging pages (last updated 2026-10-05); re-check at project start |
| Debian 13 mounts `/tmp` as tmpfs by default; systemd removes a `PrivateTmp` directory when the service stops | From general knowledge, not checked in this session: run `findmnt /tmp` on a Debian 13 gateway and read the systemd docs |
| Current Gatekeeper and SmartScreen user steps, and which signing programs (e.g. free open-source signing) you would be eligible for | From general knowledge, not checked: re-check when revisiting signing |

## Sources

- go-sunspec README and API.md; sunspecctl README; local checkouts of go-sunspec and website
- Wails v3: Server Build, Status, Linux Packaging (v3.wails.io)
- otfabric.io homepage
