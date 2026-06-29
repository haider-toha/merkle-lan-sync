# Decision: PR-3 — make the conflict no-data-loss contract (SR-7) actually hold: couple the loser-copy with the winner-install + bound the conflict-copy name

- Area: phase7 / reconcile (engine execute + puller) + pathnorm (MAX_PATH wiring)
- Status: decided
- Date: 2026-06-29
- Decider: Phase 7 fix agent (round 1), open item PR-3 (fixed-claim-refuted)
- Reads-first honoured: `plan/README.md`, `plan/agent_roster.md`,
  `docs/audit/findings/protocol/PR-3-conflict-copy-policy-and-tiebreaker.md`,
  `docs/audit/findings/review/PR-3.md`,
  `docs/audit/findings/review/votes/PR-3-skeptic1.md`,
  `docs/audit/findings/review/PR-3-skeptic2.md`,
  `docs/audit/findings/review/votes/PR-3-skeptic3.md`,
  `docs/audit/findings/review/flow-verification.md` (Invariant 2),
  `docs/audit/decisions/crossplatform/maxpath-longpath-handling.md`,
  `docs/audit/decisions/phase7/MK-2-file-vs-dir-typeclash-resolution.md` (refuse+flag precedent).
- Consumes: SR-7 (no data loss on conflict — loser renamed, never deleted), SR-9
  (delete-vs-modify), CDD-1 (loop never blocks; I/O off the loop; stop-and-wait pull),
  GR-5 (single writer, zero I/O under the lock), CDD-5 / XP-4 / MK-2 (refuse+flag
  carve-out precedent), XP-3 + maxpath-longpath (conflict-copy bounded on Windows).

## Context

All three Phase 6 skeptics REFUTED the FIXED verdict on PR-3. The *resolver* layer
(`aWins`/`winner`/`loserOf`/`conflictName`, total+commutative, TZ/ns-independent) is
genuinely sound and not disputed. The refutations land on the **execution** layer
(`engine.go execute()` + the puller), where the load-bearing no-data-loss invariant is
actually enforced — and where it is broken in two ways, plus one unwired cross-platform
claim:

