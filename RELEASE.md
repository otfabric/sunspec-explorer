# sunspec-explorer Releases

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
