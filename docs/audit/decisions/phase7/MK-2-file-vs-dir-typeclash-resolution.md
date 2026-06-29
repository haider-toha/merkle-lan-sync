# Decision: MK-2 file-vs-directory type clash — truthful differ marker + refuse-and-flag

- Area: phase7 / merkle (differ) + reconcile (engine)
- Status: decided
- Date: 2026-06-29
- Decider: Phase 7 fix agent (round 1), open item MK-2 (fixed-claim-refuted)
- Reads-first honoured: `plan/README.md`, `plan/agent_roster.md`,
  `docs/audit/findings/merkle/MK-2-diff-reconciliation.md`,
  `docs/audit/findings/review/MK-2.md`,
  `docs/audit/findings/review/votes/MK-2-skeptic1.md`,
  `docs/audit/findings/review/votes/MK-2-skeptic2.md`,
  `docs/audit/decisions/ws1/tree-representation-and-differ.md`
- Consumes: MK-2 (prune-equal diff, "absence is ambiguous"), SR-5 (convergence
  oracle), SR-7 (no data loss), CDD-5 / XP-4 (no-clobber refuse-and-flag precedent),
  GR-5 (differ read-only, zero I/O).

## Context

Two Phase 6 skeptics REFUTED the FIXED verdict on MK-2
(`review/votes/MK-2-skeptic1.md`, `MK-2-skeptic2.md`). The differ's file-vs-directory
branch (`internal/merkle/differ.go:79-87`) is both untested AND emits a materially
**false** candidate.

Reproduced this session (throwaway `internal/merkle/zz_repro_test.go`, run then
removed). Local `= {foo (file)}`, remote `= {foo/bar.txt}` (so remote holds `foo` as a
**directory**). `Diff` returns:

```
Path="foo"          Local=true  Remote=false   <- FALSE: remote is NOT absent at foo;
                                                   it has a directory there with content
Path="foo/bar.txt"  Local=false Remote=true    <- an impossible single-sided "install"
```

Two concrete defects:

1. **False absence.** The `foo` entry carries `Remote=nil`, indistinguishable from an
   ordinary local-only file. This breaks MK-2's headline invariant — "absence is
   ambiguous; emit a single-sided node as a CANDIDATE and hand the *truth* to the
   resolver" — by handing a *false* absence. A nil side must mean TRUE absence.

2. **Downstream livelock + non-convergence (traced through WS-4).** Feeding those
   candidates to `resolve` (`internal/reconcile/apply.go:51`) and `execute`
   (`engine.go:662`):
   - File-side peer (local `foo`=file): `resolve(file, nil)` → `planNoOp` (keeps the
     file), but `foo/bar.txt` → `planInstall` → `materialise` →
     `atomicWriteVerify` does `os.MkdirAll(<root>/foo)` over a FILE → `ENOTDIR`;
     `handleCompletion(ok=false)` re-reconciles → re-enqueues → fails again — a
     self-sustaining retry loop that never converges.
   - Dir-side peer (local `foo`=dir): `resolve(nil, file)` → `planInstall(file foo)` →
     `os.Rename(tmp, <root>/foo)` onto a non-empty directory → `EISDIR`/`ENOTEMPTY`;
     same retry loop.
   - No code in `internal/reconcile` detects a file-vs-dir type clash from the diff
     output (`grep isDir|TypeFile|directory` over the resolver path: none). The
     no-clobber guard (`transfer.go:148`) only catches case/normalisation folds, not a
     type clash; on the file side `os.ReadDir(<root>/foo)` even fails (foo is a file),
     so it returns "no clobber". The hazard is real and unguarded.

A file on the Mac and a directory of the same name on Windows (or a delete +
recreate-as-dir) is exactly the cross-platform structural divergence this engine
exists to survive without data loss. The differ — which holds BOTH trees and is the
only place with the full structural truth — is the correct layer to report it.

## Options (scored 1-5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — Add a file-vs-dir test that asserts the CURRENT output (no behavior change)
Satisfies skeptic #1's literal "the branch is untested" ask, nothing more.
- Correctness **1**: enshrines the false-absence + the livelock as "expected".
- Concurrency **3** (unchanged). Testability **3** (a test exists, but pins wrong
  behaviour). Cross-platform **1** (the headline Mac↔Windows clash stays broken).
- **Rejected** — codifies a real data-handling bug; skeptic #2 unaddressed.

### Option B — Truthful type-clash marker in the differ + engine refuses-and-flags (CHOSEN)
Add `LocalDir`/`RemoteDir` bools to `DiffEntry`. On a file-vs-dir clash the differ
emits ONE truthful entry (the file leaf on its side; the `*Dir` flag on the directory
side; the other `*FileInfo` nil) and **prunes the directory subtree** (does not recurse
it). The reconcile loop (`reconcileWithPeer`) detects `IsTypeClash()` and **refuses +
flags** (loud log, sentinel `ErrTypeClash`, no enqueue) — no data loss (each peer keeps
its own bytes), no impossible install, no livelock. Mirrors the existing CDD-5/XP-4
case-clobber refuse exactly. The differ stays policy-free; the engine owns the verdict.
- Correctness **5**: nil now means TRUE absence; the clash is reported honestly; no
  destructive op; no livelock; no data lost.
- Concurrency **5**: differ stays read-only / zero-I/O (GR-5); the refuse is a log only
  — no new lock, channel, or goroutine.