**(A) Live-vs-live conflict, queue saturation (skeptic #1, #3 — high severity).**
For a conflict where THIS side holds the loser's bytes, `execute` issues TWO separate,
non-atomic `enqueueFetch` calls on the same depth-256 `fetchQ`: the loser copy
(`advertise=true`), then the winner. `enqueueFetch` (`engine.go:749-759`) is a
**non-blocking send that silently DROPS** on a full queue. With the single puller
draining concurrently, the loser-copy enqueue can hit the full-queue `default` and be
dropped while the winner enqueue — issued immediately after — succeeds (the puller freed
a slot in between). The puller then `atomicWriteVerify`-overwrites the original path with
the winner's bytes, destroying the loser's ONLY on-disk copy (only the loser-custodian
side ever mints the copy). Next reconcile: `hasLocalContent(loser)` is now false ⇒ the
copy is never made ⇒ the loser version is permanently lost on both peers. The
"diff persists, retry next reconcile" net does NOT recover it.

**(B) Delete-vs-modify where the DELETE WINS (skeptic #2 §1 — high severity).**
The finding §5 / SR-9 promise: a losing *modification* survives as a `.sync-conflict`
copy even when the deletion wins. The resolver produces the right plan (winner =
tombstone, loser = live modification). But `execute` enqueues the copy ASYNC and then
calls `applyTombstone(winner)` which does a **synchronous `os.Remove`** of the original
path RIGHT THEN — before the puller runs the copy. The copy's `localSource` then finds
no live bytes (file gone, `e.files` entry now a tombstone) ⇒ falls to a network fetch ⇒
the peer is the deleter (tombstone) ⇒ declines ⇒ copy fails ⇒ the modification is
permanently lost. **Confirmed this session** with a throwaway repro
(`internal/reconcile/zz_repro_test.go`, run then removed):

```
after reconcile: f.txt-removed=true conflict-copies-on-disk=0 queued-fetch-tasks=1
REPRO CONFIRMED: original destroyed before any .sync-conflict copy exists — loser bytes lost
```

So skeptic #2's "appears correct on inspection" was too generous: this is a real,
deterministic data-loss bug, not merely a missing test.

The common root cause of (A) and (B): **the destructive operation on the shared path
(winner overwrite, or winning-tombstone remove) is NOT coupled to / gated on the loser's
bytes being safely preserved first.** The copy-out is best-effort and droppable; the
destruction is unconditional. `flow-verification.md` Invariant 2 asserted the FIFO
puller guarantees copy-before-overwrite — true only when both enqueues succeed and the
winner is live; it is false under saturation and false for the synchronous delete path.

**(D) Cross-peer FALSE DOMINATION (discovered while building the (B) integration test —
the deepest of the four).** `conflictPlan` set the winner's VV to `Merge(localV, remoteV)`,
i.e. a vector that DOMINATES the genuinely-concurrent loser. For a winning TOMBSTONE this
merged tombstone is then BROADCAST (the GC handshake in `applyTombstone`). The loser's own
custodian receives it and — because the merged VV now dominates its still-live edit —
resolves it as a plain `DominatedBy` delete (`planInstall` tombstone), removing its edit
with NO conflict copy, racing/preempting its own coupled preservation task. Reproduced
deterministically by the two-engine delete-vs-modify test: the loser-custodian logged both
"conflict (mint copy)" AND its own `applyTombstone REMOVE` (via the dominated-delete path),
and the copy's local-reuse then failed because the file was already gone. The merge also
loses the loser's version on ANY third peer that holds it (it sees domination, not
conflict). The merge is moreover UNNECESSARY for convergence: the winner is the same
replicated leaf on both peers, so the winner's OWN VV is already identical on both — the
stated reason for the merge ("both peers compute the same VV") holds without it.

**(C) §6 MAX_PATH bounding is unwired (skeptic #2 §2 — secondary).** Finding §6 claims
the conflict-copy path "is bounded against MAX_PATH / reserved-name escaping on Windows
(XP-3)". Reserved-name escaping IS handled (`EscapeForWindows` at the OS boundary,
`TestConflict_CopyNameWindowsRoundTrips`). But `WouldExceedMaxPath`
(`pathnorm.go:182`) is never called from `reconcile`/`cmd` (grep: defined+tested only in
`pathnorm`), so a conflict copy whose name crosses 260 on Windows is not refused+flagged
as claimed. The maxpath-longpath decision (Option B, point 3) explicitly prescribes a
**refuse+flag fallback** at the write boundary "when long-path support is not confirmed."

## Options for the primary fix — (A)+(B) ordering (scored 1-5: correctness / concurrency-safety / testability / cross-platform)

### Option A — Couple the loser-copy with the winner-install into ONE atomic puller task; gate the destructive op on the copy landing (CHOSEN)
`fetchTask` gains `preserve *FileInfo` (the conflict loser). For the side that holds the
loser's bytes, `execute` enqueues a SINGLE coupled task (`enqueueConflict`) keyed on the
winner path. The puller (`runFetch`) does **copy-then-winner as one unit**: materialise
the copy (local-reuse from the still-present original) FIRST; only if it lands does it
install the winner (live ⇒ `materialise` overwrite; tombstone ⇒ a deferred `applyTomb`
completion that removes the original on the loop AFTER the copy). If the copy fails, the
winner is SKIPPED (loser's bytes stay at the original path; rescan-paced retry via a
`conflictAbort` completion that clears inflight without an immediate re-reconcile).
- Correctness **5**: the destructive op is physically gated on the copy being on disk;
  a queue-full drop is atomic (both or neither, one slot); a copy *materialise* failure
  (not just an enqueue drop) also cannot strand the overwrite; fixes (A) AND (B) with one
  mechanism. No data-loss path remains.
- Concurrency **5**: the loop still never blocks (enqueue stays non-blocking; drop is
  now whole-task); zero I/O added under the lock (the copy + overwrite + remove all run
  in the existing per-peer puller / via completions, off the loop — CDD-1/GR-5); single
  puller FIFO preserved; inflight keyed on the winner path dedups re-reconciles.
- Testability **5**: deterministic engine-level tests drive `execute` + the puller and
  assert on-disk bytes (delete-wins copy preserved; copy-fail ⇒ winner not installed;
  full-queue ⇒ coupled task dropped whole ⇒ original untouched), plus a resolver-matrix
  row and an integration delete-vs-modify no-loss test.
- Cross-platform **5**: no OS-specific code; canonical-key copy path unchanged.
- **Chosen.**

### Option B — "Room-for-two" atomic enqueue (check capacity; enqueue both copy+winner or neither)
Only the reconcile goroutine produces to `fetchQ`, so `len(fetchQ)+2 <= cap` is a safe
pre-check (the puller only frees slots). Enqueue both or neither.
- Correctness **3**: closes the enqueue *split-drop* (A) but NOT the copy *materialise*
  failure (disk error / declined fetch) — the winner, already queued behind it, still
  overwrites. And it does nothing for (B): the delete path applies the tombstone
  synchronously, not via the queue, so a separate fix is still required. Partial.
- Concurrency **4**, testability **3** (the materialise-fail hole is hard to assert away),
  cross-platform **5**.
- **Rejected** — leaves two of the three holes open; not a single coherent fix.

### Option C — Blocking enqueue for the copy (drop the non-blocking `default` for the copy)
Guarantee the copy is queued by blocking the reconcile goroutine until there is room.
- Correctness **2**, concurrency **1**: the reconcile goroutine IS the select loop that
  also drains `e.completions`; if `fetchQ` is full and the puller is blocked trying to
  `report` a completion the loop is no longer draining (because it is blocked sending the
  copy), that is a classic deadlock. Violates CDD-1 ("the loop never blocks").
- Testability **2**, cross-platform **5**.
- **Rejected** — reintroduces the back-pressure deadlock CDD-1 exists to prevent.

### Option D — Synchronous copy-out on the reconcile loop before scheduling the winner
Copy the loser's bytes to the `.sync-conflict` path in `execute` (on the loop) before
enqueuing the winner.
- Correctness **4** (ordering correct) but concurrency **1**: a multi-MiB file copy on
  the single-writer select loop stalls ALL peers, change detection, and completion
  draining for the copy's duration — a direct GR-5/CDD-1 violation (the loop must hold no
  I/O).
