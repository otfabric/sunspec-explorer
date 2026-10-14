# OT Fabric SunSpec Explorer: agent handoff

Status (2026-10-13): **M1 and the daemon part of M2/M3 are implemented**: engine, read-only guard, Wails shell (Local only), frontend (both builds), `sunspec-explorerd` with the HTTP/SSE API, embedded web UI, token lifecycle, `url` and `token rotate`, bind rules, the target allow-list, the systemd unit, the container image and the release workflow (first release: v0.0.1, a preview). v0.0.1 is released (with recording). Implemented for v0.0.2: serial (`rtu://`) and TLS (`tcp+tls://`) targets with the serial allow-list, CSV and device-file exports, request pacing, the clock-skew warning, the version handshake in the web UI, the published snapshot JSON Schema, a Windows icon, trimmed fonts. The free-space floor for recordings (R15) is in as well. **Not there yet**: installers, a real-browser test suite, the soak test. This file is the entry point for Claude Code and other agents. Read it fully, then the docs it points to, before writing code.

## What this is

A small, **read-only** desktop tool (Wails v3 + React/TypeScript) to connect to a SunSpec device, discover its model chain, browse fully decoded points, read once or poll, and export or record measurements. It is built on [otfabric/go-sunspec](https://github.com/otfabric/go-sunspec) (which sits on otfabric/go-modbus). One Go engine runs in two places: in the desktop GUI's process (Local), or in a headless daemon on a gateway that serves the web UI to a browser through an SSH port forward (Remote). The GUI has no remote mode.

## Read these, in this order

1. `docs/REQUIREMENTS.md`: what to build, numbered requirements (R1..R21), acceptance tests (A1..A18).
2. `docs/ARCHITECTURE.md`: how: artifacts, layering, Service interface, API, data formats, security, milestones.
3. `docs/BRANDING.md`: OT Fabric visual identity, tokens, icon rules, accessibility numbers.
4. `docs/PROPOSAL-REVIEW.md`: why the original proposal was changed, what was verified, what is still unverified.

## Decisions that are settled (do not relitigate without asking the user)

- **Two artifacts from one module**: `sunspec-explorer` (Wails GUI, cgo, Local mode only) and `sunspec-explorerd` (plain Go daemon, `CGO_ENABLED=0`, static, linux/arm64 + armv7 + amd64, all three MUST; target gateways run Debian 11, 12 and 13). A single binary was rejected: a cgo/GTK binary cannot start on a gateway without GTK/WebKit libraries.
- **The daemon must never import Wails.** Enforce with a CI test on `go list -deps`.
- **One `Service` interface, one Go implementation** (the in-process engine). The Wails binding and the daemon's HTTP handlers both wrap it; a contract test suite runs against the engine and against the HTTP API. The TypeScript `Service` interface has two implementations (Wails bindings, `fetch` to `/api/v1`).
- **Remote = daemon + user-managed SSH port forward + browser. No GUI remote mode.** On the gateway run `sunspec-explorerd`; on the workstation `ssh -N -L <port>:127.0.0.1:<port> user@gateway` and open `http://127.0.0.1:<port>/#token=...`. No desktop install and no Wails on that path. There is no Go client of the daemon (`internal/api/client` is not to be built), no remote target UI and no token storage in the GUI.
- **Everything is session-based**: a session = target + `sunspec.Device` + poller + recorder. All Modbus traffic of a session is serialized on one connection.
- **Read-only is enforced, not implied**: every Modbus client is created with go-modbus's allow-list (`Config.AllowedFunctionCodes` = FC03, FC04; go-modbus v1.5.0 or later), plus the lint ban on `Write*` and on go-sunspec's `Server` outside tests, and wire-capture tests.
- **Daemon is authenticated even on loopback**: bearer token generated on first start, kept in a 0600 file, rotatable (`sunspec-explorerd token rotate`); Host header check; target/serial allow-list. Default bind is 127.0.0.1 only; any other address needs `--allow-remote-bind`, logs a warning and still requires the token and the Host check (no TLS on the listener: use the SSH forward). `sunspec-explorerd url` prints the paste-ready URL.
- **The daemon serves the web UI**: `sunspec-explorerd` embeds the frontend and serves it at `/` next to `/api/v1`, so a browser through the SSH tunnel is a first-class client (also the way to use the Explorer on workstations where the Wails GUI cannot run). The frontend is built twice from one source tree (Wails build, web build with no Wails runtime) against two TypeScript interfaces, `Service` and `Platform`.
- **Recordings live in the engine's data directory, which defaults to a temporary folder**: `<tmp>/sunspec-explorer-<uid>`, mode 0700, ownership checked. That storage is ephemeral (lost when the OS clears the temp dir; Debian 13 mounts `/tmp` as tmpfs by default), so default size limits are small and the UI prompts to save a copy. `--data-dir` selects persistent storage.
- **CI and release builds run on native-architecture runners, never under QEMU** (user preference, 2026-10-14: faster). One reusable workflow (`build.yml`) holds all checks and builds; `ci.yml` and `release.yml` call it. Releases are made by the manually started Release workflow, with the organisation's conventions (the same as `otfabric/.github` go-binary-release): input `bump` = none, patch, minor or major; the version is the latest `v*` tag plus the bump; the notes are the `## v<version>` section of `RELEASE.md` up to a line `---`; the workflow pushes an annotated tag and publishes the release.
- **Releases are unsigned for now** (no macOS or Windows signing identity): ship SHA-256 checksums, CI build provenance attestations and documented Gatekeeper/SmartScreen steps.
- **Recording format v0.1 is JSON Lines**; CSV (long format) and a `sunspecctl serve`-compatible device file are exports.
- **Branding**: dark theme by default, tokens from `docs/BRANDING.md`, fonts bundled via fontsource (no CDN, the app must work offline).

## Hard rules

- No Modbus write function codes anywhere. No exposure of go-sunspec's `Server`, the simulator, or arbitrary Modbus commands. The `Server` may only be used in `_test.go` files.
- No cgo in `internal/` except what the Wails GUI strictly needs under `cmd/sunspec-explorer` and `internal/gui`.
- Do not embed `registry.ModelMeta` in every snapshot or recording line; write a header with versions and reference the schema by model ID.
- Never log the bearer token (it is shown only by `sunspec-explorerd url` and, when the daemon writes to a terminal, by one startup line), serial numbers at info level, or full target URLs with credentials.
- The web UI receives the token only in the URL fragment (`#token=...`), never in a query string or cookie. Never set `PrivateTmp=yes` in the systemd unit (systemd would delete recordings every time the service stops).
- Pin the Wails v3 beta version in `go.mod`; it is a prerelease and APIs may still change.
- Do not commit unless the user asks. When committing, follow the user's commit conventions.

## Conventions to copy from sibling repos (go-sunspec, sunspecctl)

`Makefile` with `make check` (format, vet, golangci-lint, vulnerability scan, race tests, coverage gate), `.golangci.yml`, `codecov.yml`, `CONTRIBUTING.md`, `SECURITY.md`, `RELEASE.md`, GitHub Actions CI, MIT license. Release artifacts get SHA-256 checksum files like `sunspecctl`.

## Target repo layout

```
cmd/sunspec-explorer/      Wails GUI main (Local mode only)
cmd/sunspec-explorerd/     headless daemon main (no Wails, no cgo); subcommands url, token rotate
internal/model/            JSON DTOs (Snapshot, ModelResult, PointView, Sample, ...)
internal/service/          Service interface + errors
internal/engine/           in-process implementation (sessions, polling, recording, export)
internal/api/              HTTP/SSE handlers wrapping Service (no Go client package)
internal/readonly/         read-only guard + lint helpers + wire-test helpers
internal/gui/              Wails service binding, event forwarding to the window
internal/web/              static handler for the web UI (SPA fallback, CSP), used by the daemon (never imports Wails)
frontend/                  React + TypeScript + Tailwind v4, tokens from docs/BRANDING.md; two builds: dist/wails and dist/web; embed.go embeds both
packaging/                 systemd unit, Dockerfile of the daemon image, later nfpm/AppImage config and signing notes
tests/                     soak tests, fixtures (not there yet); contract, auth, Host, SSE and wire tests live in internal/api and internal/engine
docs/                      the four documents above, REMOTE-USE.md, schema/ (JSON Schema of the snapshot export)
```

## Answered by the user (2026-10-13)

- Gateways run **Debian 11 (Bullseye), 12 (Bookworm) and 13 (Trixie)** on **amd64, arm64 and armv7**. All three architectures are MUST for the daemon. The static `CGO_ENABLED=0` build is what lets one binary per architecture run on all three Debian versions.
- **Windows ARM64 GUI**: only if it costs no extra effort. Best effort: add it to the CI matrix if it builds with the same pipeline, give it no dedicated test device, and never block a release on it. Windows 11 on ARM can run the amd64 build under emulation.
- There is no armv7 GUI: Wails v3 supports Linux amd64 and arm64 only. armv7 is daemon-only.

### Also answered (2026-10-13, later)

- **Default recording directory**: a temporary folder (see settled decisions). Consequence: recordings are ephemeral by default, with small default limits and a save-a-copy prompt.
- **Daemon serves the frontend**: yes ("very nice"). Consequence: browser mode (R21), a second frontend build, a TypeScript `Service`/`Platform` abstraction from M1.
- **Signing identity**: none. Consequence: unsigned releases (R20).

### Decision change (2026-10-13, after M1)

- **Remote access is browser-only.** The GUI's Remote mode and the Go HTTP client are dropped (see settled decisions, R10, `PROPOSAL-REVIEW.md` change 14). M3 gets `sunspec-explorerd url` and the token lifecycle instead.

### Also decided (2026-10-13)

- **The daemon is also a container image** (`packaging/Dockerfile`, `make docker`), mainly for trying it on macOS. Publish its port on 127.0.0.1 only. For local testing, `sunspecctl serve` is the simulated device (CI must not depend on it).

## Open questions

None blocking. Revisit signing and a persistent default data directory before a public release.

## Milestones (details in docs/ARCHITECTURE.md)

M1 Local end-to-end behind `Service` (frontend written against the TS `Service`/`Platform` interfaces, web build skeleton included) -> M2 daemon + HTTP/SSE API + embedded web UI + contract tests -> M3 remote convenience (`sunspec-explorerd url`, token lifecycle and rotation, bind rules), recording with rotation and recovery, exports -> M4 packaging (unsigned), systemd, docs, website entry.

## Definition of done for any change

`make check` passes; new behavior has a test against go-sunspec's `testutil` fixture server or an in-process `sunspec.Server` (tests only); no new write-capable call paths; UI changes use tokens, not hard-coded colors; both themes checked in both the Wails and the web build.
