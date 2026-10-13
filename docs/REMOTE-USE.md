# Remote use

The Explorer reaches a device behind a gateway with the daemon and a browser. Nothing is installed on your workstation, and the desktop application is not involved (it has no remote mode).

```
workstation                        gateway                       device
browser ── ssh -L (encrypted) ──►  sunspec-explorerd ── Modbus ──►  inverter
           127.0.0.1:8765          127.0.0.1:8765        read-only
```

## Three steps

1. **On the gateway, start the daemon.**

   ```sh
   sunspec-explorerd
   ```

   It listens on `127.0.0.1:8765` only and prints one line with its URL. To see the URL again at any time:

   ```sh
   sunspec-explorerd url
   ```

   ```
   http://127.0.0.1:8765/#token=q3v…
   From a workstation, first run: ssh -N -L 8765:127.0.0.1:8765 <user>@gateway-17
   ```

2. **On your workstation, forward the port.**

   ```sh
   ssh -N -L 8765:127.0.0.1:8765 user@gateway
   ```

   Keep it running. If port 8765 is taken on your workstation, use another local port (`-L 9000:127.0.0.1:8765`) and change the port in the URL to match.

3. **Open the URL in a browser.** Paste the whole URL, including the part after `#`. The page removes the token from the address bar and keeps it in memory only, so after a reload or in a new tab you paste the URL again.

In the page, connect to the device with an address **as the gateway sees it** (for example `tcp://192.168.1.100:502`).

## What to expect

- **The tunnel drops.** The daemon keeps its sessions and keeps polling. The page says "connection lost", retries, and when the tunnel is back it continues where it stopped: missed events are replayed, or the page tells you that some were missed if the gap was too long.
- **Recording.** A recording runs on the daemon and continues without the page. Recordings are kept in the data directory (64 MiB each, 256 MiB together by default: `--max-recording-mb`, `--max-recordings-mb`); save a copy through the page, which downloads it. After a daemon restart a recording that was running is listed as interrupted and can still be saved.
- **You close the tab.** Same: the session stays open on the daemon. Open the URL again and the page picks it up. Disconnect in the page to close it.
- **The token.** It is the only credential: treat the URL like a password. It is created on the first start, kept in a file only the daemon's user can read, and stays the same across restarts. Replace it with `sunspec-explorerd token rotate`: the old URL stops working within a second, without a restart.
- **Which devices.** By default the daemon connects only to private and link-local addresses (10/8, 172.16/12, 192.168/16, 169.254/16 and their IPv6 counterparts). Add others with `--allow-target <cidr>`, for example `--allow-target 127.0.0.0/8` for a device forwarded to the gateway itself.
- **Read-only.** The daemon never writes to a device; see [`SECURITY.md`](../SECURITY.md).

## Where the daemon keeps its files

By default in a temporary folder, `/tmp/sunspec-explorer-<uid>`: the token and the recordings. That is lost when the system clears `/tmp` — at the latest on reboot, and on Debian 13 `/tmp` is in RAM — after which the daemon makes a new token. For a gateway that should keep its token, pass `--data-dir` (the systemd unit below does).

The directory must belong to the daemon's user and be closed to everyone else (mode 0700); the daemon creates it that way and refuses anything else.

## As a service (systemd)

[`packaging/sunspec-explorerd.service`](../packaging/sunspec-explorerd.service) runs the daemon as its own user with `/var/lib/sunspec-explorer` as data directory:

```sh
sudo install -m 0755 sunspec-explorerd /usr/local/bin/
sudo useradd --system --no-create-home --shell /usr/sbin/nologin sunspec-explorer
sudo install -m 0644 sunspec-explorerd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now sunspec-explorerd
sudo -u sunspec-explorer sunspec-explorerd url --data-dir /var/lib/sunspec-explorer
```

Two things to know:

