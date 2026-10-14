# Contributing to sunspec-explorer

Thank you for your interest in contributing. Read [`AGENTS.md`](AGENTS.md) first: it lists the settled decisions and the hard rules, and points to the requirements and the architecture in [`docs/`](docs/).

## Development Setup

### Prerequisites

- Go 1.25+ (see [go.mod](go.mod)); Wails v3 needs it. CI and releases build with the current stable Go, so that the binaries carry its security fixes; `go.mod` names the oldest version that works
- Node.js 22+ and npm for the frontend
- [staticcheck](https://staticcheck.dev/), [golangci-lint](https://golangci-lint.run/) v2 and [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) for the checks
- For the desktop application: a C compiler and the platform's webview (macOS: Xcode command line tools; Windows: WebView2 runtime; Linux: GTK4 and WebKitGTK 6.0)
- No hardware: tests run against an in-process `sunspec.Server` from go-sunspec

### Build & Test

```sh
make frontend-install   # once: npm ci
make frontend           # build the UI twice: frontend/dist/wails and frontend/dist/web
make check              # format, vet, lint, vulnerability scan, race tests, coverage gate, frontend checks
make build              # ./bin/sunspec-explorer and ./bin/sunspec-explorerd
make dev                # run the desktop application from source
```

The Go code compiles and tests without a frontend build: a fresh checkout embeds placeholders. A binary needs `make frontend` first.

Run `make` (or `make help`) for the full list of targets.

### Trying it by hand

[`sunspecctl`](https://github.com/otfabric/sunspecctl) serves a simulated device from a JSON device file:

```sh
sunspecctl template --model 1,103,160 -o device.json    # then fill in some values
sunspecctl serve --url tcp://127.0.0.1:15020 -f device.json
```

Connect the desktop application (`make dev`) to `tcp://127.0.0.1:15020`, unit ID 1. For the daemon, allow loopback targets, which are off by default:

```sh
./bin/sunspec-explorerd --allow-target 127.0.0.0/8
./bin/sunspec-explorerd url
```

Or run the daemon in a container (`make docker-run`), serve the device on every address (`--url tcp://0.0.0.0:15020`) and connect to `tcp://host.docker.internal:15020`; see [`docs/REMOTE-USE.md`](docs/REMOTE-USE.md).

The program icon is generated: `make appicon` renders `cmd/sunspec-explorer/appicon.png`, and `make winres` builds the Windows resource files (`rsrc_windows_*.syso`: icon and manifest) from it. Both results are committed, so a normal build needs neither.

The screenshots and the demo in the README are made the same way, by `docs/media/record-demo.mjs` (usage in its header): rerun it when the user interface changes visibly.

This is for hands-on checks only: the automated tests must not depend on the `sunspecctl` binary.

## Making Changes

### Hard rules

- **Read-only.** No Modbus write, no function code other than 3 and 4, no device simulation outside `_test.go` files. The linter, the run-time guard (`internal/readonly`) and the wire test enforce it; do not weaken any of the three.
- **The daemon never imports Wails**, and `internal/` uses no cgo outside `internal/gui`.
- **The user interface talks to the `Service` interface only** (`internal/service` in Go, `frontend/src/app/service` in TypeScript), never to Modbus or HTTP directly.
- **Polling runs in the engine.** No timers in the frontend.
- **Colors come from the tokens** in `frontend/src/styles/tokens.css` (see [`docs/BRANDING.md`](docs/BRANDING.md)); check both themes.

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`); start every Go file with `// SPDX-License-Identifier: MIT`
- Comments end with a period (enforced by the linter)
- `make fmt` formats with the `gofmt` of the Go version in `go.mod`, the version CI uses

### Where a change belongs

Decoding, discovery, model definitions and the Modbus transports live in [go-sunspec](https://github.com/otfabric/go-sunspec) and [go-modbus](https://github.com/otfabric/go-modbus). A wrongly decoded value or a missing model is fixed there. This repository holds the engine (sessions, polling, events, exports), the daemon and the user interface.

### Tests

New behavior needs a test against the simulated device (`internal/engine/sim_test.go`, `internal/api/sim_test.go`). API behavior is tested through plain `net/http` requests (`internal/api/client_test.go`); there is no Go client package and none should be added. `go test ./internal/api` also runs the frontend's smoke suite against a daemon when Node and `frontend/node_modules` are present. The coverage gate is 90% of the engine and daemon packages.

## Releasing

Releases are made by the **Release** workflow (Actions tab, run by hand), never from a workstation. It follows the conventions of the other otfabric repositories.

1. Write the release notes at the top of [`RELEASE.md`](RELEASE.md): a `## v<version>` heading, the notes, and a line with only `---` after them. The workflow publishes everything between the heading and that line. Without such a section it publishes the list of commits since the previous tag.
2. Merge to `main` and wait for CI: it runs the same checks and builds as a release (`.github/workflows/build.yml`).
3. Run **Release** on `main` and choose the bump:
   - `patch`, `minor` or `major`: the new version is the latest `v*` tag plus the bump (`patch` makes v0.0.1 when there is no tag yet). The workflow builds everything, pushes the annotated tag `v<version>` and publishes the GitHub release.
   - `none`: rebuilds the files of the latest existing tag and replaces them in its release.

The workflow builds every deliverable on a runner of its own architecture (no QEMU; the armv7 daemon and image are Go cross-compilation on the arm64 runner), writes `sunspec-explorer-<version>-checksums.txt`, attests build provenance, and pushes the container image to `ghcr.io/otfabric/sunspec-explorerd` (`<version>` and `latest`). A tag, once pushed, is not moved: a release that went wrong is followed by the next patch.

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
