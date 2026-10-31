# Testing

No hardware: Go tests run against go-sunspec's `testutil` fixture server or an in-process `sunspec.Server`. `docs/ARCHITECTURE.md` section 11 gives the strategy; this file says what exists and how to run it.

## Tiers

| Tier | Command | What | In CI (`.github/workflows/build.yml`) |
|---|---|---|---|
| Go tests with race | `make test` | Every package; the Wails shell needs its platform's libraries | as `make coverage-pkg-check` |
| Coverage gate | `make coverage-pkg-check` | The `TEST_PKGS` in the Makefile, at least `COVERAGE_MIN` (90%) in total and `COVER_PKG_MIN` per package | yes |
| End-to-end | `make e2e` | `e2e/` builds `sunspec-explorerd` and drives it as a process: token, a session on a simulated device over TCP, snapshot, refused targets, SIGTERM | yes, in the Go job |
| Frontend | `make frontend-check` | Typecheck and Vitest | yes |
| Web UI smoke | `TestWebUISmoke` in `internal/api` (runs `frontend/smoke/daemon.smoke.tsx`, `npm run test:smoke`) | The React application against a real daemon with a simulated device | yes, in the Go job |
| Fuzz | `make fuzz` (`FUZZTIME`, default 30s per target) | Every `Fuzz*` target in the `TEST_PKGS`, discovered; see below | `fuzz` job in `ci.yml`, 10 s per target; `nightly.yml`, 5 min |
| Chaos | `make chaos` (`CHAOS_ITERATIONS`, default 25) | `TestChaosPolling` with that many seeds. `make test` runs it with 2 | `nightly.yml`, 500 seeds |
| Soak | `make soak` (`SOAKTIME`, default 2m) | `TestSoak`, skipped unless `SUNSPEC_EXPLORER_SOAK` is set | `nightly.yml`, 30 min |
| Build checks | workflow steps | Daemon is static, has no Wails (A12), runs without a display on Debian 11, 12 and 13; the image runs (A18); web bundle free of the Wails runtime | yes |
| Vulnerabilities | `make vuln` | govulncheck | yes |

`make check` runs fmt, fmt-check, vet, lint, lint-ci, vuln, test, coverage-pkg-check and frontend-check.

`ci.yml` runs the fuzz job itself: the shared `go-ci` workflow, and with it its `fuzz-time` input, cannot build this repository (CI-002 in `.otfabric/exceptions.yaml`).

## Fuzz targets

| Package | Target | Input |
|---|---|---|
| `internal/engine` | `FuzzDeviceRegisters` | A device's register image, read over Modbus/TCP: discovery, snapshot against the published schema, CSV, device file |
| | `FuzzDecodeModel` | Registers of any model in the registry: view, JSON, CSV |
| | `FuzzParseTarget` | A target configuration: the stored form means the same and go-modbus accepts it |
| | `FuzzRecordingStore` | A sidecar and a recording file left in the data directory |
| | `FuzzRecordingCSV` | A recording file converted to CSV |
| | `FuzzRecordingName` | A recording name a client sends |
| | `FuzzLoadTLS` | The PEM files of a TLS identity |
| `internal/api` | `FuzzRequest` | Method, path, Host, Authorization, Range and body of a request |
| | `FuzzParseRange` | A Range header |
| `internal/daemon` | `FuzzReadToken`, `FuzzReadState` | The token file and the state file |
| | `FuzzListenAddress`, `FuzzTargetAllowList`, `FuzzSerialAllowList` | The flags `--listen`, `--allow-target`, `--allow-serial` |
| `internal/settings` | `FuzzSettingsFile` | The desktop application's settings file |
| `internal/web` | `FuzzHandlerPath` | A path asked of the web UI |

`go-modbus` fuzzes its own frame parsing. A failing input lands in `testdata/fuzz/<target>/` of its package: commit it with the fix, and `make test` replays it for good.

## Chaos and soak

`TestChaosPolling` (`internal/engine/chaos_test.go`) puts a seeded proxy between the engine and a simulated device. The proxy drops, delays, corrupts and truncates answers, closes connections, sends other transaction numbers, garbage, exceptions and split frames, then goes silent or refuses connections. The test's doc comment states the invariants. The N-th request of a seed meets the same fault on any machine; `SUNSPEC_EXPLORER_CHAOS_SEED=<seed>` replays a seed, and `SUNSPEC_EXPLORER_CHAOS_ITERATIONS` sets how many run.

`TestSoak` (`internal/engine/soak_test.go`) polls and records through the same proxy at a low fault rate, rolling recordings over, and compares live heap, goroutines and descriptors late in the run with early in it. `SUNSPEC_EXPLORER_SOAK=<duration>` runs it.

## Not automated here

The 24-hour soak of A14 at a 1 s interval: `TestSoak` compresses it, polling at 2 ms, and a hosted runner gives it 5 hours at most (`nightly.yml`, input `soak-time`). A real 24-hour run is `make soak SOAKTIME=24h` on a machine of your own.

## Contract and read-only tests

- `TestContract` in `internal/api` is the service contract suite (`docs/ARCHITECTURE.md` section 3).
- `TestFunctionCodes` in `internal/readonly`, and `TestWireOnlyReads` and `TestWireOnlyReadsThroughAPI`, hold the read-only guarantee on the wire. `.golangci.yml` bans `Write*` and go-sunspec's `Server` outside tests.
- `TestNoWailsNoCgo` in `cmd/sunspec-explorerd` runs `go list` to check that the daemon links no Wails package and nothing that needs cgo (A12).
- `schema_test.go` in `internal/api` validates output against `docs/schema/`.

## Coverage policy

`TEST_PKGS` is the engine, the daemon and what they are built from. `cmd/sunspec-explorer` and `internal/gui` are excluded: the Wails shell needs a window. `codecov.yml` ignores `cmd/**`, `internal/gui/**` and `frontend/**`.

## Skips

`go test -short` skips the tests that run npm or `go list`, and `TestChaosPolling`. `TestWebUISmoke` also skips without npm or `frontend/node_modules`: run `make frontend-install` first, or the smoke test does not run. File-mode tests skip on Windows.
