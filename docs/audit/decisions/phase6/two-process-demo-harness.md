# Decision: how (and whether) to run the two-process cmd/msync demo over loopback

- Area: phase6 / evidence-generator
- Date: 2026-06-29
- Status: accepted

## Context

After the in-process integration tests (the evidence the task explicitly
*prefers*), the task asks to **also** run two real `cmd/msync` daemons against
two folders over loopback "if cmd/msync can". Two facts constrain how:

1. `cmd/msync` initiates connections **only** via UDP-multicast discovery — there
   is no `-dial` flag (cmd/msync/main.go: the engine dials a peer solely from an
   `onPeerDiscovered` event, engine.go:531). Discovery deliberately skips
   loopback and needs a real UP, multicast-capable, non-loopback IPv4 interface
   (discovery/multicast.go:87 `defaultMulticastInterface`, with the comment "lo0
   does not deliver the group"). This host has an active such interface
   (`en0 = 192.168.1.26`), so two same-host processes can join the group and rely
   on multicast loopback.
2. Peers must mutually trust via `-peer <hex DeviceID>` (TOFU allow-list,
   PR-7; cmd/msync/main.go:82-90). Each daemon's DeviceID is derived from a
   persisted identity in its config dir (transport.LoadOrCreateIdentity), so the
   two IDs are not known until each daemon has minted its identity — a
   chicken-and-egg for the `-peer` flags.

## Options (scored: correctness / concurrency-safety / testability / cross-platform)

### A. Two-pass demo with persisted identities
Pass 1: start each daemon briefly so it mints + logs its DeviceID (persisted in
its config dir), then stop it. Pass 2: restart both with `-peer <other-id>`; they
discover each other on `en0`, dial, handshake, and converge. Diverge the two
folders before pass 2 so the startup scan + index exchange has work to do.
- correctness: **high** — exercises the real daemon end-to-end: discovery → TLS
  1.3 handshake → reconcile, across two OS processes and two real folders.
- concurrency-safety: n/a (separate OS processes; not a `-race` target).
- testability: **medium** — depends on a live multicast interface; deterministic
  enough with short announce intervals and a convergence poll on the two dirs.
- cross-platform: **medium** — multicast/firewall behaviour differs on Windows;
  that gap is precisely what the CI matrix + CROSS_PLATFORM_CHECKLIST cover, so
  the Mac demo is illustrative, not the cross-OS proof.

### B. Add a `-dial <addr>` flag to cmd/msync for the demo
- correctness: medium — changes the production CLI surface purely to make a demo
  easier; the real daemon connects via discovery, so this would test a path users
  do not use.
- testability: high.
- Rejected: scope creep into production code during Phase 6.

### C. Skip the two-process demo entirely; rely on in-process tests
- correctness: high for protocol convergence, but forgoes the real-process
  discovery + daemon evidence the task asks for *when feasible* (and it is
  feasible here).

## Decision

**Option A**, best-effort. Run the two-pass persisted-identity demo over `en0`
multicast and capture logs to `docs/audit/runs/two-process-demo.log`. If
multicast does not deliver between the two processes in this environment, record
that as an environment limitation in the run log and fall back to the in-process
integration tests as the primary evidence (which the task prefers). **Do not** add
a `-dial` flag (reject B — no production change for a demo).

## Rationale

A two-process run is the one thing the in-process tests cannot show: that the
actual daemon wires discovery, transport, and reconcile together correctly across
process boundaries and two independent folders. Persisted identities solve the
`-peer` chicken-and-egg without touching production code. Keeping it best-effort
honours the README's "green on the Mac is necessary but not sufficient" — the
authoritative cross-OS signal remains the windows-latest CI job and the manual
checklist.

## Consequences

- A scratch orchestration script (kept under the session scratchpad, not
  committed) runs the two passes and tees daemon stdout/stderr.
- The committed evidence is the captured `docs/audit/runs/two-process-demo.log`
  plus a per-scenario note. The demo is environment-dependent; its absence or
  failure never invalidates the in-process suite.
