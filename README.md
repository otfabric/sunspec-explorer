# sunspec-explorer

OT Fabric SunSpec Explorer: a read-only desktop tool to discover, browse, poll, export and record SunSpec devices, built on [go-sunspec](https://github.com/otfabric/go-sunspec).

**Status: preview (v0.0.2 in preparation; v0.0.1 released).** Connect, discover, browse, read, poll, export and record work, in the desktop application and through the daemon. What a release contains is in [`RELEASE.md`](RELEASE.md). The requirements, architecture and branding are in [`docs/`](docs/). Agents start with [`AGENTS.md`](AGENTS.md); contributors with [`CONTRIBUTING.md`](CONTRIBUTING.md).

- [Remote use](docs/REMOTE-USE.md)
- [Requirements](docs/REQUIREMENTS.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Branding](docs/BRANDING.md)
- [Proposal review](docs/PROPOSAL-REVIEW.md)

## Two programs

- **`sunspec-explorer`**, the desktop application. It talks to devices itself; it has no remote mode.
- **`sunspec-explorerd`**, the headless daemon for gateways: a static binary that serves the same user interface to a browser.

## Install

Download from the [releases page](https://github.com/otfabric/sunspec-explorer/releases). The files are **not code-signed yet**; each release has a checksum file and build provenance instead:

```sh
sha256sum -c --ignore-missing sunspec-explorer-0.0.1-checksums.txt    # macOS: shasum -a 256 -c --ignore-missing ...
gh attestation verify <downloaded file> --repo otfabric/sunspec-explorer   # optional: proves it was built by this repository's release workflow
```

**Desktop application**

- **macOS** (13 or later): unzip and move `SunSpec Explorer.app` to Applications. The first start is blocked because the app is not notarized: open it once, then go to System Settings → Privacy & Security and choose "Open Anyway". Or remove the quarantine flag: `xattr -dr com.apple.quarantine "/Applications/SunSpec Explorer.app"`.
- **Windows** (10 or 11): unzip and run `sunspec-explorer.exe`. SmartScreen shows "Windows protected your PC": choose "More info", then "Run anyway". Windows 10 needs the [WebView2 runtime](https://developer.microsoft.com/microsoft-edge/webview2/); Windows 11 has it.
- **Linux**: unpack and run `sunspec-explorer`. It needs GTK 4 and WebKitGTK 6.0 (`sudo apt install libgtk-4-1 libwebkitgtk-6.0-4`), which Debian 13 and Ubuntu 24.04 or later provide. On Debian 11 or 12, use the daemon and a browser instead.

**Daemon** (gateways: Linux amd64, arm64, armv7; any Debian from 11 on)

```sh
tar xzf sunspec-explorerd-0.0.1-linux-arm64.tar.gz
sudo install -m 0755 sunspec-explorerd-0.0.1-linux-arm64/sunspec-explorerd /usr/local/bin/
```

or as a container: `docker pull ghcr.io/otfabric/sunspec-explorerd:0.0.1`. Running it as a service, and everything else about the daemon: [`docs/REMOTE-USE.md`](docs/REMOTE-USE.md).

## Remote use

Nothing is installed on the workstation:

```sh
gateway$      sunspec-explorerd                              # listens on 127.0.0.1:8765
workstation$  ssh -N -L 8765:127.0.0.1:8765 user@gateway     # forward the port
gateway$      sunspec-explorerd url                          # prints http://127.0.0.1:8765/#token=...
```

Open that URL in a browser on the workstation. Details, systemd and Debian notes: [`docs/REMOTE-USE.md`](docs/REMOTE-USE.md).

The daemon also comes as a container image, which is the quick way to try it on macOS:

```sh
docker run -d --name explorer -p 127.0.0.1:8765:8765 -v explorer-data:/data ghcr.io/otfabric/sunspec-explorerd:0.0.1
docker exec explorer sunspec-explorerd url
```

(`make docker` builds the image from source as `sunspec-explorerd:dev`.)

## Build

```sh
make frontend-install   # once
make dev                # build the UI and run the desktop application
make build              # ./bin/sunspec-explorer and ./bin/sunspec-explorerd
make bundle-macos       # ./bin/SunSpec Explorer.app (macOS)
make build-daemon-all   # the daemon for linux amd64, arm64 and armv7
make docker             # the daemon's container image
make check              # all checks
```

The Explorer only reads: it never sends a Modbus write. See [`SECURITY.md`](SECURITY.md).

License: MIT.
