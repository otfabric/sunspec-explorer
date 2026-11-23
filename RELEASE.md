# sunspec-explorer Releases

## v0.3.3

**Date:** 2026-11-23
**Previous release:** v0.3.2

No feature is added or changed.

### Changed

- Built on go-modbus v1.23.0 (was v1.17.0) (go-modbus RELEASE.md, v1.18.0..v1.23.0). The explorer uses the go-modbus client only and reads with FC 03 and 04; none of the changed behaviours of go-modbus v1.18.0..v1.22.0 (the server changes, and the client changes to FC 08, 12, 20, 43 and `ProbeFunction`) is in a part it uses, and it implements none of the new optional metrics interfaces. go-modbus v1.23.0 changes nothing for the explorer: its RTU fix concerns FC 43/14 and FC 08 responses, which the explorer does not request, and the rest is server-side and opt-in (go-modbus RELEASE.md, v1.23.0). go-sunspec stays at v0.12.2.

---

## v0.3.2

**Date:** 2026-11-16
**Previous release:** v0.3.1

No feature is added or changed.

### Changed

- Built on go-modbus v1.17.0 (was v1.16.2) and go-sunspec v0.12.2 (was v0.12.1) (go-modbus RELEASE.md, v1.17.0; go-sunspec RELEASE.md, v0.12.2).

---

## v0.3.1

**Date:** 2026-11-16
**Previous release:** v0.3.0

No feature is added or changed.

### Changed

- Built on go-modbus v1.16.2 (was v1.16.1), go-serial v0.5.0 (was v0.4.1) and go-sunspec v0.12.1 (was v0.12.0) (go-modbus RELEASE.md, v1.16.2; go-serial RELEASE.md, v0.5.0; go-sunspec RELEASE.md, v0.12.1).

---

## v0.3.0

**Date:** 2026-11-15
**Previous release:** v0.2.9

Bounds on what a connection, a request body and the event buffer can hold; starting and stopping polling and recording are one step with closing a session; refused read-only requests are visible in the health answer. The API version stays 1.0: the health field is an addition.

### Breaking

Behaviour a client or an operator can notice, with the way back where there is one:

- The replay of the event stream (`since`, `Last-Event-ID`) sends, of the sample events still in the buffer, only the newest of each open session; the others are left out, so the `seq` of replayed events can have gaps. The live stream is unchanged, and so is what the web UI and the desktop application show after a reconnect: they show the newest sample. Way back for a client that needs every sample: record the session (`POST /api/v1/sessions/{id}/recordings`) and download the recording.
- `rotate_bytes` of a recording is 0 (the default) or from 65536 (64 KiB) up to the size at which a recording is stopped (`--max-recording-mb`, 64 MiB by default); other values are refused with `invalid_argument`. Before, any positive value was used. Way back: ask for 64 KiB or more, or send 0 and let the operator set `--rotate-recording-mb`.
- `timeout_ms` of a target is checked as sent: a value outside 100..60000 is refused with `invalid_argument`, also where its conversion to a duration used to wrap into that range.
- `--max-recording-mb`, `--max-recordings-mb`, `--rotate-recording-mb` and `--min-free-mb` above 8796093022207 (the largest number of MiB whose bytes fit a 64-bit integer) are refused at start. Before, such a value became another size, for `--min-free-mb` the switched-off check. Way back: `--min-free-mb 0` switches the free-space check off.
- The daemon exits with status 1 when requests still running 5 s after SIGINT or SIGTERM had to be cut off, and says so on standard error (the journal under systemd); before, it exited with 0. A clean stop is still 0, also when a connection that had not sent a request yet was closed.
- The daemon closes a connection that carries no request for 120 s, and answers and closes a request whose body has not arrived 30 s after its headers. Browsers and HTTP clients open a new connection on their own; an event stream is a running request and stays open.

### Added