- Testability **5**: pure differ unit test (marker + subtree pruned + minimal
  comparison count) AND a pure engine test (clash ⇒ refuse+flag, no fetch enqueued),
  paralleling the existing clobber test.
- Cross-platform **5**: directly and safely handles the file↔dir Mac/Windows
  divergence; documented as a flagged exception + a CROSS_PLATFORM_CHECKLIST item.
- **Chosen.**

### Option C — Truthful marker + full auto keep-both (directory wins, file → .sync-conflict copy, both converge)
The differ marker as in B, but the engine auto-resolves: the directory keeps the path;
the file is preserved as a deterministic `.sync-conflict` copy; both peers converge.
- Correctness **5** IF implemented perfectly (converges, no loss).
- Concurrency **2**: requires removing the file leaf at the clashing path WITHOUT
  minting a tombstone (a tombstone leaf at `foo` while `foo/bar.txt` exists is itself
  `ErrTreeConflict`), suppressing the watcher echo of that removal, fetching the file
  to a conflict path on the dir side, and ordering it all per-peer. It touches the
  watcher-echo guards and per-peer teardown the flow-verifier JUST certified
  (Invariants 3 & 4) — real regression risk for this fix round.
- Testability **3**: needs deterministic two-engine integration scenarios.
  Cross-platform **5**.
- **Deferred** — logged as the forward path; better UX, but disproportionate risk now.

### Option D — No DiffEntry change; resolver probes local state per entry to detect the clash
Keep `DiffEntry` as-is; in the engine, before installing a remote-only leaf, stat the
parent / consult the local tree to spot a type clash and refuse.
- Correctness **3**: refuses the clash but leaves the differ's FALSE-absence output
  intact — skeptic #2's charge is precisely about the DIFFER (MK-2), so the named
  finding stays unfixed; the truth the differ already holds is rediscovered ad hoc.
- Concurrency **3**: adds per-entry `os.Stat`/lookups on the reconcile path (the I/O
  GR-5 keeps off the diff). Testability **3**. Cross-platform **4**.
- **Rejected** — wrong layer; duplicates structural knowledge the differ owns.

## Decision

Adopt **Option B**.

Differ (`internal/merkle/differ.go`):
- `DiffEntry` gains `LocalDir bool` and `RemoteDir bool`, set ONLY on a file-vs-dir
  type-clash entry (and an `IsTypeClash()` helper). When both are false, a nil
  `Local`/`Remote` means TRUE absence, exactly as before.
- In `diffNodes`, the clash condition `(lLeaf && rDir) || (rLeaf && lDir)` emits one
  entry — the file leaf on its side, the `*Dir` flag on the dir side, the opposite
  `*FileInfo` nil — and RETURNS without recursing the directory subtree (those children
  cannot be materialised on the file side while the path is a file; recursing them only
  manufactures impossible installs). Dir-vs-absent and dir-vs-dir still recurse exactly
  as before.

Engine (`internal/reconcile/engine.go`, `transfer.go`):
- New sentinel `ErrTypeClash` (next to `ErrCaseClobber`).
- `reconcileWithPeer` skips a clash entry to `flagTypeClash(d)` BEFORE `resolve`:
  a loud log, no enqueue, nothing destructive. Both peers keep their own data; the path
  stays divergent and FLAGGED — the same accepted carve-out as the CDD-5 case-clobber
  refuse (both refuse and do not converge; data is never lost or clobbered).

## Rationale

- It fixes MK-2 at the named layer: the differ stops lying about absence and reports
  the structural truth it already holds, keeping "absence is ambiguous" honest.
- It is policy-free in the differ and decisive in the engine — the layer split MK-2 and
  the WS-1 decision prescribe.
- Refuse-and-flag is the established, flow-verifier-accepted response to a
  divergence that cannot be auto-resolved without risking loss (CDD-5). It is the
  minimum that removes the real hazard (livelock + false signal) without touching the
  just-certified no-sync-loop / no-leak machinery.
- Both skeptics are answered: a distinct type-clash marker the engine ACTS on
  (skeptic #2 option (a)) and a tested file-vs-dir branch with proven minimal recursion
  (skeptic #1).

## Consequences

- Touches `internal/merkle/differ.go` (+ `differ_test.go`) and
  `internal/reconcile/{engine.go,transfer.go}` (+ `reconcile_test.go`). `DiffEntry`
  fields are additive — existing consumers compile unchanged (the only consumer is
  `engine.go:647`).
- New accepted, FLAGGED non-convergence case: a file-vs-dir clash is refused, not
  converged (parallel to CDD-5). The convergence oracle (SR-5) holds as "converge OR
  safely refuse-and-flag — never lose data, never livelock." Recorded here and to be
  added to `docs/audit/CROSS_PLATFORM_CHECKLIST.md` (real NTFS/APFS scenario).
- Forward path (Option C): auto keep-both (directory wins, file → `.sync-conflict`
  copy, both converge) — a future Phase 7 round; needs remove-without-tombstone +
  watcher-echo suppression + two-engine ordering tests.
- Tests added:
  - `internal/merkle/differ_test.go`: `TestDiff_FileVsDirTypeClash` (both directions +
    a deeper subtree), asserting the truthful marker, nil-means-true-absence, the
    pruned subtree, and the minimal comparison count.
  - `internal/reconcile/reconcile_test.go`: a clash diff entry ⇒ `flagTypeClash`
    (refuse, no fetch enqueued, no completion), and the symmetric direction.
- Cross-refs: MK-2; SR-5, SR-7; CDD-5/XP-4; GR-5.
