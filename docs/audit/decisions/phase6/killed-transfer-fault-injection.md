# Decision: how to interrupt a real two-instance transfer for the killed-transfer scenario

- Area: phase6 / evidence-generator
- Date: 2026-06-29
- Status: accepted

## Context

The killed-transfer scenario (plan WS-4 #3; SR-1 / SR-2 in
`docs/audit/rules/sync-rules.md`) must prove **at the two-instance integration
level** that interrupting a transfer mid-stream leaves no corrupt or partial
destination file and no leftover temp, and that the transfer recovers on
reconnect. The unit suite already proves the atomic primitive in isolation
(`internal/reconcile/reconcile_test.go`: `TestAtomicWriteVerify_KillMidStreamLeavesDstUntouched`,
`TestAtomicWriteVerify_HashMismatchRejected`, `TestAtomicWriteVerify_SuccessAndReRunCompletes`,
transfer.go:72 `atomicWriteVerify`). What is missing is an *integration* test
where two real engines move bytes over real TLS framing and the wire is severed
mid-stream — exercising the production teardown path (puller ctx-cancel →
`fetchOverWire` returns `ErrPeerGone` → `atomicWriteVerify` defer discards the
temp), not a hand-rolled `fill` error.

The transfer is stop-and-wait over 32 KiB blocks (transfer.go:22 `BlockSize`,
:256 `fetchOverWire`); the receiver (puller) creates a `.msync-*.tmp` in the
destination directory and only renames after the whole-file SHA-256 verify
(transfer.go:90-108). So a genuine mid-stream cut must land after the TLS+HELLO+
INDEX bytes and before the last block.

## Options (scored: correctness / concurrency-safety / testability / cross-platform)

### A. Loopback TCP cut-proxy middlebox
The receiver dials a loopback proxy that forwards to the source's real listener
and severs **both** directions once a cumulative byte threshold is crossed
(threshold set above the handshake/index bytes, below the file size). Real TLS,
real framing, real engines, production teardown; deterministic cut by byte count.
- correctness: **high** — the real wire path is interrupted; verify-before-rename
  and temp-discard run exactly as in production.
- concurrency-safety: **high** — the proxy is independent goroutines; the engine
  side is the unchanged production teardown; `-race` clean.
- testability: **high** — the byte threshold makes the cut deterministic; the
  full SR-1 triad (dst untouched / no temp / re-run completes) is assertable.
- cross-platform: **high** — pure `net` loopback; runs identically on the
  windows-latest CI runner.

### B. Close the source transport mid-transfer (`a.tp.Close()`)
Start the fetch, then close the source transport to drop the connection.
- correctness: **medium** — it interrupts, but the source transport is now dead;
  the recover-on-reconnect step (SR-1 "(c) a re-run completes") cannot be tested
  without reconstructing the whole source node.
- concurrency-safety: high.
- testability: **medium** — timing the `Close` to land mid-stream is racy; no
  recovery assertion.
- cross-platform: high.

### C. Engine-level fake `peerConn` that serves K blocks then reports gone
Drive `materialise()` with a recording fake (the existing `fakeConn` pattern)
that declines/short-chunks after K blocks.
- correctness: **medium** — bypasses real TLS + framing; this is what the existing
  *unit* tests already do.
- concurrency-safety: high.
- testability: high.
- cross-platform: high.
- But: duplicates existing unit coverage and adds **no** integration-level
  evidence — the explicit point of the evidence-generator phase
  (plan/agent_roster.md: "spins up two msync instances ... killed-transfer").

## Decision

**Option A — a loopback TCP cut-proxy** added to `test/integration`. The receiver
dials the proxy; the proxy forwards to the source and closes both legs after a
cumulative byte count that clears the TLS handshake + HELLO + INDEX and lands
inside the chunk stream of a multi-MiB file. After the cut, assert the SR-1 triad
on the receiver, then dial the source **directly** and assert byte-exact
convergence.

## Rationale

Only A interrupts a **real two-engine** transfer *and* supports the
recover-on-reconnect assertion, which is the unique evidence this phase must add
on top of the existing unit tests. B cannot test recovery; C re-tests the unit
path through a fake. A runs the production teardown (peer-disconnect →
ctx-cancel → temp discard) verbatim, so a regression in that path would fail the
test — which is exactly the integration signal we want.

## Consequences

- Adds a `cutProxy` helper to `test/integration/helpers.go` (`net`, `sync`,
  `sync/atomic`). ~60 LoC, no production change.
- The cut threshold is chosen empirically well above the handshake bytes
  (TLS 1.3 + small ECDSA cert + HELLO + one-file INDEX ≪ 96 KiB) and well below
  the test file size (4 MiB), so the interruption is reliably mid-stream. The
  proxy counts ciphertext bytes, which is sufficient for a byte-threshold cut.
- The recovery dial uses the source's real listener address (bypassing the dead
  proxy), so the second connection is a clean production path.