- `read_only_violations` in `GET /api/v1/health` (`model.Health`, `Health` in `docs/schema/api-1.schema.json`): the number of requests with a function code other than 3 and 4 that the read-only allow-list refused before they were sent. The first one is also logged at error level. It is 0 unless something is wrong.
- `SECURITY.md` lists every resource limit with its value; `TESTING.md` has the lifecycle contract matrix (L1–L16) of the engine and the daemon.

### Fixed

- Two `StartPolling` calls at once on one session, or a `StartPolling` or `StartRecording` that ran while the session closed, could leave a poller or a recording running that `StopPolling`, `CloseSession` and the engine's close no longer reached. Starting, stopping and closing are now one step per session: a start on a session that is closing returns `not_found`, and no poller or recording outlives `StopPolling`, `CloseSession` or the daemon's stop.
- A download whose `Range` ends at the largest number a client can write gets the rest of the recording with a correct `Content-Range` and `Content-Length`, like any range that ends beyond the recording.

### Other

- Lifecycle contract tests for the engine (`internal/engine/lifecycle_test.go`): closing twice and from two goroutines, closing during a blocked read and during `OpenSession`, starts racing a close; they synchronise on events and run with `-race -count=20`. Tests of what the event buffer keeps, of the shutdown bound and of the connection and body timeouts.
- The two tests that waited with a sleep now wait for an event: polling has stopped when its goroutine has ended, and the chain check reads a clock the test moves.

---

## v0.2.9

**Date:** 2026-11-15
**Previous release:** v0.2.8

No change to the application.

### Changed

- Built on go-sunspec v0.12.0 (was v0.11.1) (go-sunspec RELEASE.md, v0.12.0). The explorer only reads devices; of the changed behaviours of go-sunspec v0.12.0, the device-file export is the one it uses:
  - A model with a string point that is not valid UTF-8 is exported by its registers, so `sunspecctl serve` gives a device that reads the same. Before, such a string was exported as a point value that did not read back the same.
  - A model the device reports without data registers is exported as `"registers": []`, which `sunspecctl serve` refuses as a model that does not match its layout. Before, the model was exported without values and served at its schema's length.

---

## v0.2.8

**Date:** 2026-11-15
**Previous release:** v0.2.7

No change to the application.

### Changed

- Built on go-sunspec v0.11.1 (was v0.11.0), go-modbus v1.16.1 (was v1.16.0) and go-serial v0.4.1 (was v0.4.0): on Windows a serial write that times out returns an error together with the bytes written, where it returned a short count without one; on Linux, macOS and the BSDs a port path that is not a character device is refused when it is opened; reads wait correctly on OpenBSD (go-serial RELEASE.md, v0.4.1). This reaches devices on a serial line.

---

## v0.2.6

**Date:** 2026-11-14
**Previous release:** v0.2.5

No change to the application.

### Changed

- Built on go-sunspec v0.11.0 (was v0.10.5) and go-modbus v1.16.0 (was v1.15.3) (go-sunspec RELEASE.md, v0.11.0; go-modbus RELEASE.md, v1.16.0). The explorer only reads devices; the changed behaviours of go-sunspec v0.11.0 (server callbacks, `WritePoint`, device files) are in parts it does not use. A callback of the Modbus client that does not return normally is recovered by the library and reported as an error of that request.

---

## v0.2.5

**Date:** 2026-11-14
**Previous release:** v0.2.4

No change to the application.

### Changed

- Built on go-modbus v1.15.3 (was v1.15.2) and go-sunspec v0.10.5 (was v0.10.4). In go-modbus v1.15.3, after a failed `Accept` the server waits before it tries again, a metrics hook that does not return normally is contained, an RTU request longer than a frame is refused before it is sent, and a request whose context ends while the deadline is set is not sent (go-modbus RELEASE.md, v1.15.3); the go-sunspec library code is unchanged. The previous release named go-modbus v1.15.2 for these changes; v1.15.2 was tagged without them (it holds go-serial v0.4.0 only), so they arrive with this release.

### Tests

