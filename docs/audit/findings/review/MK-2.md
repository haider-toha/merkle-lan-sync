# Review verdict — MK-2 (Diff/reconciliation: prune equal, recurse mismatching)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/merkle/MK-2-diff-reconciliation.md` (claimed `status: fixed`, WS-1, commit `182ff00`)
- **Verdict: FIXED**

## What was claimed
Prune any subtree whose two hashes are equal; recurse only into differing children. A
child present on only one side is emitted as a CANDIDATE, never pre-classified create/delete
("absence is ambiguous"). Read-only, zero I/O held. White-box prune-assertion test.

## Evidence verified against code
- Prune-equal at the top of the call: `internal/merkle/differ.go:63`
  `if l != nil && r != nil && l.hash == r.hash { return }`.
- Single-sided candidate, not pre-classified: `differ.go:5-14` (`DiffEntry` doc),
  `differ.go:45-54` `emit()` sets only the present side's `*FileInfo`, leaving the other
  `nil`; `differ.go:74-87` emits file-vs-dir / single-sided as candidates.
- White-box comparison counter for the prune assertion: `differ.go:26-43` `diffCounted`.
- Read-only / zero-I/O: pure tree walk; the engine takes the diff under `RLock` and acts
  afterwards (`internal/reconcile/engine.go:632-649` `reconcileWithPeer`).

## Evidence verified against tests (all PASS under `-race`)
- `internal/merkle/differ_test.go:27` `TestDiff_PrunesEqualSubtrees` — asserts EXACTLY 3
  comparisons (root + `big` pruned + `small.txt`); had the equal `big/` subtree been
  recursed it would add 5 more. This is the load-bearing prune proof.
- `differ_test.go:11` `TestDiff_IdenticalTreesEmpty` — equal trees ⇒ 1 comparison, empty diff.
- `differ_test.go:67` `TestDiff_SingleSidedCandidate` — local-only and remote-only paths
  emitted with the opposite side `nil`, shared identical path pruned.
- `differ_test.go:91` `TestDiff_RemoteOnlySubtreeRecursedToLeaves` — remote-only subtree
  yields per-leaf candidates.

## Run-log corroboration
- `docs/audit/runs/race-all.log:6` `ok internal/merkle`; fresh 2026-06-29 run all `--- PASS`.

## Skeptical check
The comparison-count assertion (`== 3`) makes a hollow implementation impossible to pass —
a differ that recursed equal subtrees would report ≥8. "Absence is ambiguous" is enforced
structurally (the differ has no create/delete verdict; that lives in the WS-4 resolver,
`internal/reconcile/apply.go:51`). No gap found.

## Phase 7 fix (round 1) — resolution of the refuted gap

This verdict's "No gap found" was **refuted** by both skeptics
(`votes/MK-2-skeptic1.md`, `votes/MK-2-skeptic2.md`): the file-vs-directory branch was
untested AND emitted a **false** absence (`Remote=nil` over a path the remote held as a
directory), which livelocked WS-4 on an impossible `mkdir`/`rename`. The refutation was
correct. The gap is now **closed** in commit
`0e8df56665659fd7fc20b497e3ae47a9b10c1df2`:

- Differ emits a truthful clash marker (`DiffEntry.LocalDir`/`RemoteDir` +
  `IsTypeClash()`) and prunes the directory subtree instead of manufacturing impossible
  installs; a nil side now means TRUE absence unless its `*Dir` flag is set.
- Engine refuses + flags the clash (`ErrTypeClash`, `flagTypeClash`) — no fetch, no
  completion, no livelock, no data loss (CDD-5-style refuse).
- Tests: `TestDiff_FileVsDirTypeClash` (both directions + multi-file subtree + a
  Windows-hostile key; truthful marker, pruned subtree, minimal comparisons) and
  `TestReconcile_RefusesFileVsDirTypeClash` (both directions; no fetch/completion,
  flagged, bytes intact). All green under `-race`; Windows cross-compile clean.

Decision: `docs/audit/decisions/phase7/MK-2-file-vs-dir-typeclash-resolution.md`.
Finding updated: `docs/audit/findings/merkle/MK-2-diff-reconciliation.md` ("Phase 7
resolution"). **Round-1 verdict: FIXED** (the originally-claimed prune/recurse/single-
sided properties remain proven; the previously-untested-and-wrong file-vs-dir branch is
now truthful, guarded, and tested).