- **Never add `PrivateTmp=yes` to the unit** if you use the default data directory: systemd deletes a private `/tmp` whenever the service stops, and the token and recordings with it.
- **The startup line with the URL goes to the journal** (`journalctl -u sunspec-explorerd`). Whoever may read the journal can read the token. Restrict journal access accordingly, or rotate the token after the first start and use `sunspec-explorerd url` from then on.

## Debian 11, 12 and 13

The daemon is one static binary per architecture (amd64, arm64, armv7) without any library dependency: the same file runs on Debian 11 (Bullseye), 12 (Bookworm) and 13 (Trixie). It needs no display and no GTK or WebKit.

| | Debian 11 | Debian 12 | Debian 13 |
|---|---|---|---|
| Daemon (`sunspec-explorerd`) | yes | yes | yes |
| `/tmp` | on disk, cleared at boot | on disk, cleared at boot | **in RAM (tmpfs)**, also cleared of old files while running: use `--data-dir` for anything to keep |
| Desktop application on the same machine | no | only a build with the legacy GTK3 tag | expected to work (WebKitGTK 6.0) |
| Container image | yes | yes | yes |

On a Debian 11 or 12 workstation, where the desktop application does not run, use the daemon locally in the same way: start it, open its URL, no SSH needed.

## In a container

The daemon is also delivered as a container image (`packaging/Dockerfile`; multi-architecture: amd64, arm64, armv7). It is the quickest way to try the daemon and the browser UI on macOS or Windows, and an alternative to the systemd unit on a gateway that runs containers.

```sh
make docker                                   # or: docker build -f packaging/Dockerfile -t sunspec-explorerd .
docker run -d --name explorer -p 127.0.0.1:8765:8765 -v explorer-data:/data sunspec-explorerd:dev
docker exec explorer sunspec-explorerd url    # http://127.0.0.1:8765/#token=...
```

Open the printed URL in a browser. `make docker-run` does the build and the run in the foreground.

- **Publish the port on `127.0.0.1` only**, as above. Inside a container the daemon has to listen on every address for the published port to reach it (the image starts it with `--listen 0.0.0.0:8765 --allow-remote-bind`, and it warns about that at start); `-p 8765:8765` without the address would expose the unencrypted listener on the host's network. On a gateway, combine `-p 127.0.0.1:8765:8765` with the SSH port forward exactly as without a container.
- **The URL names port 8765.** If you publish another host port (`-p 127.0.0.1:9000:8765`), change the port in the URL. The daemon accepts any port, but only the loopback host names.
- **Data**: the token and the recordings live in `/data`. Use a named volume, as above, to keep the token across `docker rm`. A bind-mounted host folder must belong to the container's user (uid 65532) and have mode 0700, or the daemon refuses it; on macOS a named volume avoids that.
- **Reaching devices from the container.**
  - A device on the network: its address as usual (`tcp://192.168.1.100:502`). On Docker Desktop (macOS, Windows) the container reaches the host's networks through NAT.
  - A simulated device on your Mac: let it listen on every address and use Docker's name for the host, which resolves to a private address the default allow-list accepts:

    ```sh
    sunspecctl serve --url tcp://0.0.0.0:15020 -f device.json
    # in the page: tcp://host.docker.internal:15020, unit ID 1
    ```

  - On Linux, `--network host` gives the container the host's network (and loopback bind works again: add `--listen 127.0.0.1:8765` and drop `-p`).
- **Serial ports** (when serial targets exist) need the device passed in: `--device /dev/ttyUSB0`.
- The image holds the static binary and nothing else (distroless, no shell), and runs as a non-root user. `docker logs` shows the startup line with the URL: whoever may read the container's logs can read the token; rotate it with `docker exec explorer sunspec-explorerd token rotate`.

## Binding to another address

`--listen` accepts only loopback addresses unless you also pass `--allow-remote-bind`. The daemon then warns at start: its listener is plain HTTP, so the token crosses the network unencrypted. The token and the Host check still apply; name the hosts the daemon should answer to with `--allow-host`. Prefer the SSH forward.

## Not in this version

A built-in SSH tunnel (the application opening the forward itself) is an idea for v0.2. For now the forward is yours to run.