- `TestPolling` failed now and then, on v0.2.4 too (5 of 60 runs under `-race`), in two checks. It now checks that no poll cycle reads before it is due, instead of a minimum time between two reads: a cycle's read starts once it has the connection, which a single read may hold. After `StopPolling` it allows the one request that was in flight when polling stopped and can reach the device afterwards. 200 runs under `-race` pass.
- The leak check of the chaos and soak tests compared the number of goroutines, so a goroutine of an earlier test that ended during the check made up for one the test left running, and `TestLeakCheckFindsLeaks` failed now and then. It now reports the goroutines started since the baseline that still run, by ID. A new case of `TestLeakCheckFindsLeaks`, a goroutine left running while an older one ends, fails with the old check.

---

## v0.2.4

**Date:** 2026-11-14
**Previous release:** v0.2.3

No change to the application.

### Changed

- Built on go-modbus v1.15.2 (was v1.15.1) and go-sunspec v0.10.4 (was v0.10.3): go-modbus v1.15.2 backs off after a failed `Accept`, contains a metrics hook that does not return normally, refuses an RTU request longer than a frame before sending it, and does not send a request whose context ended while the deadline was set (go-modbus RELEASE.md, v1.15.2); the go-sunspec library code is unchanged.

---

## v0.2.3

**Date:** 2026-11-14
**Previous release:** v0.2.2

No change to the application.

### Changed

- Built on go-sunspec v0.10.3 (was v0.10.2). Its library code is the same; it is built on go-modbus v1.15.1.
- CI: otfabric/engineering policy 1.6, the shared actions v3.8.0, and one pull request workflow with a single required check.

---

## v0.2.2

**Date:** 2026-11-13
**Previous release:** v0.2.1

### Changed

- Built on go-modbus v1.15.1 and go-sunspec v0.10.2 (was go-modbus v1.14.5 and go-sunspec v0.10.1). go-sunspec is a dependency bump only.
- `rtu://` targets: after a timeout or an answer that does not match its request, the line is discarded until it is quiet, so the next read does not receive a late answer to an earlier one.
- `rtuovertcp://` targets: the connection is closed and redialled after a request error or a mismatched answer. Expect more reconnects behind gateways where units often time out.

---

## v0.2.1

**Date:** 2026-11-13
**Previous release:** v0.2.0

### Changed

- Built on go-sunspec v0.10.1, go-modbus v1.14.5 and go-serial v0.3.0 (was go-sunspec v0.9.0, go-modbus v1.11.1, go-serial v0.2.1). go-modbus wraps dial and TLS handshake errors with `modbus: dial <network> <addr>:`; go-serial rejects out-of-range serial parameters instead of truncating them. No other change.

---

## v0.2.0

**Date:** 2026-11-05
**Previous release:** v0.1.5

### Changed

- Go 1.26 or later is required to build from source (was 1.25). `go fix` modernised the code.
- Built on go-sunspec v0.9.0: a scale factor outside -10..10 sent by a device is no longer applied, so the point shows its raw value instead of +Inf, 0 or NaN.
- Built on go-modbus v1.11.1: RTU and ASCII links drain a late answer instead of returning it for the next request, RTU variable-length reads honour the deadline, and UDP drops bytes left over from a previous datagram.

---

## v0.1.5

**Date:** 2026-11-03
**Previous release:** v0.1.4

### Changed

- Built on go-modbus v1.10.6 (codec count checks compare the full length of a slice) and go-sunspec v0.8.5. No other change.

---

## v0.1.4

**Date:** 2026-11-01
**Previous release:** v0.1.3

### Changed

- Dependency update, no other change. Picks up the go-modbus fix for a response that arrives during `Client.Close`.
- Built on go-modbus v1.10.5, go-sunspec v0.8.3, go-serial v0.2.1.

---

## v0.1.3

**Date:** 2026-11-01
**Previous release:** v0.1.2

### Changed

- Dependency update, no other change.
- Built on go-modbus v1.10.3, go-sunspec v0.8.2, go-serial v0.2.1.

---

## v0.1.2

**Date:** 2026-10-31
**Previous release:** v0.1.1

### Fixed

