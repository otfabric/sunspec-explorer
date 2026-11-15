# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in the SunSpec Explorer, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/sunspec-explorer/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue
- The affected version(s) (`sunspec-explorer --version` output)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### The Explorer Only Reads

The Explorer never sends a Modbus write, a diagnostic or any request other than a register read (function codes 3 and 4). This is enforced, not a convention:

- every Modbus client the engine creates is restricted by go-modbus's function-code allow-list to those two codes: the client refuses anything else before it reaches the wire (`internal/readonly`);
- the linter fails the build when non-test code calls a write or uses a device simulator (`.golangci.yml`);
- a test captures the wire across every workflow and asserts that only function codes 3 and 4 appear.

No interface of the Explorer accepts a raw Modbus frame, a function code, a register value to write or a file path.

### The Daemon

`sunspec-explorerd` listens on `127.0.0.1` only, and is still authenticated: other users of the gateway, and other pages in your browser, can reach a loopback port too.

- **Token.** Every API request needs a bearer token, compared in constant time and accepted in the `Authorization` header only. It is created on the first start, stored in a file with mode 0600, never logged, and shown only by `sunspec-explorerd url` and, when the daemon was started from a terminal, in one line at startup. A daemon under systemd or in a container does not write it: its journal and its logs hold no token. A URL with `#token=` is a credential: do not paste it into chats or tickets.
- **In the browser** the token arrives in the URL fragment, which is not sent to any server; the page removes it from the address bar and keeps it in memory only: no cookie, no local storage, no query string.
- **Host check.** Requests that name another host are refused, which stops DNS-rebinding attacks from web pages. No CORS headers are sent; the page's content security policy allows its own origin only.
- **Serial devices.** The daemon opens no serial device unless its operator names it (`--allow-serial`).
- **Targets.** The daemon connects only to the networks it is allowed to (`--allow-target`; by default private and link-local ranges), so that a token holder cannot use a gateway to probe other networks. A host name is checked when the session is opened; a name that later resolves elsewhere is not re-checked.
- **Other addresses.** Binding anything but loopback needs `--allow-remote-bind`. The listener is plain HTTP: on such an address the token is readable on the network. Use the SSH port forward instead.
- **Data directory.** Created with mode 0700; a symbolic link, another owner or wider permissions are refused.
- **Read-only evidence.** A request with any function code but 3 and 4 that some code path tried is refused before it is sent, logged once at error level, and counted in `read_only_violations` of `GET /api/v1/health`. Anything but 0 there is a defect: please report it.

### Resource Limits

What a client, a device and the daemon's operator can make the Explorer use. "Client" means a holder of the token (the web UI, the API); the desktop application applies the same engine limits.

| What | Limit | Set by |
|---|---|---|
| Open sessions | 4 | `--max-sessions` |
| Request body | 64 KiB | fixed |
| Time to send a request's headers | 10 s | fixed |
| Time to send a request's body | 30 s, from the end of its headers | fixed |
| Connection without a request | closed after 120 s | fixed |
| Open connections | not limited by the daemon; the bind rules decide who can connect, the token who is served | — |
| Event stream | a comment line every 15 s keeps it open; it lasts as long as its client stays | fixed |
| Poll interval | 1 s to 3600 s, default 5 s | client |
| Modbus request timeout | 100 ms to 60 s, default 3 s | client (`timeout_ms`) |
| Base addresses probed | at most 16 | client |
| Models read from a chain | at most 1024; 0 selects go-sunspec's default | client (`max_models`) |
| Pause between two model reads | 0 to 10 s, default 0 | `--request-delay` |
| Events kept for replay | 1024; of the sample events only the newest of each open session keeps its sample | fixed |
| Events a subscriber may fall behind | 256, then a `resync` | fixed |
| Size of a recording | 64 MiB, then it is stopped | `--max-recording-mb` |
| Size of all recordings | 256 MiB, then the running ones are stopped and none starts | `--max-recordings-mb` |
| Size of a recording file | 16 MiB, then the recording continues in a new file | `--rotate-recording-mb`; a client may ask for 64 KiB up to the size of a recording (`rotate_bytes`) |
| Free space left alone | 64 MiB | `--min-free-mb` (0 switches the check off) |
| Duration of a recording | 7 days | fixed |
| Line of a recording read back | 16 MiB | fixed |
| Recording name | 1 to 64 letters, digits, dots, dashes and underscores, not starting with a dot | fixed |
| Size flags (`--*-mb`) | at least 1 (`--min-free-mb`: 0), at most 8796093022207, the largest number of MiB whose bytes fit a 64-bit integer | — |
| Requests still running at a stop (SIGINT, SIGTERM) | 5 s, then they are cut off and the daemon exits with status 1 | fixed |

### SunSpec Modbus Has No Built-In Security

Modbus carries no authentication and no encryption: anyone who can reach a device can read and write it, and anyone on the path can read or alter the traffic.

- **Use the Explorer only on devices and networks you own or are authorized to access**
- **Keep Modbus traffic on isolated, trusted networks**; never expose port 502 of an inverter, meter or battery to the Internet

### Data Read From Devices Is Untrusted Input

Model IDs, lengths, strings and values come from the device. Discovery is bounded and lengths are validated before decoding; the user interface renders device strings as text, never as markup.

### Exports Are Operational Data

A snapshot export contains serial numbers, network addresses, settings and production figures. The Explorer warns before saving one; check a file before sharing or committing it.

### Dependencies

`make check` runs `govulncheck`. The protocol code is in [go-sunspec](https://github.com/otfabric/go-sunspec) and [go-modbus](https://github.com/otfabric/go-modbus). The desktop shell is [Wails v3](https://v3.wails.io), a prerelease pinned to an exact version in `go.mod`. The user interface bundles its fonts and makes no request to any other host.

### Verifying Downloads

Releases are not code-signed or notarized yet: macOS Gatekeeper and Windows SmartScreen warn before the first start (the README has the steps). Each release is published with a SHA-256 checksum file, and every file and the container image carry a build provenance attestation, which proves that the release workflow of this repository built them:

```sh
sha256sum -c --ignore-missing sunspec-explorer-<version>-checksums.txt
gh attestation verify <file> --repo otfabric/sunspec-explorer
gh attestation verify oci://ghcr.io/otfabric/sunspec-explorerd:<version> --repo otfabric/sunspec-explorer
```

Verify before installing, above all on a gateway.