- Testability **3**, cross-platform **5**.
- **Rejected** — wrong layer; the puller exists precisely to keep this I/O off the loop.

## Options for the secondary fix — (C) MAX_PATH bounding

### Option C1 — Wire `WouldExceedMaxPath` as a refuse+flag at conflict-copy creation (CHOSEN)
In `execute`, on the side that mints the copy, refuse the WHOLE conflict (do not enqueue
the coupled task ⇒ do not overwrite the loser) and flag (`ErrMaxPathExceeded`) when the
conflict-copy canonical key would exceed `MaxPath` on a Windows target. No data lost
(both versions stay in place); a flagged, non-converging path — the same accepted carve-
out as `ErrCaseClobber` (CDD-5) and `ErrTypeClash` (MK-2). Exactly the maxpath-longpath
Option B point-3 fallback, and testable on the Mac via the explicit `Windows` Target.
- correctness 5 / concurrency 5 (a log, no I/O) / testability 5 (deterministic, long
  key) / cross-platform 5. **Chosen.**

### Option C2 — Retract / soften the §6 claim, defer MAX_PATH to the windows-latest checklist
Honest but weaker: leaves a known unwritable-copy risk on a Windows peer unguarded.
- correctness 3 / testability 2. **Rejected** — the maxpath decision already chose
  refuse+flag; wiring it is small and removes a real risk.

### Option C3 — Check MAX_PATH inside the pure resolver (`conflictPlan`)
Pollutes the pure, I/O-free, root-agnostic resolver with `absRoot` + OS knowledge.
- correctness 3 / concurrency 4 / testability 4 / cross-platform 3. **Rejected** — wrong
  layer; the engine (which owns `absRoot` and the refuse+flag verdicts) is the right place.

## Options for the cross-peer fix — (D) false domination (scored as above)

### Option E — The conflict winner keeps its OWN version vector (drop the merge) (CHOSEN)
`conflictPlan` no longer sets `win.Version = Merge(localV, remoteV)`; the winner retains
the winning leaf's own VV. The winning leaf is identical on both peers (winner() is a pure,
commutative function of replicated fields), so both record the same winner VV — convergence
is preserved — but the winner no longer falsely dominates the concurrent loser, so EVERY
holder of the loser (including the loser's own custodian receiving the winning tombstone,
and any third peer) sees a true `Concurrent` conflict and preserves the loser as a copy.
- Correctness **5**: removes the false causality at the source; fixes (D) for 2 AND N
  peers; strictly more correct than the merge (which silently dropped the loser on a third
  holder). Convergence re-verified by the full integration suite (`-race`, ×5).