- The daemon's Host check refuses a malformed Host header. Stray brackets used to be trimmed off, so `]localhost` was taken for `localhost`. Brackets are now accepted only around an IPv6 literal.
- Test only: the chaos and soak leak check no longer counts the Go runtime's network poller as a leak of two descriptors, and a leak report lists the descriptors.

---

## v0.1.1

**Date:** 2026-10-31
**Previous release:** v0.1.0

### Changed

- Policy 1.2: build output in `bin/` and `release/`, a per-package coverage floor, and more tests and CI. No change in behaviour.
- Built on go-modbus v1.10.3, go-sunspec v0.8.1, go-serial v0.2.1.

---

## v0.1.0

**Date:** 2026-10-30
**Previous release:** v0.0.4

### Dependencies

- `go-modbus` v1.7.2 → v1.10.0, `go-sunspec` v0.6.1 → v0.7.1.

### Fixes

- A target with an invalid host, such as `tcp://::` or an IPv6 zone that a URL cannot hold, is refused. It used to be accepted and stored as a URL that could not be read back (`FuzzParseTarget`, `b40ca1077abc80fc`, `551db88b8b6ed6fe`).
- The URL of a daemon bound to an IPv6 address with a zone can be opened: `sunspec-explorerd url` printed the zone unescaped (`FuzzListenAddress`).
- A session finds a model chain that discovery cut short. One bad answer during the walk can look like the end marker: every model found then reads well, and the rest of the chain stayed out of sight until a manual re-discovery. Every 60 s a poll cycle or a snapshot now compares the headers, the end marker and the device identity with the device (a handful of extra FC03 reads), walks the chain again on a difference and emits `discovery_changed`.
- The list of recordings takes the number of files and their size from the disk, not from the sidecar: a damaged sidecar could make a delete walk up to its claimed number of files and spoil the total of what recordings use (`FuzzRecordingStore`).

### Testing

- Fuzz targets for everything read from a device, a client, a flag or a file; a seeded chaos test around polling; a soak test; a nightly workflow. See `TESTING.md`.

---

## v0.0.4

**Date:** 2026-10-29
**Previous release:** v0.0.3

## Summary

Dependency update for SunSpec devices on Modbus RTU: go-serial v0.1.8 fixes two POSIX serial I/O bugs. No command or flag changes.

### Dependencies

- go-sunspec v0.6.0 → v0.6.1, go-modbus v1.5.0 → v1.7.2, go-serial v0.1.6 → v0.1.8.

### Fixes (from go-serial v0.1.8)

- `Write` no longer loses data when the serial port's output buffer is
  full. It used to return a short count with a nil error (or `n == -1`
  with `EAGAIN`); it now blocks until every byte is written.
- `Read` works when the port's file descriptor is 1024 or higher. It used
  to return a spurious `io.EOF` at once.

### Other

- go-modbus v1.5.0 → v1.7.2 also brings the go-modbus fixes of v1.5.1–v1.7.2; see go-modbus's release notes.
- A test now checks the daemon's stop error; security contact is security@otfabric.io.

---

## v0.0.3

**Date:** 2026-10-14
**Previous release:** v0.0.2

## Summary

TLS without the command line in the desktop application, COM ports on Windows, JSON Schemas for everything the Explorer reads and writes, and the device file shared with sunspecctl through go-sunspec.

## Changes

### Added

- **TLS identity in the desktop application**: Advanced → "TLS identity…" chooses the certificate, its private key and the CA (or device) certificate with the system's file dialog. The choice is checked at once (for example, a key that does not belong to the certificate is reported), applied to the running application, and remembered between runs; only the file paths are stored, in the user's settings. An identity given with `--tls-cert`, `--tls-key` and `--tls-ca` still takes precedence and is shown read-only. The daemon keeps its flags.
- **COM ports on Windows**: the desktop application lists the serial ports of the computer, as on macOS and Linux.
- **JSON Schemas for every entity** (`docs/schema/`, see its README): `recording-1.schema.json` for a line of a recording, and `api-1.schema.json` with every entity of the HTTP API (sessions, samples, events, recordings, errors, …) as named definitions, next to the existing `snapshot-1.schema.json`. Tests validate real recordings, every API answer (also the error answers) and the event stream against them. The device file's schema is published with its code in go-sunspec.
- A tooltip on a model marked "static" explains that it is read once and can be ticked to be polled anyway.

