# Decision: tombstone retention period + garbage collection

- Area: protocol (Phase 2 — protocol-researcher)
- Status: decided (closes synthesis **OQ-6**)
- Date: 2026-06-28
- Decider: protocol-researcher (Phase 2)

## Context

A deletion is a **tombstone**: the `FileInfo` is kept with `deleted=true` and the
deleting device's VV counter bumped (SR-9; `SetDeleted` —
`bep_fileinfo.go:588-594`). The tombstone must dominate any stale peer's pre-delete
version so the file is *removed*, not *resurrected* (SR-10). The open question is
**how long to keep a tombstone before garbage-collecting it.**

The hazard is asymmetric and severe: GC a tombstone **too early** and a peer that
was offline during the delete reconnects holding the old file; with no tombstone to
dominate its version, the file **resurrects** (and propagates back to the deleter) —
"Consumers replaying old messages without applying tombstones can reintroduce
deleted data" (`sync-rules.md` SR-10, citing Cassandra tombstone management,
accessed 2026-06-28). This is *exactly* one of the #10590 symptoms
("deleted files resurrect as clean copies", accessed 2026-06-28). Keeping
tombstones **forever** is always safe for correctness but accretes state without
bound.

For a **2-device LAN tool** the safe retention condition is precise and cheap to
evaluate: a tombstone may be GC'd only once the **other peer is known to also hold
the deletion** (its VV for that path dominates-or-equals the tombstone's VV, observed
via its `INDEX`/`INDEX_UPDATE`). After that, no *live* peer can carry a pre-delete
version, so the tombstone has done its job.

## Options (scored 1–5; axes = correctness / concurrency-safety / testability / cross-platform)

### Option A — ack-gated retention: keep until the peer has acknowledged the deletion, then GC; symmetric (CHOSEN)
A tombstone is retained until the engine observes, in the peer's advertised index,
a VV for that path that **dominates or equals** the tombstone's VV (i.e. the peer
has applied the delete). Only then may **both** peers GC it. GC is symmetric: a peer
GCs only after it has both *sent* its tombstone and *seen* the peer's acknowledgement
— so the two never end up one-with-one-without (the FM-3 unequal-state hazard).
- correctness **5** — GC happens strictly after the deletion is durably replicated,
  so resurrection (SR-10) is impossible by construction; bounded retention.
- concurrency **5** — evaluated by the single writer under the `RWMutex` (GR-5) from
  already-exchanged index state; no new wire round-trip needed.
- testability **5** — deterministic: "delete on A; until B's index shows the
  tombstone, A keeps it; after, both GC; a partitioned-B reconnect before GC still
  gets deleted, and **after a (correctly gated) GC B can never have a pre-delete
  version**" — the SR-10 scenario.
- cross-platform **5** — pure state logic.

### Option B — time-based retention (keep N days, then GC)
- correctness **2** — fragile: a peer offline **longer than N** reconnects with the
  pre-delete file and resurrects it (SR-10 broken). Syncthing keeps deleted
  `FileInfo`s precisely to avoid this; a fixed TTL re-opens the hole
  (`syncthing-bep` §10.5). Picking N trades a resurrection risk against unbounded
  state — a bad trade when the ack signal is cheaply available on a 2-device LAN.
- concurrency **5**, testability **3** (time-dependent), cross-platform **5**.
- **Rejected** for the resurrection risk; ack-gating dominates it on the only axis
  that matters (correctness) at no cost.

### Option C — never GC (retain all tombstones forever)
- correctness **5** (resurrection impossible), concurrency **5**, testability **5**,
  cross-platform **5**.
- **Cost:** state grows without bound (one retained `FileInfo` per file ever
  deleted). For a small personal folder this is *survivable*, but it is strictly
  worse than Option A, which is just-as-safe **and** bounded. Kept as the trivially-
  correct fallback if the ack signal is ever unavailable (e.g. a single-device run).

## Decision

Adopt **Option A**: **ack-gated tombstone retention.** Retain a tombstone until the
peer's advertised VV for that path dominates-or-equals the tombstone's VV (the peer
has applied the deletion); then both peers may GC it, symmetrically. **Never** GC on
a timer. If no peer is currently paired/known, fall back to Option C (retain) — never
to Option B. The tombstone's VV is **never** pruned while it is retained
(`vv-pruning-counter-cleanup.md`).

## Rationale

- The ack signal is already on the wire for free (the peer's `INDEX`/`INDEX_UPDATE`),
  so we get *bounded* retention with **zero** resurrection risk — strictly better
  than both a TTL (risky) and never-GC (unbounded).
- Ties the GC trigger to the *exact* safety condition SR-10 requires ("no live peer
  can carry a pre-delete version the tombstone doesn't dominate"), rather than to a
  proxy (elapsed time) that can be wrong.
- Symmetric GC keeps both peers' tombstone sets equal, avoiding the FM-3 unequal-state
  trap that would otherwise turn a deletion into a spurious conflict.

## Consequences

- Drives `internal/reconcile/tombstone.go` (retain set, ack check against the peer's
  last index, symmetric GC) and shares the "peer has acknowledged state ≥ X" primitive
  with `vv-pruning-counter-cleanup.md`.
- **Interplay with VV pruning:** GC removes the whole tombstone `FileInfo`; counter
  pruning (`DropCounter`) removes a *dead device's* counter from surviving vectors.
  Both are ack-gated; never GC a tombstone or drop a counter that a live peer still
  needs.
- For >2 devices (out of current scope, N6) the ack condition generalises to "all
  live peers have acknowledged"; documented so a future expansion doesn't silently
  reintroduce resurrection.
- **Test obligations:** the SR-10 partition-delete-reconnect scenario (no
  resurrection); a "GC only after ack" assertion; a "premature GC ⇒ resurrection"
  negative test proving the gate is load-bearing.
- Cross-references: SR-9, SR-10; `syncthing-bep` §10.5; `version-vectors` FM-1;
  issue #10590 (resurrection symptom); `vv-pruning-counter-cleanup.md`; OQ-6;
  and OQ-5 (the persisted snapshot must store tombstones too, so a restart doesn't
  forget a not-yet-acked deletion).
