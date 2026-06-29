# PR-5 — Rename handling: delete+create (v1) vs hash-match heuristic (later)

- Phase / role: Phase 2 — protocol-researcher
- Severity: **medium** (correctness is fine either way; the difference is transfer
  efficiency — a naive scheme re-sends the whole file on a rename — not data loss)
- Status: **fixed** (WS-4) — v1 rename is the emergent delete+create: the scanner
  synthesizes a tombstone for the old path and a create for the new, the broadcast
  orders creates-before-deletes (`broadcast.go` `orderCreatesBeforeDeletes`), and the
  puller's local content-addressed reuse (`transfer.go` `localSource`) makes the new
  path cost ZERO network when the bytes are still local. Verified by
  `reconcile_test.go` `TestRename_NoNetworkTransfer` +
  `TestRescan_DetectsRenameAsDeleteCreate`. Decision
  `docs/audit/decisions/ws4/tombstone-lifecycle-rename-and-no-clobber.md`. Commit
  `af12de099165f38e11556555acc986b9ba385f24`. **Binding decision owner =
  merkle-researcher (synthesis OQ-7)**; this finding contributed the protocol-layer
  analysis and the chosen v1 approach matches it (no new wire type; `MOVE` deferred).
- Reads-first honoured: `findings/synthesis/problem-space-map.md` OQ-7,
  `findings/literature/rsync-algorithm.md` §9.5, `findings/literature/merkle-tree.md` §4.6,
  `findings/literature/syncthing-bep.md` §7.
- Evidence: rsync/Syncthing rename behaviour inherited from the cited literature
  findings (access date 2026-06-28).

---

## 1. Claim

For v1, a rename is correctly handled as **delete-old + create-new** using the
mechanisms already specified — a **tombstone** for the old path (PR-4) plus a new
`FileInfo` for the new path (PR-1 `INDEX_UPDATE`) — and **requires no new message
type and no new protocol machinery**. Because file content is **content-addressed by
`content_hash`**, the create side can be made *transfer-free* (the new path's blocks
are found locally under the same hash) as an optimisation, and a later **hash-match
rename detection** can suppress even the tombstone churn — but neither is needed for
correctness, and a dedicated `MOVE` message is an explicit `0x08`+ deferral.

## 2. Why delete+create is the correct, safe baseline

- It composes from primitives that are already proven: the old path becomes a
  tombstone with a bumped VV (PR-4), the new path is a normal create with its own VV;
  both ride existing `INDEX_UPDATE` frames. No new code paths, no new wire types.
- rsync itself "can't detect either" a move or a rename and treats them as
  delete+create (`rsync-algorithm` §9.5/§13(4)); it is a well-trodden, safe baseline.
- Convergence is unaffected: after propagation both peers hold the tombstone at the
  old path and the file at the new path, so root hashes match (SR-5).

**The one correctness subtlety — ordering of the two events.** A rename observed by the
watcher should be emitted so that a peer does not transiently *delete the only copy*
before the create lands. Two safe options: (a) emit create-new **before**
delete-old in the same `INDEX_UPDATE` batch, so the new path exists before the
tombstone removes the old; or (b) rely on content-addressing — since the new path's
`content_hash` equals the old's, even if the tombstone applies first the create can
reconstruct the bytes locally (no network fetch) from any file still holding that hash.
Recommend **both**: batch create-before-delete *and* content-address the create. This
keeps the temp→fsync→atomic-rename guarantee (SR-1) intact for the new path.

## 3. The efficiency cost it leaves on the table

Delete+create with a *naive* transfer re-sends the whole file to the new path even
though the bytes are unchanged. Content-addressing already removes most of that cost:
the puller, before requesting bytes over the wire, looks for a local file with the same
`content_hash` and copies locally ("This might be locally, if another file already has a
block with the same hash" — `syncthing-bep` §7.2, accessed 2026-06-28). So a rename
where the old bytes are still on disk costs **zero network transfer** even without
explicit rename detection. The residual cost is only the tombstone+create index churn.

## 4. The optional upgrade — hash-match rename detection (deferred)

A later optimisation can *detect* a rename: when the diff shows a created path whose
`content_hash` equals a concurrently-deleted path's `content_hash`, treat it as a move
and (optionally) emit a single `MOVE{from, to, content_hash}` hint instead of a
tombstone+create pair. `content_hash` is what *enables* this (`merkle-tree` §4.6;
synthesis OQ-7). Trade-offs:

- **Pro:** less index churn; a clearer audit trail; avoids a transient "file briefly
  absent" window on the new path.
- **Con:** false matches (two distinct files with identical content, e.g. empty files
  or duplicates) and added complexity; a `MOVE` message is new wire surface (a `0x08`+
  type behind `featureFlags`, `message-type-enumeration.md`).

**Recommendation:** v1 = delete+create (create-before-delete + content-addressed copy);
defer hash-match detection and any `MOVE` message until measured need. This matches the
synthesis lean ("v1 may treat as delete+create; `content_hash` enables optional
hash-match detection later") and keeps the catalogue at seven types.

## 5. Protocol-layer conclusion (my lane)

- **No new message type for v1.** Rename = tombstone (`INDEX_UPDATE`) + create
  (`INDEX_UPDATE`), ordered create-before-delete, with content-addressed local copy on
  the create side. `REQUEST`/`RESPONSE` only fire if the bytes are not already local.
- **Reserve `MOVE` as a future `0x08`+ type** behind `featureFlags`, never renumbering.
- The **binding decision** (treat-as-delete+create vs build hash-match now) is
  **merkle-researcher's** (OQ-7); this finding's role is to confirm the protocol needs
  *nothing new* for the v1 choice and to specify the create-before-delete ordering and
  content-addressed-copy requirements so the chosen approach is safe.

## 6. Test obligations

1. Rename a file on A → B ends with the file at the new path and a tombstone at the old;
   roots converge; **zero network transfer** when the bytes are still local (assert no
   `REQUEST` for that content_hash).
2. Create-before-delete ordering: assert the new path is never transiently absent on B
   in a way that loses the only copy.
3. (If hash-match is later built) duplicate-content false-match guard: two unrelated
   identical files are not mis-detected as a rename in a way that loses one.

## 7. Cross-references

- Findings: PR-1 (INDEX_UPDATE carries both events; MOVE as `0x08`+),
  PR-4 (tombstone for the old path), PR-3 (rename racing a concurrent edit → conflict),
  `literature/rsync-algorithm.md` §9.5, `literature/merkle-tree.md` §4.6.
- Decisions: `protocol/message-type-enumeration.md` (no new type for v1; MOVE deferred);
  **OQ-7 binding decision belongs to merkle-researcher** (`decisions/ws1/`).
- Rules: SR-1 (atomic create), SR-5 (convergence), SR-6 (broadcast on confirmed change).
