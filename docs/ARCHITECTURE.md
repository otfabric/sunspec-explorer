# Architecture

Implements `REQUIREMENTS.md`. Facts about go-sunspec below were checked against the local checkout (go-sunspec `go 1.23`, go-modbus v1.4.2) on 2026-10-13; the module uses the released go-sunspec v0.5.1, go-modbus v1.4.2 and Wails v3.0.0-beta.29. Items marked **VERIFY** were not checkable from the connected folders.

## 1. Principles

1. One Go engine, two places it runs: in the desktop application's process (Local), or in the daemon, used from a browser (Remote). There is no Go client of the daemon.
2. The GUI talks to a `Service` interface only, never to Modbus or HTTP directly.
3. The daemon is plain Go: static, no cgo, no Wails.
4. Read-only is a property of the code, enforced and tested, not of the UI.
5. Small: no database, no auth framework, no external services.

## 2. Artifacts

| Artifact | Built with | Notes |
|---|---|---|
| `sunspec-explorer` | Wails v3 (Beta), cgo | Local mode only (in-process engine; no remote mode, no daemon client). Linux: GTK4 + WebKitGTK 6.0 by default; `-tags gtk3` (GTK3 + WebKit2GTK 4.1) is legacy, supported through v3.0.x, removed in v3.1. Windows needs the WebView2 runtime. macOS uses WKWebView. Built on a runner of the target architecture (the cgo build is never cross-compiled or emulated). Needs Go 1.25+. |
| `sunspec-explorerd` | `CGO_ENABLED=0 go build` | Static, serves `/api/v1` and the embedded web UI (`frontend/dist/web`); the browser is its only client. Subcommands `url` and `token rotate` (section 9). Release targets: `GOOS=linux` with `GOARCH=arm64`, `GOARCH=arm GOARM=7`, and `GOARCH=amd64`. Static linking means one binary per architecture runs on Debian 11, 12 and 13 (glibc versions do not matter). |

Platform notes: gateways run Debian 11 (Bullseye), 12 (Bookworm) and 13 (Trixie) on amd64, arm64 and armv7 (confirmed by the user). The GUI is not needed there. Wails v3 supports Linux amd64 and arm64 only, so armv7 is daemon-only. Windows arm64 for the GUI is best effort (CI matrix entry if it costs nothing, no dedicated test device; Windows 11 on ARM can run the amd64 build under emulation). CI builds the daemon for all three Linux architectures on every change, which also proves that every dependency (including go-serial) stays cgo-free. Builds run on native-architecture runners, without QEMU: amd64 on an x86-64 runner, arm64 on an arm64 runner, and armv7 as Go cross-compilation on the arm64 runner (the container image likewise: its Dockerfile cross-compiles in the build stages and its last stage only copies files). `.github/workflows/build.yml` holds the checks and every build; CI calls it on each push and the release workflow calls it to publish, so the release path is exercised continuously.

Why not one binary: a dynamically linked cgo GUI binary fails in the loader on a machine without the GTK/WebKit shared libraries, before `main` runs. Runtime flags cannot fix that. Wails' own `-tags server` build is compile-time too. It is not used by default because it would tie the daemon to Wails Beta APIs and Wails' WebSocket event protocol instead of this project's versioned API. It is not needed for browser access: the daemon embeds and serves the web build itself (section 10).

The daemon is delivered three ways from the same static build: tarballs per architecture, the systemd unit in `packaging/`, and a container image (`packaging/Dockerfile`: web UI and binary built in stages, final image distroless with the binary, a non-root user and the `/data` volume). In a container the daemon must listen on every address for a published port to reach it, so the image starts it with `--listen 0.0.0.0:8765 --allow-remote-bind`; the port is to be published on the host's loopback only (`-p 127.0.0.1:8765:8765`). `SUNSPEC_EXPLORER_DATA_DIR` (set to `/data` in the image) replaces the default data directory, so that `docker exec <name> sunspec-explorerd url` needs no flag. For a daemon bound to every address the printed URL names `127.0.0.1`.

CI guards: (a) `go list -deps ./cmd/sunspec-explorerd` must not contain `github.com/wailsapp/wails`; (b) `file` on the built daemon reports "statically linked"; (c) a container without GTK runs it.

## 3. Layering

