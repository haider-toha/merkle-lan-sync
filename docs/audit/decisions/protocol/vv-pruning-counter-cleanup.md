# Decision: version-vector pruning / device-counter cleanup (ghost counters)

- Area: protocol (Phase 2 — protocol-researcher)
- Status: decided (closes synthesis **OQ-3**)
- Date: 2026-06-28
- Decider: protocol-researcher (Phase 2)

## Context

Each per-file version vector accretes one counter per device that has *ever*
authored that path. The agent contract names "VV growth / pruning as device counts
rise" as a deliverable. The decisive real-world evidence is Syncthing issue
**#10590** (verified 2026-06-28): *"Ghost version vector counters are never cleaned
up on device removal, causing deleted files to reappear and thousands of false
conflicts"* — **closed as not planned**, reported March 2026. Symptoms quoted from
the issue:

- "Deleted files resurrect as clean copies (no sync-conflict marker)" — a direct
  SR-10 violation: a removed device's ghost counter means *neither* vector can
  dominate, so a tombstone can't win.
- "8,591 files arrive as sync-conflict copies" on one device rebuild;
  "~40,000 conflicts on first rebuild" — conflict storms.
- Proposed fix in the issue: *"Add `Vector.DropCounter(shortID)`"* plus a
  removal-time sweep over `FileInfo` records.
  ([Issue #10590](https://github.com/syncthing/syncthing/issues/10590), accessed
  2026-06-28; also `version-vectors` FM-1, FM-3.)

The counter-tension is **FM-3 (unequally-pruned vectors)**: "two nodes … exchanging
version vectors [that] have not been equally pruned … may lead to a false reporting
of conflicts" ([Version Vector(III), LinkedIn](https://www.linkedin.com/pulse/version-vectoriii-pratik-pandey), accessed 2026-06-28). So pruning is **not
free** — done unilaterally it manufactures the very conflicts it aims to prevent.

For Merkle Sync the device count is small and bounded (LAN, ~2 devices), so growth
is a non-issue in the common case; the real risk is the **ghost counter of a
de-paired device**, which is permanent and cumulative.

## Options (scored 1–5; axes = correctness / concurrency-safety / testability / cross-platform)

### Option A — `DropCounter(id)`/`Compact(live)` API, applied **only on explicit device removal**, **ack-gated** and symmetric (CHOSEN)
A counter is removed from stored vectors **only** when its device is explicitly
removed from the allow-list (de-paired) — never on a timer, never by size. When the
operator removes device D:
1. The local engine records "D is being dropped."
2. It strips D's counter from every stored `FileInfo`'s VV (`DropCounter(D.Short())`,
   copy-on-write) **and** from the snapshot.
3. The remaining live peer is told (a `CLOSE`-with-reason or, more cleanly, the next
   `INDEX` simply no longer contains D's counter); the drop is only considered
   complete once **both** live peers have applied it (so vectors stay *equally*
   pruned — defeats FM-3).
- correctness **5** — kills the ghost-counter class (FM-1) at its only real trigger
  (device removal) while never pruning a *live* device's counter (so no SR-10
  resurrection from premature drop); equal/symmetric pruning avoids FM-3 false
  conflicts.
- concurrency **5** — `DropCounter` is copy-on-write (`version-vectors` §8 A4),
  applied by the single writer under the `RWMutex` (GR-5).
- testability **5** — deterministic: a "remove device, assert its counter is gone
  from every leaf and no spurious conflict arises on the next compare" test; a
  "drop on only one side ⇒ no silent divergence" negative test.
- cross-platform **5** — pure data operation.

### Option B — never prune (accept ghost counters)
- correctness **2** — exactly Syncthing's #10590 behaviour: removing a device leaves
  permanent ghost counters → deletions can't dominate (resurrection, SR-10 broken)
  and conflict storms over a cluster's lifetime. For a strict 2-device tool that
  *never* re-pairs it is survivable, but it bakes in the marquee long-lived bug the
  moment a device is ever replaced.
- concurrency **5**, testability **5**, cross-platform **5**.
- **Rejected** — cheap to do better; FM-1 shows the cost of not doing it.

### Option C — blind time/size-based pruning (drop old/low counters automatically)
- correctness **1** — FM-3: two peers prune differently → causal mistaken for
  concurrent → spurious conflict copies; and dropping a *live* device's counter can
  make a stale version falsely dominate → resurrection/loss. This is the
  anti-pattern.
- **Rejected outright.**

## Decision

Adopt **Option A**: provide `VersionVector.DropCounter(id)` (and a `Compact(live
[]ShortID)` convenience) from day one, copy-on-write. **Prune a counter only when
its device is explicitly removed from the allow-list**, apply the sweep to every
stored `FileInfo` and the snapshot, and treat the drop as complete only once **both
live peers have applied it** (symmetric/equal pruning). **Never** prune by time or
size; **never** drop a live device's counter.

## Rationale

- Builds the fix #10590 lacked **before** the bug can ever manifest, at trivial cost
  for a small device count.
- Tying pruning to the *one* event that creates a dead counter (device removal) means
  we never touch a live device's history, so SR-9/SR-10 dominance is preserved.
- Symmetric, ack-gated application keeps both peers' vectors equally pruned, which is
  the documented prerequisite for pruning not to manufacture false conflicts (FM-3).

## Consequences

- Drives `internal/protocol/versionvector.go` (`DropCounter`, `Compact`,
  copy-on-write) and a removal path in `internal/reconcile` that sweeps stored
  `FileInfo`s + the snapshot under the writer lock.
- **Couples to `tombstone-retention-gc.md`:** both are ack-gated state-cleanup
  operations and share the "only GC/prune after mutual acknowledgement" safety rule;
  implement them with one shared "has the peer acknowledged state ≥ X?" primitive.
- For v1 the operator action "remove a paired device" is the only pruning trigger;
  automatic/aged pruning is explicitly **out of scope** (and would be unsafe).
- **Test obligations:** remove-device sweep correctness; equal-pruning (one-sided
  drop must not cause divergence — assert it's gated); `-race` on copy-on-write.
- Cross-references: `version-vectors` FM-1/FM-3/§8 A2; issue #10590; SR-9/SR-10;
  `vv-counter-seeding.md`; `tombstone-retention-gc.md`; OQ-3.