### Changed

- **The device-file export uses go-sunspec's `devicefile` package** (go-sunspec v0.6.0), the code `sunspecctl capture` and `sunspecctl serve` use, so the two cannot drift apart. A model is now written by its point values when they decode back to the same points (sunspecctl's rule); v0.0.2 required identical registers. Files can therefore differ from v0.0.2 for models with, for example, space-padded strings; serving them still gives a device with the same values.
- **Install instructions** in the README fetch the latest release and verify its checksum, instead of naming a version.
- The `$defs` of `snapshot-1.schema.json` are renamed to the entity names (`ModelResult`, `GroupView`, `PointView`, `Address`, …); the documents it accepts are unchanged.

### Built with

go-sunspec v0.6.0, go-modbus v1.5.0, Wails v3.0.0-beta.29.

### Not in this release

- Code signing, installers and packages.
- Serial targets were tested for their rules and settings, and RTU framing over TCP; they were not run against a physical serial port.

---

## v0.0.2

**Date:** 2026-10-14
**Previous release:** v0.0.1

## Summary

Serial and TLS devices, two more export formats, and the rough edges of the first preview.

## Changes

### Added

- **Serial targets** (`rtu://<device>`, for example `rtu:///dev/ttyUSB0` or `rtu://COM3`) with baud rate, data bits, parity and stop bits (default 9600 8N1). The page offers the serial devices of the machine the engine runs on. The daemon opens only the devices its operator names with `--allow-serial '<pattern>'`; without that flag it opens none.
- **Modbus/TCP Security targets** (`tcp+tls://host:port`, mutual TLS). The engine uses the identity it was started with: `--tls-cert`, `--tls-key`, `--tls-ca`, on the daemon and on the desktop application. Key material never passes through the user interface or the API.
- **Export as CSV**: one row per point (`captured_at, seq, model_index, model_id, group_path, point, value, scaled, unit, implemented, status`), of a single read and of a recording ("Save a copy as CSV").
- **Export as device file**: a JSON file that `sunspecctl serve` turns back into the same device, register for register. Models are written by point values where those reproduce the registers exactly, otherwise by their registers.
- **JSON Schema** of the snapshot export: `docs/schema/snapshot-1.schema.json`. Every snapshot the Explorer writes is checked against it in the tests.
- **Clock-skew warning**: the page warns when the daemon's clock differs from the browser's by more than 5 seconds; sample times are the daemon's.
- **Free-space floor for recordings**: recording leaves 64 MiB of the data directory's file system alone (`--min-free-mb`; 0 switches it off). Below that no recording starts, and a running one is stopped with the reason "the disk is almost full". On a temporary directory in RAM this protects the gateway's memory.
- **Request pacing**: `--request-delay` puts a pause between the reads of two models, for slow serial lines and devices that cannot take requests back to back.
- The Windows executable has an icon and an application manifest.
- `GET /api/v1/health` reports `features` (`recording`, `serial`, `tls`); `POST /api/v1/sessions/{id}/export`; `GET /api/v1/recordings/{id}/download?format=csv`.

### Changed

- **The daemon no longer writes its URL, and with it the access token, to a journal or a container log.** It prints the URL at startup only when it is started from a terminal. Otherwise it writes one line that names the command to use: `sunspec-explorerd url`.
- **A page that is older than its daemon says so.** When the connection returns after a daemon upgrade, the page offers a reload; when the API is incompatible, it requires one.
- **Read-only is now enforced by the Modbus client itself.** Every client is created with go-modbus's function-code allow-list (new in go-modbus v1.5.0, added for this project) set to FC03 and FC04; anything else is refused before it reaches the transport. It replaces the guard of v0.0.1, which stopped such requests from a metrics callback.
- The window and the browser tab are titled "SunSpec Explorer", like the banner.
- The web bundle is a third of its size (about 0.4 MB compressed instead of 1.2 MB): only the Latin subsets of the fonts are shipped.
- CI and releases build with the current stable Go.

