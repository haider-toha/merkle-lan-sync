# Skeptic #1 vote — PR-3 "FIXED" verdict — REFUTED (medium confidence)

Reviewed 2026-06-29. Inputs: `docs/audit/findings/review/PR-3.md`,
`docs/audit/findings/protocol/PR-3-conflict-copy-policy-and-tiebreaker.md`,
`internal/reconcile/{conflict,apply,engine}.go`, `internal/merkle/fileinfo.go`,
test files, run logs.

## What holds up (not disputed)
- `aWins` (conflict.go:30-40) is total + commutative for every pair that can actually
  reach `conflictPlan`. The tier-3 content-hash backstop cannot tie there: a tombstone's
  ContentHash is `32x0x00` (fileinfo.go:28) and SHA-256 of real content is never all-zero,
  so `!sameContent` (apply.go:62) with equal hashes is unreachable. Tier-2 authorOf
  asymmetry (okA/okB) also cannot diverge inside conflictPlan: Concurrent ⇒ both ok,
  Equal-VV ⇒ both not-ok. The deterministic UTC-truncated name + suffix author are
  computed from the (identical) loser leaf on both peers. The `TestW_Commutative`,
  `TestConflict_CopyName*`, and integration `TestConflict_NeitherVersionLostSymmetricName`
  tests are real and pass. The single-conflict happy path is genuinely fixed.

## Why "FIXED" is NOT solidly evidenced — unhandled concurrency edge case (data loss)

The finding's load-bearing claim is the **literal no-data-loss contract (SR-7)**. The fix
relies on ordering in `execute` (engine.go:662-686): for a conflict where the LOCAL file
is the loser, the loser copy is enqueued (local-reuse, `advertise=true`) BEFORE the winner,
so "the loser's still-on-disk bytes are copied locally before the winner overwrites the
path." The comment claims "FIFO per-peer puller preserves the order."

That guarantee breaks under `fetchQ` saturation. `enqueueFetch` (engine.go:716-726) uses a
non-blocking `select { case ps.fetchQ <- ...: ...; default: /* drop, retry next reconcile */ }`
on a buffered channel of depth 256 (`fetchQueueDepth`, engine.go:24). The two enqueues for
a single conflict (loser, then winner) are **separate, non-atomic** select operations on the
reconcile goroutine, while `pullLoop` (engine.go:752-764) drains the same channel concurrently.

Adverse-but-realistic interleaving (>256 concurrent conflicting files — plausible for a LAN
folder both sides edited offline):
1. `enqueueFetch(loser, advertise=true)` — queue full ⇒ hits `default`, **dropped**;
   `ps.inflight[loserPath]` is NOT set.
2. puller consumes one task, freeing a slot.
3. `enqueueFetch(winner)` — now succeeds ⇒ winner is materialised and **atomically
   overwrites the original path**, destroying the local loser's bytes.

There is no cross-guard between the two enqueues and `handleCompletion` (engine.go:793-817)
does not verify the paired loser copy exists before installing the winner.

The "diff persists, retry next reconcile" safety net does NOT recover this case: after the
winner overwrites the path, the loser's bytes are gone from disk and from `e.files`; next
reconcile sees local == winner == remote ⇒ NoOp. The peer that holds the winner never had
the loser's bytes (`hasLocalContent` was false there, engine.go:677) and was waiting for
this node to advertise the copy — which now never happens. **The loser version is
permanently lost on both peers**, violating the exact SR-7 invariant the finding rates
"high severity."

## Test gap
No test exercises the `fetchQ` full / `default` drop branch (engine.go:723) at all, and none
exercises a conflict batch large enough to saturate the queue. `grep` for `queue full` /
`fetchQueueDepth` / the conflict-batch case in `internal/reconcile/*_test.go` and
`test/integration/*_test.go` returns nothing. The integration conflict test uses a single
file, so the ordering invariant the fix depends on is only ever tested in the un-saturated
regime where the drop branch never fires.

## Verdict
The deterministic/symmetric winner is fixed, but the no-data-loss contract — the finding's
primary high-severity claim — has an unhandled, untested concurrency edge case under queue
saturation. "FIXED" is not solidly evidenced. refuted = true.