- Concurrency **5**: pure-resolver change, no new I/O / lock / channel.
- Testability **5**: the two-engine delete-vs-modify test (forced delete-wins) and the
  whole conflict/deletion/restart suite cover it.
- Cross-platform **5**: VV semantics are OS-agnostic.
- **Chosen.** (Amends the WS-4 resolver decision's `Merge` choice — logged here with
  rationale; the WS-4 reason for the merge is satisfied without it.)

### Option F — Gate the `planInstall` tombstone/overwrite on `inflight`
Stop a dominating tombstone from preempting an in-flight conflict-copy task.
- Correctness **3**: fixes the protocol-ordered race (INDEX before INDEX_UPDATE ⇒ the
  conflict is claimed first) but leaves an extreme-saturation residual (a dropped coupled
  task ⇒ `inflight` unset ⇒ the dominating tombstone still applies) AND does NOT fix the
  third-peer loser loss (the merge still falsely dominates). Concurrency 4 / testability 3 /
  cross-platform 5.
- **Rejected as the fix** (treats a symptom; leaves holes). The merge removal (E) makes the
  dominated-delete path unreachable for conflicts, so this gate is not even needed.

### Option G — Receiver-side "delete/overwrite would drop unpreserved differing live data ⇒ treat as conflict"
- Correctness **2**: cannot distinguish FALSE domination (conflict merge) from GENUINE
  causal supersession (a real newer edit) from the VV alone, so it would mint a spurious
  conflict copy for every ordinary edit-over-edit update. Testability 2.
- **Rejected** — over-preserves; corrupts normal update semantics.

### Option H — Tag conflict-resolution tombstones on the wire so receivers don't plain-apply
- Correctness **3** but a protocol/grammar change (larger blast radius, back-compat), and
  the receiver STILL must mint the copy. Concurrency 4 / testability 3 / cross-platform 4.
- **Rejected** — disproportionate; (E) achieves the same with no wire change.

## Decision

Adopt **Option A** (couple loser-copy + winner-install, gate destruction on the copy),
**Option E** (winner keeps its own VV — no false-dominating merge), and **Option C1**
(refuse+flag an over-MAX_PATH conflict copy). A and E are COMPLEMENTARY and both required:
A fixes the intra-node ordering (saturation split-drop + delete-before-copy); E fixes the
cross-node false-domination that otherwise routes the loser's own custodian into a plain
dominated-delete before its coupled task can preserve the bytes.

Resolver (`internal/reconcile/apply.go`):
- `conflictPlan` drops `win.Version = Merge(localV, remoteV)`; the winner keeps its own VV
  (Option E). Loser-copy minting (`p.loser`) is unchanged.

Engine (`internal/reconcile/engine.go`):
- `fetchTask` gains `preserve *merkle.FileInfo`. `completion` gains `applyTomb bool`
  (deferred winning-tombstone removal) and `conflictAbort bool` (copy did not land —
  clear inflight, skip the immediate re-reconcile to avoid a tight spin).
- New `enqueueConflict(ps, loser, winner)` enqueues ONE coupled task keyed on
  `winner.Path` inflight (atomic both-or-neither drop on a full queue).
- `pullLoop` calls a new `runFetch(ctx, ps, task)`: if `preserve != nil`, materialise
  the copy first; on failure report `conflictAbort` and STOP (no overwrite/remove); on
  success, install the winner (live ⇒ `materialise`; tombstone ⇒ report `applyTomb`).
- `materialise` returns `bool` (success) in addition to reporting its completion, so
  `runFetch` can gate the winner on the copy. (Existing call sites ignore the return —
  valid Go.)
- `execute` planConflict becomes: (i) loser present AND we hold its bytes ⇒
  `enqueueConflict` (after the MAX_PATH guard); (ii) else winner is a tombstone ⇒
  `applyTombstone` directly (winner-custodian, nothing to preserve); (iii) else
  `enqueueFetch(winner)`. The MAX_PATH guard (`ErrMaxPathExceeded`,
  `pathnorm.WouldExceedMaxPath(e.absRoot, p.loser.Path)`) refuses+flags before (i).
- `handleCompletion` handles `applyTomb` (deferred `applyTombstone` after the copy) and
  `conflictAbort` (clear inflight, no re-reconcile).

