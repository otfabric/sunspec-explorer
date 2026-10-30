# Testing

No hardware: Go tests run against go-sunspec's `testutil` fixture server or an in-process `sunspec.Server`. `docs/ARCHITECTURE.md` section 11 gives the strategy; this file says what exists and how to run it.

## Tiers

| Tier | Command | What | In CI (`.github/workflows/build.yml`) |
|---|---|---|---|
| Go tests with race | `make test` | Every package; the Wails shell needs its platform's libraries | as `make coverage-check` |
| Coverage gate | `make coverage-check` | The `TEST_PKGS` in the Makefile, at least `COVERAGE_MIN` (90%) | yes |
| Frontend | `make frontend-check` | Typecheck and Vitest | yes |
| Web UI smoke | `TestWebUISmoke` in `internal/api` (runs `frontend/smoke/daemon.smoke.tsx`, `npm run test:smoke`) | The React application against a real daemon with a simulated device | yes, in the Go job |
| Build checks | workflow steps | Daemon is static, has no Wails (A12), runs without a display on Debian 11, 12 and 13; the image runs (A18); web bundle free of the Wails runtime | yes |
| Vulnerabilities | `make vuln` | govulncheck | yes |

`make check` runs fmt, fmt-check, vet, lint, lint-ci, vuln, test, coverage-check and frontend-check.

## Not automated here

The 24-hour soak (A14) has no test in this repository, and there is no Go fuzz target. `docs/ARCHITECTURE.md` section 11 lists a chaos proxy for fault injection; no test of that kind exists.

## Contract and read-only tests

- `TestContract` in `internal/api` is the service contract suite (`docs/ARCHITECTURE.md` section 3).
- `TestFunctionCodes` in `internal/readonly`, and `TestWireOnlyReads` and `TestWireOnlyReadsThroughAPI`, hold the read-only guarantee on the wire. `.golangci.yml` bans `Write*` and go-sunspec's `Server` outside tests.
- `TestNoWailsNoCgo` in `cmd/sunspec-explorerd` runs `go list` to check that the daemon links no Wails package and nothing that needs cgo (A12).
- `schema_test.go` in `internal/api` validates output against `docs/schema/`.

## Coverage policy

`TEST_PKGS` is the engine, the daemon and what they are built from. `cmd/sunspec-explorer` and `internal/gui` are excluded: the Wails shell needs a window. `codecov.yml` ignores `cmd/**`, `internal/gui/**` and `frontend/**`.

## Skips

`go test -short` skips the tests that run npm or `go list`. `TestWebUISmoke` also skips without npm or `frontend/node_modules`: run `make frontend-install` first, or the smoke test does not run. File-mode tests skip on Windows.
