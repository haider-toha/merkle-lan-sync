# PR-4 — Deletions via tombstones: propagation + anti-resurrection by a stale peer

- Phase / role: Phase 2 — protocol-researcher
- Severity: **high** (a resurrected deletion is "inverse data loss" — the file you
  deleted comes back — and is the marquee long-lived sync bug, Syncthing #10590)
- Status: open (research finding; implements SR-9/SR-10; backs
  `decisions/protocol/tombstone-retention-gc.md` and `vv-pruning-counter-cleanup.md`)
- Reads-first honoured: `sync-rules.md` SR-9/SR-10, `findings/literature/syncthing-bep.md`
  §4.5/§10.5, `findings/literature/version-vectors.md` FM-1/§4.3, SKILL §4.
- Evidence: `SetDeleted` ground-truth `bep_fileinfo.go:588-594`; resurrection symptom
  re-verified at [Issue #10590](https://github.com/syncthing/syncthing/issues/10590)
  (closed-as-not-planned, reported March 2026, accessed 2026-06-28).

---

## 1. Claim

A deletion is a **versioned event, not an absence**: deleting a path produces a
**tombstone** — the `FileInfo` is retained with `deleted=true`, `content_hash` zeroed,
and the deleting device's VV counter **bumped** — which propagates like any other
update. Because the tombstone's VV **dominates** any pre-delete version (PR-2's
missing-counter-is-0 rule), a stale peer that reconnects holding the old file
**deletes it locally and does not resurrect it on everyone else** (SR-10). Tombstones
are retained until the peer acknowledges the deletion, then GC'd (never on a timer).

## 2. Why absence is not enough (the ambiguity)

A path simply missing from an index is ambiguous between three states: *deleted here*,
*not yet created here*, and *deleted elsewhere and must be removed here* (SKILL §4;
`merkle-leaf-shape.md`). Only a **versioned** tombstone disambiguates: it carries
*who* deleted and *when* (causally), so it can win/lose a conflict deterministically
(PR-3) and can dominate a stale version. "A tombstone … is a lightweight, timestamped
marker inserted to represent the deletion … rather than immediately removing the
underlying data, which allows for propagation of delete operations across replicas
while maintaining eventual consistency" (SR-9 citation, accessed 2026-06-28).

## 3. The tombstone (adopt `SetDeleted` semantics)

Ground-truth (`bep_fileinfo.go:588-594`, verbatim in `syncthing-bep.md` §4.5):

```
SetDeleted(by):
  ModifiedBy = by
  Deleted    = true
  Version    = Version.Bump(by)     # a delete is an EDIT — bump the VV (pure prev+1, vv-counter-seeding)
  setNoContent()                    # content_hash = 32×0x00, size = 0, no blocks
```

The bumped VV + flipped `deleted` byte make the tombstone leaf's **structural hash
differ** from the pre-delete leaf (`merkle/leaf-shape-and-structural-hash.md` §D.3
tombstone rule), so the deletion changes the parent and the root and therefore shows
up in the Merkle diff (PR-1 step 4) like any other change.

A scan-detected local deletion stamps the tombstone the same way (Syncthing:
`folder.go:834,963`), and a delete is broadcast via `INDEX_UPDATE` only after the
scanner confirms it (SR-6 / PR-6) — never speculatively.

## 4. Propagation

1. A deletes `f.txt` → `SetDeleted(A)` → tombstone VV e.g. `{A:6, B:3}` (was `{A:5,B:3}`).
2. A broadcasts the tombstone in `INDEX_UPDATE`.
3. B receives it; `Compare(B's {A:5,B:3}, tombstone {A:6,B:3})` → **DominatedBy** → B
   applies the deletion: removes `f.txt` from disk, **keeps the tombstone** in its tree
   (so B can in turn dominate any *other* stale holder).
4. Both now hold the tombstone with the same VV → root hashes converge (SR-5).

## 5. Anti-resurrection by a stale peer (SR-10 — the load-bearing case)

Scenario: B is **partitioned** (offline) while A deletes `f.txt`. B still holds
`f.txt` with the pre-delete VV `{A:5, B:3}`. B reconnects.

- B advertises `f.txt` `{A:5,B:3}`; A holds tombstone `{A:6,B:3}`.
- `Compare({A:5,B:3}, {A:6,B:3})`: A-slot `5<6`, B-slot equal ⇒ **tombstone Dominates**
  B's version ⇒ B applies the deletion. **The file is removed on B; it is NOT
  re-created on A.** ∎

The dominance is *guaranteed* by the bumped counter: the deleter's counter is strictly
higher in the tombstone than in any version that existed before the delete, and a peer
that never saw the delete cannot have a counter ≥ it for the deleter's slot. This is
why the **counter must be bumped** on delete (a plain "removed from index" with no VV
change could not dominate) and why **tombstones must not be GC'd while a live peer can
still carry a pre-delete version**.

### 5.1 The resurrection failure modes we must avoid

- **Premature GC (SR-10):** GC the tombstone before B has acknowledged it; B reconnects
  with the old file, nothing dominates it, the file resurrects and propagates back to A.
  Mitigated by **ack-gated retention** (`tombstone-retention-gc.md`): keep until the
  peer's index shows it holds the tombstone, then GC symmetrically.
- **Ghost counters (#10590 / FM-1):** a *removed* device leaves a permanent counter so
  *neither* vector can dominate → "deleted files resurrect as clean copies (no
  sync-conflict marker)" (#10590, accessed 2026-06-28). Mitigated by ack-gated
  `DropCounter` on explicit device removal (`vv-pruning-counter-cleanup.md`).
- **Counter rollback (FM-4):** a wiped peer re-authoring with a low counter could make a
  tombstone fail to dominate; mitigated by the persisted-snapshot + cold-start reseed
  (`vv-counter-seeding.md`).

## 6. Delete-vs-concurrent-modification

If, instead of being merely stale, a peer **concurrently modified** `f.txt` while A
deleted it, `Compare` returns **Concurrent** (each side bumped its own slot) → this is a
**conflict** (PR-3), not a clean delete. Per SR-7/SR-9 and the verified Syncthing rule,
if the deletion wins `W`, the modification is preserved as a `.sync-conflict` copy (no
data loss); if the modification wins, the file stays and the tombstone loses. Either
way both versions of *content* are accounted for.

## 7. Retention / GC (summary; full decision in `tombstone-retention-gc.md`)

- **Retain** a tombstone until the peer's advertised VV for that path
  dominates-or-equals the tombstone's VV (peer has applied the delete).
- **Then GC** symmetrically (both peers, only after mutual acknowledgement — avoids
  FM-3 unequal-state false conflicts).
- **Never** GC on a timer (a peer offline longer than any TTL would resurrect).
- The persisted last-synced snapshot (OQ-5) **must store tombstones** so a daemon
  restart does not forget a not-yet-acked deletion (→ resurrection on restart).

## 8. Test obligations

1. Delete on A → B removes the file and **both retain the tombstone**; root hashes
   converge.
2. **SR-10 scenario:** partition B, delete on A, reconnect B → file deleted on B,
   **not resurrected** on A.
3. **Premature-GC negative test:** force a GC before B's ack → assert resurrection
   occurs (proving the ack gate is load-bearing), then with the gate enabled assert it
   does not.
4. Delete-vs-concurrent-modify → conflict copy per PR-3 (no loss).
5. Restart-with-pending-tombstone → deletion survives the restart (snapshot stores it).

## 9. Cross-references

- Rules: SR-6 (broadcast delete only after confirmed scan), SR-9 (tombstone, not
  absence), SR-10 (no resurrection), SR-7 (delete-vs-modify conflict).
- Decisions: `protocol/tombstone-retention-gc.md`, `protocol/vv-pruning-counter-cleanup.md`,
  `protocol/vv-counter-seeding.md`, `merkle/leaf-shape-and-structural-hash.md` (tombstone
  leaf hash).
- Findings: PR-2 (dominance via missing-counter-0), PR-3 (delete-vs-modify),
  PR-6 (delete broadcast only on confirmed local change);
  `literature/syncthing-bep.md` §4.5/§10.5, `literature/version-vectors.md` FM-1/FM-4.
