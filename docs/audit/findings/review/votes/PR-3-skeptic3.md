# PR-3 — Skeptic #3 vote (refute "FIXED")

- Date: 2026-06-29
- Verdict reviewed: `docs/audit/findings/review/PR-3.md` → **FIXED**
- My vote: **REFUTED** (the "no data-loss path found" claim is overstated; a real,
  untested data-loss edge case exists in the execute path)
- Confidence: medium

## What I confirmed is genuinely solid

The *resolver* layer is sound and well-evidenced:
- `aWins` (`internal/reconcile/conflict.go:30-40`) is total + commutative: tier-2 author
  comparison is symmetric (`aWins(a,b)` uses `authA<authB`, `aWins(b,a)` uses
  `authB<authA`, complementary when distinct; equal authors fall to the strict
  content-hash total order). `authorOf` is a pure function of both VVs.
- `conflictName` (`conflict.go:86-93`) is TZ- and nanosecond-rounding independent
  (`UTC().Truncate(time.Second)`), suffix uses the loser's author — identical on both
  peers because the loser leaf is content-addressed/replicated.
- `TestW_Commutative`, `TestConflict_CopyName*`, `TestResolver_*` back these.

So the *decision* of who wins / what the copy is named converges. No dispute there.

## Why I still refute FIXED — a destructive ordering hole the resolver proof does NOT cover

The no-data-loss invariant (SR-7) is not enforced by the pure resolver; it is enforced
by the *execution order* in `engine.go execute()` (`internal/reconcile/engine.go:662-686`).
Only ONE side ever materialises the conflict copy — the side that already holds the
loser's bytes (the loser's author, call it M). The other side (winner P) never has M's
bytes and waits for M to advertise the copy (comment `engine.go:673-679`). So **M is the
sole custodian of the losing version's only on-disk copy.**

On M, `execute` does two independent, NON-atomic enqueues:

```
if p.loser != nil && e.hasLocalContent(p.loser.ContentHash) {
    e.enqueueFetch(ps, *p.loser, true)   // make the copy (reads M's bytes from orig path)
}
... else {
    e.enqueueFetch(ps, p.winner, false)  // OVERWRITES orig path with P's bytes
}
```

`enqueueFetch` (`engine.go:716-726`) is a **non-blocking send that silently DROPS the
task when `fetchQ` (depth 256, `engine.go:24`) is full**, relying on
"the diff persists, so a later reconcile retries it" (`:723-724`).

That retry assumption is **false for the conflict-loser case**:

1. Under load (>256 pending fetches — a large initial sync with many concurrent edits),
   the loser-copy enqueue can hit the full-queue `default` and be DROPPED, while the
   winner enqueue — issued immediately after — succeeds because the single puller
   goroutine drained one slot in between (the puller runs concurrently with `execute`).
2. The puller then materialises the winner: `atomicWriteVerify` overwrites the original
   path with P's bytes (`transfer.go:230-243`). **M's only on-disk copy of the losing
   version is now gone.**
3. On the next `reconcileWithPeer`, `resolve` still returns `planConflict`, but now
   `e.hasLocalContent(p.loser.ContentHash)` is FALSE on M (M overwrote its own bytes) and
   was always FALSE on P → **neither peer ever creates the conflict copy.** Both ends
   converge to P's bytes at the path; M's version is **permanently lost.**

This is exactly the SR-7 violation the finding calls "the literal no-data-loss
contract." The destructive winner-overwrite is enqueued UNCONDITIONALLY; the
copy-out that protects the loser's bytes is best-effort and droppable. There is no
guard tying the winner-apply to a confirmed loser-copy (e.g. "only enqueue the winner
if the loser copy was enqueued/succeeded", or use a blocking/guaranteed enqueue for the
copy, or copy-out synchronously before scheduling the overwrite).

## Missing test

- `TestResolver_*` only assert `plan.kind == planConflict` (`reconcile_test.go:155-188`)
  — the PURE verdict, never the on-disk effect.
- The one end-to-end no-loss test (`test/integration/sync_test.go:46`
  `TestConflict_NeitherVersionLostSymmetricName`) is a single-file, unsaturated happy
  path.
- No test exercises `execute()`'s enqueue ordering, the `fetchQ`-full drop branch, or
  the loser-dropped/winner-applied interleaving. The load-bearing invariant's actual
  enforcement point is untested.

## Severity / honesty note

Triggering requires `fetchQ` saturation (256 in-flight) plus a specific interleave, so
this is not an everyday loss — but the finding's severity is **high** precisely because
the contract is "no version is EVER lost," and the reviewer's "No data-loss path found"
is therefore factually too strong. A FIXED verdict on a no-data-loss finding should not
rest on a happy-path integration test while an unconditional destructive overwrite sits
behind a droppable copy-out. At minimum this is "insufficient" pending a fault-injection
test and an ordering guard.