### Fixed

- `rtuovertcp://` targets are now covered by tests against an RTU responder; v0.0.1 listed them untested.

### Built with

go-sunspec v0.5.1, go-modbus v1.5.0, Wails v3.0.0-beta.29.

### Not in this release

- Code signing, installers and packages, as in v0.0.1.
- Serial targets were tested for their rules and settings, and RTU framing over TCP; they were not run against a physical serial port.

---

## v0.0.1

**Date:** 2026-10-14
**Previous release:** none

## Summary

First preview of the OT Fabric SunSpec Explorer: a **read-only** tool to connect to a SunSpec device, discover its model chain, browse fully decoded points, read once or poll, export a snapshot and record measurements over time. It comes as a desktop application and as a headless daemon with the same user interface in a browser.

This is a preview. It is complete enough to explore and record real devices; the downloads are not code-signed, and serial targets are not supported yet. See "Not in this release".

## Changes

### Added

#### Two programs

- **`sunspec-explorer`**, the desktop application (macOS, Windows, Linux). It talks to devices itself. It has no remote mode.
- **`sunspec-explorerd`**, the daemon for gateways: one static binary per architecture (Linux amd64, arm64, armv7) that runs unchanged on Debian 11, 12 and 13, needs no display and no libraries, and serves the user interface to a browser. Also available as a container image.

#### Exploring a device

- **Connect** by URL (`tcp://host:port`, `rtuovertcp://host:port`), unit ID and timeout; register type, base addresses and model limit under "Advanced". A failed connect shows every probe attempt and its error: a wrong unit ID is the usual cause.
- **Discovery** lists every model instance in chain order, including several instances of the same model ID and vendor models without a schema. Re-discover at any time.
- **Point table** with label, description, units, type, access, mandatory and static flags, raw and scaled value, scale factor, enum and bitfield symbols, register address (zero-based, optionally also in the 1-based form of the SunSpec documents) and offset. Repeating and nested groups appear as instances (`module [2]`, `Crv [1] / Pt [3]`). Points a device does not implement are labeled, not shown as 65535. Models without a schema show their raw registers. Search over name, label and description.
- **Read once** all models or a selection; every model reports its own status, error and duration. A model that fails does not hide the others.
- **Polling** runs in the engine, at whole-second intervals from 1 s: start-to-start timing, skipped ticks counted as overruns, one connection per device with nothing overlapping, static models (such as Common) read once. Values that stop arriving are marked stale; failures are shown, not replaced by old values. A device that stops answering is retried with backoff. A device whose model chain changes is discovered again instead of being decoded with the old layout.
- **Export** a snapshot as JSON (`otfabric.sunspec-explorer.snapshot/1`): target, base address, versions, every model with its decoded points and metadata, raw registers of unknown models, warnings. The application warns that an export can contain serial numbers and network addresses.
- The model list collapses to a narrow rail to give the table room.
- Dark and light theme, keyboard navigation, no status carried by color alone. Fonts are bundled: the application makes no request to any other host.

#### Recording

The engine that owns a session records its poll samples as JSON Lines (`otfabric.sunspec-explorer.recording/1`): a header line with the device and its models, one line per sample (compact: no labels or descriptions), event lines for what explains a gap or a change (polling stopped or started, reconnecting, the model chain discovered again, with the new chain), and a footer when it stops. Every line is written at once, so a file is valid up to its last complete line also after a kill.
- Start and stop from the toolbar; the page shows elapsed time, samples, failures, size and where the recording lives, and warns when that is temporary storage. When a recording stops, by you or by the engine, the page says why and offers to save a copy (native Save dialog in the desktop application, browser download through the daemon).
- A recordings list with state, counters, Save a copy and Delete.
- Rotation by size into `<name>.000.jsonl`, `.001.jsonl`, ...; reading and downloading treat them as one stream. `GET /api/v1/recordings/{id}/download` supports `Range`, also while the recording runs.
- Limits: 64 MiB per recording, 256 MiB for all, 7 days (daemon flags `--max-recording-mb`, `--max-recordings-mb`, `--rotate-recording-mb`). A recording that reaches one is stopped with reason `limit`.
- On the daemon a recording continues when the browser closes or the tunnel drops. After a daemon restart, a recording that was cut off is listed as `interrupted`, counted again from its files, and downloadable.
- Recordings are named, never addressed by path: `[a-zA-Z0-9._-]{1,64}`, not starting with a dot.