Transfer (`internal/reconcile/transfer.go`): `ErrMaxPathExceeded` sentinel.

## Rationale

- It fixes the load-bearing invariant at the layer that actually enforces it (execution
  ordering), which is exactly where all three skeptics located the failure — the pure
  resolver they vindicated is untouched.
- One mechanism (couple + gate) closes BOTH the saturation split-drop (A) and the
  synchronous-delete-before-copy (B), and additionally the copy-*materialise*-failure
  hole that "room-for-two" would miss. The winner's destruction is now impossible unless
  the loser's bytes are already duplicated on disk.
- It respects every concurrency invariant the flow-verifier just certified: the loop
  never blocks (non-blocking enqueue; whole-task drop), no I/O moves onto the loop (the
  copy/overwrite stay in the puller; the deferred remove is the same single-syscall
  `applyTombstone` already run on the loop today), and the single-puller FIFO + inflight
  dedup are preserved.
- MAX_PATH refuse+flag makes §6 literally true via the established carve-out and the
  maxpath-longpath decision, and is testable on the Mac through the explicit Windows
  Target — no new cross-OS gap.
- Dropping the conflict-winner merge (E) removes a false-causality bug that the WS-4
  decision's "merge so both compute the same VV" reasoning did not need (the winner leaf
  is already identical on both peers). It is strictly more correct: the prior merge
  silently dropped the loser on the loser's own custodian (via the broadcast winning
  tombstone) and on any third peer holding the loser. The whole conflict/deletion/restart
  suite re-passes under `-race`, so convergence is preserved.

## Consequences

- Touches `internal/reconcile/{apply.go,engine.go,transfer.go}` and
  `internal/reconcile/reconcile_test.go` + `test/integration/{sync_test.go,helpers.go}`.
- AMENDS the WS-4 resolver decision (`decisions/ws4/resolver-totality-conflict-identity-
  and-sync-loop.md`): the conflict winner no longer merges the loser's VV. The WS-4
  convergence rationale still holds (the winner leaf is replicated, so its VV is identical
  on both peers without the merge); the merge's removal fixes the cross-peer data loss.
- `flow-verification.md` Invariant 2's "FIFO guarantees copy-before-overwrite" wording is
  now backed by an *enforced* coupling (the copy and the destructive op are one task,
  gated) rather than incidental enqueue ordering. (The verdict stays PASS; the mechanism
  is now actually true under saturation and for the delete path.)
- New accepted, FLAGGED non-convergence case: a conflict whose copy key would exceed
  Windows MAX_PATH is refused (parallel to CDD-5 / MK-2). To be added to
  `docs/audit/CROSS_PLATFORM_CHECKLIST.md` (deep-tree conflict on a non-long-path
  Windows box).
- Tests added (all `-race`, Windows-input where paths are involved):
  - `internal/reconcile/reconcile_test.go`:
    - `TestResolver_Matrix` row: concurrent live-modify vs winning-tombstone ⇒
      planConflict with a loser copy preserving the modification.
    - `TestConflict_DeleteWins_ModificationPreservedAsCopy` — drives `execute` + the
      puller for delete-wins; asserts the modification's bytes land in the
      `.sync-conflict` copy on disk and the original is tombstoned (the repro, now
      green); includes a Windows-hostile path variant.
    - `TestConflict_WinnerGatedOnCopy_NoOverwriteWhenCopyFails` — a coupled task whose
      copy deterministically fails must NOT install the winner (original bytes intact).
    - `TestConflict_FullQueueDropsCoupledTaskAtomically` — a saturated `fetchQ` drops the
      whole coupled task (neither copy nor winner); the loser's bytes are untouched.
    - `TestConflict_RefusesOverMaxPathCopy` — an over-MAX_PATH (Windows) conflict-copy
      key is refused+flagged; the loser is not overwritten.
  - `test/integration/sync_test.go`:
    - `TestConflict_DeleteVsModify_NoLossBothPeers` — two live engines, a delete-vs-modify
      conflict; the modification's bytes are recoverable on BOTH peers and the roots
      converge (no loss, robust to which side wins the mtime tiebreak).
- Cross-refs: SR-7, SR-9; CDD-1, GR-5; CDD-5 / XP-4 / MK-2 (refuse+flag); XP-3 +
  maxpath-longpath; PR-3 finding §3-§6.
