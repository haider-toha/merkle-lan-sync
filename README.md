# merkle-lan-sync

A decentralised LAN file sync with no central server. Two machines on the same
network mirror a folder between each other over raw TCP (TLS 1.3) for transfer
and UDP multicast for peer discovery. A Merkle tree is the source of truth for
what differs. Written in Go, built and tested for macOS and Windows.

> The module path is `github.com/haider-toha/merkle-sync` (the original name).
> The repository was renamed to `merkle-lan-sync`; GitHub redirects, so
> `go install` and `go build` resolve normally.

## How it works

Each peer scans its folder into a Merkle tree whose leaves carry a content hash,
file size and a version vector. On connect, peers exchange root hashes; if they
match, the folders are already in sync. If not, they walk the tree to find the
diverging leaves and request exactly those files.

Edits are ordered by version vector, not wall-clock mtime. When two peers edit
the same file concurrently, the losing side is renamed to a conflict copy rather
than overwritten. Deletions are tombstones, so a peer that was offline when the
delete happened cannot bring the file back when it reconnects.

Receiving a file is not a local change. Applying a remote update never bumps the
local counter and never triggers a broadcast, which breaks the watcher echo that
would otherwise bounce files between peers forever.

## Install

Requires Go 1.23.

```sh
git clone https://github.com/haider-toha/merkle-lan-sync
cd merkle-lan-sync
go build ./cmd/msync
```

Or install directly:

```sh
go install github.com/haider-toha/merkle-sync/cmd/msync@latest
```

The only non-stdlib dependencies are `github.com/fsnotify/fsnotify` and
`golang.org/x/text/unicode/norm`.

## Usage

```sh
msync -dir ~/sync -folder default -port 22000
```

Run the same command on each peer, pointing at the folder you want to keep in
sync. Both peers announce on UDP multicast, find each other on the LAN, perform
the TOFU handshake and begin reconciling.

To pin specific peers instead of trusting anyone on the multicast group, pass
their device IDs:

```sh
msync -dir ~/sync -peer 1a2b3c... -peer 4d5e6f...
```

### Flags

| Flag       | Default          | Description                                              |
|------------|------------------|----------------------------------------------------------|
| `-dir`     | `.`              | Folder to keep in sync                                   |
| `-port`    | `22000`          | TCP port for peer-to-peer sync                           |
| `-folder`  | `default`        | Folder ID shared by both peers (must match on both ends) |
| `-config`  | `<dir>/.msync`   | Where identity and snapshot are stored                   |
| `-peer`    | (none)           | Hex device ID of a paired peer; repeatable               |

On first run, `msync` generates a self-signed Ed25519 identity and prints the
resulting device ID. The identity and the tree snapshot live under `<config>/`.
Delete that directory to reset state.

## Two-machine demo

On the Mac:

```sh
./msync -dir ~/sync
```

On the Windows machine, build the executable and copy it across:

```sh
GOOS=windows GOARCH=amd64 go build ./cmd/msync
```

```sh
msync.exe -dir %USERPROFILE%\sync
```

Drop a file into `~/sync` on the Mac. It appears in `%USERPROFILE%\sync` on
Windows shortly after. Edit it on Windows, the Mac picks up the change. Delete
it on one side, it disappears on the other. Kill either side mid-transfer with
`Ctrl-C`; the partial file is discarded, the destination is untouched and
syncing resumes cleanly on restart.