```
cmd/sunspec-explorer  ->  internal/gui          -> service.Service (engine)
cmd/sunspec-explorerd ->  internal/api (server) -> service.Service (engine)   <- browser: fetch + SSE
internal/engine -> go-sunspec -> go-modbus
```

- `internal/model`: JSON DTOs independent of go-sunspec types (stable wire and file formats).
- `internal/service`: the `Service` interface and error taxonomy.
- `internal/engine`: the one Go implementation of `Service`. Owns sessions, pollers, recorders, exporters.
- `internal/api`: HTTP/SSE handlers wrapping a `Service`. There is no Go client package.
- `internal/gui`: thin Wails service binding exposing the engine's `Service` methods to the frontend and forwarding events to the window.
- `internal/web`: `go:embed` of `frontend/dist/web` plus a static handler with SPA fallback, CSP and cache headers, used by the `internal/api` server. Must not import Wails.
- `internal/readonly`: guard, lint config and wire-test helpers.

Remote operation is the daemon plus a browser, nothing else: the user forwards the daemon's loopback port over SSH (`ssh -N -L <port>:127.0.0.1:<port> user@gateway`) and opens `http://127.0.0.1:<port>/#token=...`. The page is the web build, served by the daemon itself, so it is same-origin (no CORS) and always matches the daemon's API version. The desktop application is not involved and has no remote mode (decided 2026-10-13; the earlier design had a Go HTTP client as a second `Service` implementation inside the GUI).