#### Read-only, enforced

The Explorer sends register reads (Modbus function codes 3 and 4) and nothing else. A guard stops any other function code before it is sent, the build fails when non-test code calls a write, and tests capture the wire to prove it, also through the daemon's API. See `SECURITY.md`.

#### Remote use: daemon + SSH port forward + browser

Nothing is installed on the workstation. Start `sunspec-explorerd` on the gateway, run `ssh -N -L 8765:127.0.0.1:8765 user@gateway`, and open the URL that `sunspec-explorerd url` prints. See `docs/REMOTE-USE.md`.

- The daemon listens on `127.0.0.1` only by default and still requires a bearer token on every request. The token is created on the first start (file mode 0600), reused across restarts and replaced with `sunspec-explorerd token rotate`, without a restart. Any other listen address needs `--allow-remote-bind` and is warned about.
- Requests for a foreign host name are refused (DNS rebinding); the page runs under a strict content security policy; the token reaches the page in the URL fragment only and is kept in memory.
- The daemon connects only to allowed networks (`--allow-target`; by default private and link-local ranges).
- When the tunnel drops, the daemon keeps its sessions and keeps polling and recording; the page shows the lost connection and resumes where it stopped, or says that events were missed. A page that is opened again picks up the session that is still open.
- A systemd unit and a container image (`ghcr.io/otfabric/sunspec-explorerd`, amd64, arm64, armv7) are included.

### Downloads

| File | For |
|---|---|
| `sunspec-explorer-0.0.1-darwin-arm64.zip`, `-darwin-amd64.zip` | macOS 13 or later: `SunSpec Explorer.app` (Apple silicon, Intel) |
| `sunspec-explorer-0.0.1-windows-amd64.zip` | Windows 10/11 with the WebView2 runtime (part of Windows 11) |
| `sunspec-explorer-0.0.1-windows-arm64.zip` | Windows 11 on ARM. Best effort: untested, and absent when its build failed; the amd64 build runs there under emulation |
| `sunspec-explorer-0.0.1-linux-amd64.tar.gz`, `-linux-arm64.tar.gz` | Linux desktops with GTK 4 and WebKitGTK 6.0 (Debian 13, Ubuntu 24.04 or later) |
| `sunspec-explorerd-0.0.1-linux-amd64.tar.gz`, `-linux-arm64.tar.gz`, `-linux-armv7.tar.gz` | The daemon, with the systemd unit and the remote-use guide |
| `sunspec-explorer-0.0.1-checksums.txt` | SHA-256 of every file above |

The daemon as a container image: `docker pull ghcr.io/otfabric/sunspec-explorerd:0.0.1`.

Install notes, including the steps macOS and Windows ask for with unsigned software, are in the README.

### Not in this release

- **Serial targets** (`rtu://`) and Modbus over TLS (`tcp+tls://`).
- CSV and device-file exports.
- **Code signing.** The downloads are not signed or notarized: macOS and Windows warn before the first start. Each file has a SHA-256 checksum and a build provenance attestation instead.
- Installers and packages (dmg, msi, deb, rpm, AppImage): the desktop application comes as an application bundle (macOS), an executable (Windows) or a plain binary (Linux). The Windows executable has no icon of its own yet.
- A desktop build for Debian 11 or 12 (they lack WebKitGTK 6.0): use the daemon and a browser there.

### Built with

go-sunspec v0.5.1 (112 SunSpec models), go-modbus v1.4.2, Wails v3.0.0-beta.29.

---
