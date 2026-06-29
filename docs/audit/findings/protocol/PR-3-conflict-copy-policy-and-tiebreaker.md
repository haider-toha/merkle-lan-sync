# PR-3 — Conflict-copy policy + the mtime-tie tiebreaker (deterministic & symmetric)

- Phase / role: Phase 2 — protocol-researcher
- Severity: **high** (this is the literal no-data-loss contract; an asymmetric or
  non-deterministic winner rule means the two peers diverge or one version is lost)
- Status: **fixed** (Phase 7 round 1, commit `9d1e0cca7ac3ef7bd66e042c665103f42947d9fc`).
  The total+commutative winner `W` (`aWins`/`winner`/`loserOf`) and the deterministic
  UTC-whole-second `.sync-conflict` copy name (`conflictName`) were sound from WS-4
  (`af12de0`). But the WS-4 "fixed" verdict was **REFUTED by all three Phase 6 skeptics**:
  the no-data-loss CONTRACT (SR-7/SR-9) was broken in the EXECUTION layer (the resolver
  was fine). Four defects, now fixed in Phase 7:
  (A) live-vs-live under `fetchQ` saturation — the loser copy and the winner overwrite
  were separate non-blocking enqueues; a dropped copy + a slipped-through winner destroyed
  the loser (skeptic #1/#3);
  (B) delete-vs-modify where the delete wins — `execute` removed the original via a
  synchronous `applyTombstone` BEFORE the async copy ran, losing the modification (skeptic
  #2 §1; reproduced deterministically);
  (C) §6 MAX_PATH bounding was unwired — `WouldExceedMaxPath` never called from reconcile
  (skeptic #2 §2);
  (D) cross-peer FALSE DOMINATION — `conflictPlan` merged the loser's VV into the winner,
  so a broadcast winning tombstone dominated the loser's still-live edit on its own
  custodian (and on any 3rd peer holding it), which then plain-deleted it without a copy
  (found while building the (B) integration test).
  Fix: (A)+(B) couple the loser-copy with the winner's install into ONE atomic puller task
  gating the destructive step on the copy landing; (D) the winner keeps its OWN VV (no
  false-dominating merge — the winner leaf is identical on both peers, so convergence is
  preserved while every holder of the loser now sees a true Concurrent conflict and
  preserves it); (C) `execute` refuses+flags (`ErrMaxPathExceeded`) an over-MAX_PATH copy,
  never overwriting the loser. Implemented in `internal/reconcile/{apply,engine,transfer}.go`,
  verified by `reconcile_test.go` (`TestResolver_DeleteWinsPreservesModification`,
  `TestConflict_DeleteWins_ModificationPreservedAsCopy`,
  `TestConflict_WinnerGatedOnCopy_NoOverwriteWhenCopyFails`,
  `TestConflict_FullQueueDropsCoupledTaskAtomically`, `TestConflict_RefusesOverMaxPathCopy`,
  plus the original `TestW_Commutative`/`TestConflict_CopyName*`/`TestResolver_*`) and
  integration `TestConflict_NeitherVersionLostSymmetricName` +
  `TestConflict_DeleteVsModify_NoLossBothPeers` (forced delete-wins; the losing
  modification survives on BOTH peers). `go build ./... && go test ./... -race` green;
  `GOOS=windows GOARCH=amd64` build clean. Decisions
  `docs/audit/decisions/phase7/PR-3-conflict-no-data-loss-ordering.md` (amends
  `docs/audit/decisions/ws4/resolver-totality-conflict-identity-and-sync-loop.md`: the
  conflict winner no longer merges the loser's VV).
- Reads-first honoured: `sync-rules.md` SR-4/SR-7/SR-9, `findings/literature/syncthing-bep.md`
  §4.6/§5, `findings/literature/version-vectors.md` §4.6/FM-6, `findings/codebases/syncthing-source.md`
  A2-3, SKILL §3.
- Evidence: conflict policy independently re-verified at
  [Syncthing — Understanding Synchronization](https://docs.syncthing.net/users/syncing.html) (accessed 2026-06-28); code ground-truth
  `WinsConflict` `bep_fileinfo.go:208-227`, `conflictName` `folder_sendrecv.go:2220-2223`.

---

## 1. Claim

When two edits to one path are **`Concurrent`** (PR-2) and their contents differ, the
engine keeps **both**: the winner stays at the path; the **loser is renamed** to
`<name>.sync-conflict-<UTC-date>-<UTC-time>-<deviceID>.<ext>` and then syncs as an
ordinary file. The winner is chosen by a **total, commutative** function of the two
`FileInfo`s — so **both peers independently pick the same loser** with no coordination
— and the loser is **renamed, never deleted**. This makes the no-data-loss invariant
literally true (SR-7).

## 2. The conflict-copy policy (verified verbatim)

From [Understanding Synchronization](https://docs.syncthing.net/users/syncing.html)
(accessed 2026-06-28):

- format: `"<filename>.sync-conflict-<date>-<time>-<modifiedBy>.<ext>"`;
- which file is renamed: "The file with the older modification time will be marked as
  the conflicting file and thus be renamed. If the modification times are equal, the
  file originating from the device which has the larger value of the first 63 bits for
  its device ID will be marked as the conflicting file";
- modification-vs-deletion: "If the conflict is between a modification and a deletion
  of the file, and the deletion wins the conflict resolution, the file is renamed to a
  conflict copy as above" (so even a *losing modification* survives — no data loss);
- propagation: "the `<filename>.sync-conflict-<date>-<time>-<modifiedBy>.<ext>` files
  are treated as normal files after they are created, so they are propagated between
  devices."

The loser is **renamed, never deleted** (`moveForConflict` → `Rename`,
`folder_sendrecv.go:1863-1906`); the renamed copy is fed back into the scanner so it
itself syncs (`:1902-1904`). Adopt verbatim into `internal/reconcile/conflict.go`.

## 3. The deterministic winner function `W(fiA, fiB)`

Resolution only runs **after** PR-2 says `Concurrent` (the VV cannot break the tie —
that is what "concurrent" *means*: neither dominates). Evaluated in priority order, as
a pure function of the two `FileInfo`s' intrinsic fields (never of "local vs remote"):

```
W(fiA, fiB):                       # returns the WINNER (stays at path); the other is the loser
  0. (optional) if exactly one is invalid  → the valid one wins        # XP-6/scan-failure flag
  1. if mtimeA != mtimeB                    → newer mtime wins          # older mtime LOSES (SR-7)
  2. else if author(A) != author(B)         → smaller modified_by wins  # larger ShortID LOSES
  3. else (defensive backstop)              → smaller content_hash wins # bytewise; never ties (contents differ)
```

- **Tier 1 (mtime):** older mtime loses. This is the *only* use of mtime (SR-4); it
  orders nothing — it merely breaks a tie among already-concurrent edits.
- **Tier 2 (author / `modified_by` ShortID):** when mtimes are equal, the larger
  authoring DeviceID loses — exactly the Syncthing user-doc rule ("larger value of the
  first 63 bits for its device ID will be marked as the conflicting file"). This is the
  binding rule per SR-7 and SKILL §3.
- **Tier 3 (content hash):** a defensive total-order backstop for the degenerate
  same-author-same-mtime case. In a genuine conflict the contents differ by definition,
  so the 32-byte `content_hash`es differ and bytewise compare is a strict total order —
  no tie can survive.

### 3.1 Resolution of the doc-vs-code nuance (logged so Phase 3 doesn't re-litigate)

Syncthing's **code** breaks an mtime tie with `f.FileVersion().Compare(other) ==
ConcurrentGreater` (`bep_fileinfo.go:226`), while its **user docs** say "larger
device ID loses." `version-vectors` §4.6 / `syncthing-source` A2-3 note the doc phrasing
is the *intent* and the `ConcurrentGreater` walk is the deterministic realisation.
Merkle Sync adopts the **explicit `modified_by` (DeviceID) rule** (tier 2) rather than
the VV-direction walk because (a) it is the rule SR-7/SKILL already pin, (b) it is
*trivially* provably commutative without needing the VV internals, and (c) for a
2-device tool the two are equivalent. Both are deterministic; we choose the one we can
prove in two lines.

## 4. Proof obligation: `W` is total and commutative ⇒ symmetric convergence

The contract demands every conflict rule be **deterministic and symmetric**: both peers
independently reach the same outcome. The proof reduces to two properties of `W`:

- **Total:** `W` always returns exactly one winner. Tiers 1–3 are exhaustive and the
  tier-3 backstop is a strict total order over distinct 32-byte hashes (contents differ
  in a real conflict), so there is never an undecided tie. ∎
- **Commutative:** `W(fiA, fiB) == W(fiB, fiA)`. Each tier compares an intrinsic field
  with a symmetric relation: `mtime` (`>`), `modified_by` (`<`), `content_hash`
  (bytewise `<`). Swapping arguments swaps which side each comparison favours but yields
  the **same** winning `FileInfo`. ∎

**Worked symmetry check.** Devices M (ShortID 5) and P (ShortID 3) concurrently edit
`f.txt`, equal mtime; M authored its copy, P authored its copy.
- On **M**: `W(localM, remoteP)` → tier 1 tie → tier 2: authors 5 vs 3 → smaller (3=P)
  wins ⇒ P's content stays at `f.txt`; M renames *its own* copy to
  `f.sync-conflict-<…>-<M>.txt`.
- On **P**: `W(localP, remoteM)` → tier 2: authors 3 vs 5 → smaller (3=P) wins ⇒ P keeps
  its copy at `f.txt`; P renames the *incoming M* copy to `f.sync-conflict-<…>-<M>.txt`.

Both peers end with P's bytes at `f.txt` and M's bytes in an **identically named**
`.sync-conflict-…-<M>.txt` (the suffix is the *loser's* `modified_by`). The conflict
copy then syncs as a normal file, so both hold both versions and the trees converge to
the same root hash (SR-5). No coordination, no data loss. ∎

The symmetry holds **because `W` ignores "local vs remote"** and depends only on
intrinsic fields both peers observe identically — that is the load-bearing design
constraint (FM-6: "A VV without a deterministic tiebreaker leads peers to disagree on
the winner → divergence").

## 5. Modification-vs-deletion conflict (no-data-loss even when delete wins)

If the conflict is between a modification and a tombstone (PR-4) and the **deletion
wins** `W`, the *losing modification* is still preserved as a `.sync-conflict` copy
(Syncthing: "the file is renamed to a conflict copy as above", accessed 2026-06-28).
So a delete never silently destroys a concurrent edit — consistent with SR-7 + SR-9.

## 6. Conflict-copy naming details

- `<UTC-date>` = `YYYYMMDD`, `<UTC-time>` = `HHMMSS` (UTC, so both peers format the same
  string regardless of local timezone — a cross-platform determinism requirement).
- `<deviceID>` = the **loser's** `modified_by` rendered as a short hex/base32 string
  (no Luhn/base32 GUI flourish — N15). Both peers compute the same loser ⇒ same suffix.
- `.<ext>` preserved so the copy keeps its type association.
- The full conflict-copy path is itself a canonical NFC forward-slash key (SR-13) and
  is bounded against `MAX_PATH`/reserved-name escaping on Windows (XP-3, crossplatform).

## 7. Test obligations

1. Concurrent edits with differing content on two instances → converge to **two** files
   (winner + one `.sync-conflict-…`), **neither version's bytes lost**, on **both**
   peers (the SR-7 acceptance).
2. Property test: `W(a,b) == W(b,a)` over random `FileInfo` pairs (commutativity);
   exhaustive tier coverage incl. mtime-tie and the content-hash backstop.
3. Equal mtime → assert the **same** conflict-copy filename is produced on both peers.
4. Modification-vs-deletion where the delete wins → the modification survives as a
   conflict copy (no loss).
5. UTC formatting → two instances in different `TZ` produce the identical suffix.

## 8. Cross-references

- Rules: SR-4 (mtime is tiebreaker only), SR-7 (conflict copy + tiebreaker), SR-9
  (delete-vs-modify), SR-13 (canonical key), XP-3 (Windows-safe conflict name).
- Findings: PR-2 (Concurrent precondition), PR-4 (tombstone side of delete-vs-modify),
  PR-6 (the conflict copy syncs without re-looping); `literature/version-vectors.md`
  FM-6, `literature/syncthing-bep.md` §4.6/§5, `codebases/syncthing-source.md` A2-3.
- Decisions: `protocol/vv-counter-seeding.md` (equal-VV-differing-content backstop also
  routes here); `merkle/leaf-shape-and-structural-hash.md` (`modified_by`, mtime fields).