The `Service` interface exists twice, with a different number of implementations: in Go there is one (the engine; the daemon's handlers and the Wails binding both wrap it), in TypeScript there are two (Wails bindings in the desktop build, `fetch` to `/api/v1` in the web build; section 10).

```go
type Service interface {
    Health(ctx context.Context) (model.Health, error)
    OpenSession(ctx context.Context, cfg model.TargetConfig) (model.SessionInfo, error) // detect + discover
    CloseSession(ctx context.Context, id string) error
    Sessions(ctx context.Context) ([]model.SessionInfo, error)
    Rediscover(ctx context.Context, id string) (model.SessionInfo, error)
    Snapshot(ctx context.Context, id string, req model.SnapshotRequest) (model.Snapshot, error)
    StartPolling(ctx context.Context, id string, cfg model.PollConfig) error
    StopPolling(ctx context.Context, id string) error
    State(ctx context.Context, id string) (model.SessionState, error)      // last sample + counters, for a browser that reconnects
    Subscribe(ctx context.Context, sinceSeq uint64) (<-chan model.Event, error)
    StartRecording(ctx context.Context, id string, cfg model.RecordingConfig) (model.RecordingInfo, error)
    StopRecording(ctx context.Context, recID string) (model.RecordingInfo, error)
    Recordings(ctx context.Context) ([]model.RecordingInfo, error)
    OpenRecording(ctx context.Context, recID string, offset int64) (io.ReadCloser, model.RecordingInfo, error)
    DeleteRecording(ctx context.Context, recID string) error
    SerialPorts(ctx context.Context) ([]model.SerialPort, error)
}
```

A contract test suite (`internal/api/contract_test.go`) runs against the engine directly and against the HTTP API of an `api` server wrapping an engine. For the second run the test file holds a small `net/http` adapter that presents the API as a `Service`; it is test code, not a package the programs link. Both runs must pass the same tests, which keeps the handlers from drifting from the interface.

## 4. Sessions

A session = target (URL, unit ID, register type, base addresses) + one open `*modbus.Client` + one `sunspec.Device` + at most one poller + at most one recorder.

- All Modbus calls of a session go through one mutex-guarded queue on a single connection. Many inverters accept only 1 to 3 TCP clients, so never open a second connection per session.
- v0.1 UI shows one active session; the API and engine are multi-session from the start (limit in R15).
- Session identity is an opaque ID returned by `OpenSession`. Target URLs are stored without credentials.

## 5. go-sunspec facts the engine relies on

- `sunspec.Detect` returns probe attempts; `Discover` returns a `*Device` with `Discovery` (`BaseAddress`, `RegType`, `Models []ModelInstance`, `Warnings`, `Raw`).
- `ModelInstance`: `Header` (`ID`, `Length`, `StartAddress`, `EndAddress`; all **zero-based** register addresses), `Name`, `Schema`, `SchemaKnown`, `DecodingSupported`.
- `Device.ReadAll` loops every model, returns the decoded models it got plus only the **first** error. The engine therefore loops `ReadModel` itself to keep a status per model.
- `ReadModel` reads header + data together and returns `ErrModelChanged` when the device's ID/length no longer match discovery (then data is returned undecoded). Handling: re-discover, emit event.
- `DecodedModel`: `ModelID`, `InstanceAddress`, `Name`, `Schema`, `Group`, `RawRegisters`, `Warnings`. `DecodedGroup`: `Name`, `Index`, `Points`, `Groups`; helpers `Walk`, `GroupsNamed`, `FindPoint`.
- `DecodedPoint` has no label or description: join them from `registry.PointMeta` (`Label`, `Desc`, `Units`, `Access`, `Mandatory`, `Static`, `Offset`, `Size`, `SF`, `Symbols`) through `DecodedModel.Schema`. `DecodedPoint.RegisterOffset` counts from the model's ID register, so absolute address = `Header.StartAddress + RegisterOffset` (zero-based). `DecodedPoint` already implements `json.Marshaler` (NaN/Inf become `null`).
- `PointMeta.Static` identifies static points. **No registry model consists of static points alone** (checked against all 112): Common has the writable device address `DA`. The engine therefore calls a model static-only when every point is static, padding or writable (`Access == "RW"`, a setting that changes only when somebody writes it). That covers models 1, 5, 6, 9, 14, 18, 702 and two vendor models; they are read once at the start of polling and on request.
- A chain that became **shorter** does not produce `ErrModelChanged`: the read of a model that is gone fails with exception 2 (illegal data address), which `ReadModel` returns as a plain error. On that exception the engine reads the model's two header registers; when they are gone or differ, it treats the model as changed.
- Concurrency: `Device` follows the go-modbus client's rules, hence the per-session queue.
- No client-side write calls exist in go-sunspec non-test code (grep for `WriteRegister`, `WriteCoil`). Its `Server` type lives in the same root package as the client.
- **Verified 2026-10-13 (go-modbus v1.4.2)**: there is no transport/PDU hook or middleware that can reject a request, and no dialer hook. The lowest usable point is `Config.Metrics`: the client calls `ClientMetrics.OnRequest(unitID, functionCode)` immediately before sending, before it takes any lock. `internal/readonly.Guard` panics there for anything but FC03/FC04 and `readonly.Do` turns the panic into an error, so the request never reaches the wire and the client stays usable (tested with a wire tap). A real allow-list option in go-modbus (`Config.AllowedFunctionCodes` or a request interceptor) would be cleaner; open question for the go-modbus maintainers.

## 6. Polling

- `interval` whole seconds, 1..3600, default 5. Timer is start-to-start. If a cycle is still running when the next tick fires, skip that tick and increment `overruns` (no catch-up).
- Scope: `model_indexes` (chain positions). Default: every model not static-only. Static-only models are read at start and on request.
- Per model per cycle: status `ok|error|changed`, duration, error. A sample is always emitted, with `stale` flags per model computed from the last `ok` time (stale after 2 intervals).
- Connection errors: close, reopen with exponential backoff (1 s to 30 s), emit status events; polling state persists.
- `ErrModelChanged`: stop reading that session's models, re-run discovery, emit `discovery_changed`, resume. Recording gets an event line.
- Timestamps: `sample_time` = start of the first request, `read_duration_ms` = to end of the last. Samples are not atomic across models; documentation says so.

## 7. Data formats

Snapshot export (`otfabric.sunspec-explorer.snapshot/1`):

```json
{
  "schema": "otfabric.sunspec-explorer.snapshot/1",
  "app": {"name": "sunspec-explorer", "version": "0.1.0"},
  "libs": {"go_sunspec": "v0.5.x", "registry_models": 112},
  "captured_at": "2026-10-13T20:48:29.826123456Z",
  "read_duration_ms": 412,
  "target": {"url": "tcp://192.168.1.100:502", "unit_id": 1, "reg_type": "holding", "base_address": 40000},
  "device": {"manufacturer": "...", "model": "...", "version": "...", "serial": "..."},
  "models": [{
    "index": 1, "id": 103, "name": "Inverter (Three Phase)",
    "start_address": 40070, "length": 50, "schema_known": true,
    "status": "ok", "error": null, "read_duration_ms": 38, "warnings": [],
    "group": {"name": "inverter", "index": 0,
      "points": [{"name": "W", "label": "AC Power", "description": "...", "type": "int16", "units": "W",
                  "access": "R", "mandatory": true, "static": false, "implemented": true,
                  "raw": 4200, "scaled": 4200, "sf_name": "W_SF", "sf_raw": 0, "symbols": [],
                  "address": 40078, "offset": 8}],
      "groups": []},
    "raw_registers": null
  }],
  "warnings": []
}
```

Rules: `metadata` (label, description, access, mandatory, static) is included for exports (`full`) and omitted in recordings (`compact`). Unknown models carry `raw_registers` and `schema_known: false`. Serial number and addresses are included; the UI warns before saving.

Recording (`.jsonl`, one JSON object per line):

```
{"type":"header","schema":"otfabric.sunspec-explorer.recording/1","started_at":"...","interval_s":5,"target":{...},"app":{...},"libs":{...},"models":[{"index":0,"id":1},...]}
{"type":"sample","seq":1,"captured_at":"...","read_duration_ms":412,"overruns":0,"models":[{"index":1,"status":"ok","points":[...compact...]}]}
{"type":"event","seq":2,"at":"...","kind":"discovery_changed|reconnect|gap","detail":"..."}
{"type":"footer","stopped_at":"...","samples":720,"failures":3,"reason":"user|limit|shutdown"}
```

As implemented (`internal/engine/recording.go`, types in `internal/model`): the header also carries `name`, `device` and the full model list; a sample line is the poll sample as it is (`session_id`, `metadata: "compact"`, per-model status, stale flag and points); event kinds are `gap` (polling not running or stopped), `polling_started`, `reconnecting`, `connected` and `discovery_changed` (with the new `models`); the footer `reason` is `user`, `limit`, `session_closed`, `shutdown` or `error`. Files live in `<data-dir>/recordings`, mode 0600.

Every line is one `write` call, unbuffered. A truncated last line (crash) is tolerated by readers. Rotation by size creates `<name>.000.jsonl`, `.001.jsonl`. A `<name>.meta.json` sidecar (state, counters, config; written atomically at start, every 30 samples and at stop) records `running|stopped|interrupted`. At start the engine marks every recording that still says `running` as `interrupted` and counts its samples again from the files. While a recording runs, each `sample` event carries its counters, so the user interface needs no timer. CSV (long) export columns: `captured_at, seq, model_index, model_id, group_path, point, value, scaled, unit, implemented, status`. Device-file export matches the `sunspecctl capture` format (see `sunspecctl` repo, `devicefile.go`; consider moving that code into go-sunspec so both can import it).

## 8. HTTP API v1 (daemon)

All `/api/v1` requests: `Authorization: Bearer <token>`, `Content-Type: application/json`. Errors: `{"error":{"code":"...","message":"..."}}`.

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/health` | `{status, version, api_version, uptime_s, go_sunspec, registry_models}` |
| `POST /api/v1/sessions` | Open session (target config) -> `SessionInfo` with discovery result and probe attempts |
| `GET /api/v1/sessions` / `GET /api/v1/sessions/{id}` | List / get |
| `DELETE /api/v1/sessions/{id}` | Close |
| `POST /api/v1/sessions/{id}/rediscover` | Re-run discovery |
| `POST /api/v1/sessions/{id}/snapshot` | Single read (optional `model_indexes`) |
| `PUT /api/v1/sessions/{id}/polling` / `DELETE ...` | Start (replace) / stop polling |
| `GET /api/v1/sessions/{id}/state` | Last sample, counters, polling and recording state (used after reconnect) |
| `GET /api/v1/events?since=<seq>` | SSE stream |
| `POST /api/v1/sessions/{id}/recordings` | Start recording (name, rotation) |
| `POST /api/v1/recordings/{rid}/stop` | Stop |
| `GET /api/v1/recordings` | List (state `running|stopped|interrupted`) |
| `GET /api/v1/recordings/{rid}/download` | Download of all files of the recording as one stream; `Range: bytes=N-` and `bytes=N-M` supported; works while running (up to the size at that moment) |
| `DELETE /api/v1/recordings/{rid}` | Delete a recording that is not running |
| `GET /api/v1/serial-ports` | Allowed serial devices on the daemon host |
| `GET /` and static assets | Embedded web UI unless `--no-ui`. Assets need no token (none contain secrets), SPA fallback, `no-cache` for `index.html`, long-lived caching for hashed assets |

No endpoint accepts a file path, a raw Modbus frame, a function code, or a register write.

SSE: `id:` = monotonically increasing `seq`; the daemon keeps a ring buffer (default 1024 events) and replays from `since` or `Last-Event-ID`; if the gap is larger, it sends `resync` and the client calls `/state`. Event kinds: `sample`, `session_status`, `discovery_changed`, `recording_status`, `error`, `resync`. The web UI reads the stream with `fetch` (not `EventSource`) so it can send the bearer header.

## 9. Security and read-only enforcement

Read-only, defense in depth:

1. Allow-list FC03 and FC04 at the lowest hook go-modbus offers: `internal/readonly.Guard` on `Config.Metrics` (section 5).
2. Lint (`golangci-lint` `forbidigo` or a small analyzer): ban `Write*`/FC05/06/15/16/21/22/23/08 helpers and any import of go-sunspec `Server`/go-modbus server types outside `_test.go`.
3. Wire test: run every workflow against a recording TCP proxy in front of the fixture server and assert the set of function codes observed is a subset of {03, 04}.

Daemon:

- Bind `127.0.0.1` by default. Any other address needs `--allow-remote-bind`, logs a warning (the listener is plain HTTP: the token crosses the network unencrypted) and still requires the token and the Host check. TLS on the listener is not offered: the supported way across a network is the SSH port forward.
- Host check: a request must name a host the daemon answers to (`127.0.0.1`, `localhost`, `::1`; for a remote bind also the bind address and `--allow-host` names). The port is not compared, because the local end of an SSH forward may use another port. This is what stops DNS rebinding from a page in the user's browser.
- When the tunnel drops, nothing changes on the daemon: sessions, polling and (M3) recordings continue. The page shows "connection lost", retries with backoff and resumes with `GET /api/v1/events?since=<last seq>`: the daemon replays what the page missed, or sends `resync` first when its buffer wrapped, and the page then re-reads `/state` and says that events were missed. A page that is opened or reloaded adopts the session that is still open on the daemon.
- Bearer token: generated on the first start, written to a 0600 file (default `<data-dir>/token`, `--token-file` overrides), reused by later starts, compared in constant time. With the default temporary data directory a cleared temp dir means a new token. `sunspec-explorerd token rotate` replaces the file atomically (write to a temporary name, 0600, rename); the running daemon notices the change (it checks the file's modification time), switches to the new token and closes event streams opened with the old one. Host header check; no permissive CORS.
- The token is never logged. It leaves the daemon in two ways only: the single startup line that prints the URL (R21), and `sunspec-explorerd url`.
- `sunspec-explorerd url` prints `http://<address>:<port>/#token=<token>` for the running daemon and, when the hostname is known, the matching `ssh -N -L <port>:127.0.0.1:<port> <user>@<hostname>`. It needs the address the daemon actually bound (`--listen` may say port 0): the daemon writes it at start to a 0600 state file next to the token file, and `url` reads both. It fails with a clear message when no daemon is running.
- Web UI: the token reaches the page only in the URL fragment (never sent to the server, never in query strings, cookies or logs); the page keeps it in memory, strips it from the address bar and sends it as a header. Headers: CSP `default-src 'self'; connect-src 'self'` with no inline script, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `frame-ancestors 'none'`. `--no-ui` disables static serving.
- Data directory: default `<tmp>/sunspec-explorer-<uid>` (`--data-dir` overrides). Created 0700; the daemon `lstat`s it and refuses a symlink, another owner or wider permissions (shared `/tmp` symlink attacks). Temporary by design: lost when the OS clears it (Debian 13's `/tmp` is tmpfs, RAM-backed), so defaults are small (R15) and the UI prompts to save a copy. The systemd unit must not set `PrivateTmp=yes`: systemd deletes a private `/tmp` when the service stops, which would destroy recordings on every restart.
- `--allow-target <cidr>` (repeatable; default RFC 1918 + link-local) and `--allow-serial <glob>` (default none).
- Limits (R15), fixed recording directory, recording names `[a-zA-Z0-9._-]{1,64}`.
- SSH secures the tunnel but is not the daemon's authentication; other local users on the gateway could otherwise reach the loopback port.

## 10. Frontend

React + TypeScript + Tailwind v4 (the website uses Tailwind 4.x) on the Wails template. Tokens and fonts per `BRANDING.md`; fonts from `@fontsource/*` packages so the app works offline.

One source tree, two builds:

| Build | Output | Transport | Embedded by |
|---|---|---|---|
| Wails | `frontend/dist/wails` | Wails bindings to the Go `Service` (the in-process engine; Local only) | `cmd/sunspec-explorer` |
| Web | `frontend/dist/web` | `fetch` plus fetch-streamed SSE to same-origin `/api/v1` with the bearer token | `internal/web` in `cmd/sunspec-explorerd` |

Shared code lives in `frontend/src/app` and depends on two TypeScript interfaces only: `Service` (mirrors the Go interface, types mirrored from `internal/model`; two implementations, one per build) and `Platform` (`saveFile`, `openExternal`, `persistTheme`, `copyText`). Wails uses native dialogs; the web build uses Blob/`<a download>`. The Wails runtime package must not be imported by the web entry point (separate entry files `main.wails.tsx` and `main.web.tsx`), so the daemon bundle stays free of it. Build order: `make frontend` (both builds) before the Go builds, because the daemon embeds `dist/web`. The web bundle lives inside a gateway binary, so keep it small and set a size budget in CI (suggested starting point: 1.5 MB gzipped, to be revisited).

State comes from three sources only: Service query results, the event stream, and local UI state. No JS polling timers. Components: connection bar (mode badge LOCAL or BROWSER, target, connect/disconnect), model sidebar (chain order), point table (virtualized), toolbar (single read, poll interval, start/stop, export, record), status bar (last sample time, stale, failures, recording state and storage location).

Browser mode is the remote mode, and needs no desktop install: any current browser works against a daemon on a gateway (through the SSH tunnel) or a daemon running on the workstation, which is also the way to use the Explorer where the Wails GUI cannot run (e.g. Debian 11/12 desktops).

## 11. Testing strategy

- Unit: engine pieces with the go-sunspec `testutil` fixture server and an in-process `sunspec.Server` in tests.
- Contract: one suite, run against the engine and against the daemon's HTTP API (section 3).
- Wire: function-code capture (A11).
- Fault injection: chaos proxy for drops, delays, exceptions per address range (A6, A7).
- Soak: 24 h polling + recording (A14).
- Daemon portability: static binary check, run in a GTK-less container, `go list -deps` guard (A12).
- Frontend: component tests plus smoke tests in both themes for the Wails build and, with Playwright against a running daemon, the web build (A15).
- Data directory hardening and systemd unit lint (A16).

## 12. Milestones

| M | Deliverable | Exit criteria |
|---|---|---|
| M1 | Local end-to-end behind `Service`: connect (tcp, rtuovertcp), discover, browse, single read, polling, JSON export, branding applied, frontend written against the TS `Service`/`Platform` interfaces with a web build skeleton | A1 to A8 (Local), A11 |
| M2 | `sunspec-explorerd`, HTTP/SSE API, auth, allow-lists, limits, serial + TLS targets (Modbus side), version handshake, embedded web UI (R21) = remote operation through SSH forward + browser (R10), contract tests | A10, A12, A13, A15, A16 |
| M3 | Remote convenience and hardening: `sunspec-explorerd url` (paste-ready URL with `#token=`, `ssh -L` suggestion), token lifecycle (persisted 0600, `token rotate`, never logged), bind rules (`--allow-remote-bind` with a warning), connection-lost handling with event replay; recording with rotation and recovery, CSV and device-file exports, time-skew warning | A9, A14, A17 |
| M4 | Packaging (unsigned, see R20), systemd unit, release checksums and provenance, docs, website entry (see `BRANDING.md` section 8) | Installable artifacts on all targets |

Sequencing note: M1 first de-risks the Wails Beta without changing the architecture, because the `Service` interface exists from day one.
