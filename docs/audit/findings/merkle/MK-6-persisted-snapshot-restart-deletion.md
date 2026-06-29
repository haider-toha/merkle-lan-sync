# Finding MK-6 — A persisted last-synced tree snapshot is required to detect deletions across a daemon restart

- Slug: `MK-6-persisted-snapshot-restart-deletion`
- Phase / role: Phase 2 — merkle-researcher (surfacing the least-mitigated risk in
  the merkle lane; primary decision owner is the Phase 3 tree-critic)
- Status: **fixed** (WS-1) — the snapshot persist/load + startup `SynthesizeDeletions`
  diff are implemented and tested in `internal/merkle/{snapshot.go,scanner.go}`
  (decision `docs/audit/decisions/ws1/snapshot-and-deletion-synthesis.md`). The
  WS-4 startup wiring is now landed: `internal/reconcile/engine.go`
  `startupReconcile`/`restoreVVs` load the snapshot, restore version vectors, and call
  `SynthesizeDeletions` at boot, and the snapshot is persisted on shutdown +
  periodically (`saveSnapshot`); a missing snapshot enters cold-start reseed
  (vv-counter-seeding). Fixed by commit
  `182ff00a16868df05377cb3585b914aa1d59784e` (WS-1) +
  `af12de099165f38e11556555acc986b9ba385f24` (WS-4 wiring).
- Severity: **high** (this is synthesis risk **R-5**, "the least-mitigated risk";
  no existing rule covers it; the failure is missed deletions / resurrection /
  divergence after a restart)
- Date / access date for all URLs: 2026-06-28
- Reads-first honoured: `docs/audit/findings/synthesis/problem-space-map.md` (R-5,
  OQ-5), `docs/audit/findings/codebases/syncthing-source.md` (D3-1),
  `docs/audit/rules/{sync-rules,go-rules}.md` (SR-9/SR-10/SR-11, GR-7)

## Claim

The in-memory Merkle tree alone **cannot distinguish "a file was deleted while the
daemon was down" from "a file never existed here."** Both look like "absent from the
freshly-scanned tree on startup." Without a **persisted last-synced snapshot** to
diff the startup rescan against, a deletion that happened while the daemon was off is
**missed** (so a peer silently re-creates the file — resurrection), or, symmetrically,
a remote create is mis-read. This is a genuine gap with **no current rule covering
it**; it must be closed by persisting a **local-only** snapshot and reconciling the
startup rescan against it.

## Evidence

- **Absence is ambiguous (the root of it).** Deletions are only safe because they are
  **versioned tombstones**, not absences (SR-9/SR-10; `syncthing-bep` §4.5). But a
  tombstone is only created at the moment a delete is *observed*. If the file is
  removed while the daemon is **not running**, there is no watcher event and no
  tombstone; on restart the scanner simply doesn't see the file — indistinguishable
  from "never existed" unless we remember it *used* to exist.
- **The scanner is not the memory.** The rescan is the source of truth for *current*
  on-disk state (SR-11), but it has no history; it can only diff against something we
  persisted. The synthesis names this explicitly: the in-memory tree "cannot
  distinguish 'deleted while down' from 'never existed'" and rates it **R-5**, the
  least-mitigated risk, with "*no existing rule covers it yet*"
  (`synthesis/problem-space-map.md` §5 R-5, §4 OQ-5).
- **Syncthing has a persistent index DB precisely for this** (`syncthing-source`
  D3-1; `syncthing-bep` §8 — the `index-*` database holds last-known state). Merkle
  Sync deliberately does **not** rebuild a multi-device index DB (synthesis N4) — but
  it still needs *some* persisted last-synced state to derive deletions across a
  restart. The two are different: N4 is a multi-device global-version DB; this is a
  single local snapshot of "what my tree was when I last quiesced."
- **`gob` is acceptable for this** because it is **local on-disk state we wrote
  ourselves**, never bytes from a peer — exactly the case GR-7 permits ("`gob` is
  acceptable only for local on-disk state … never for bytes a peer sent").

## Recommended approach (for the tree-critic to ratify)

1. On clean shutdown (and/or periodically), persist a **local-only** snapshot of the
   current tree — the `FileInfo` set (or the serialized tree) — under the config
   dir, `gob`-encoded (GR-7-permitted for local state). Include each leaf's VV and
   `deleted` flag.
2. On startup, load the snapshot, run a **full rescan**, and **diff rescan vs
   snapshot**:
   - present-in-snapshot, absent-on-disk ⇒ a **deletion that happened while down** ⇒
     synthesize a tombstone (bump VV) just as a live delete would (SR-9), so it
     propagates and resists resurrection (SR-10).
   - absent-in-snapshot, present-on-disk ⇒ a genuine local create.
   - present in both, content differs ⇒ a normal local edit (bump VV per SR-6).
3. Treat a **missing/corrupt snapshot** conservatively: do **not** synthesize
   deletions (absence is then truly ambiguous) — fall back to "rescan = create-only"
   and let the normal VV/tombstone exchange with the peer converge; log it.

## Impact / hand-off

- **Owner:** Phase 3 **tree-critic** (synthesis routes OQ-5/R-5 there), implemented
  across **WS-1** (snapshot persist/load in `internal/merkle`) and **WS-4**
  (startup reconcile in `internal/reconcile/{engine,scanloop}.go`).
- **Acceptance test (deletion-across-restart):** create+sync a file on A and B; stop
  A; delete the file on A's disk; restart A; assert A synthesizes a tombstone from the
  snapshot diff, B removes its copy, and the file is **not** resurrected (extends the
  SR-10 scenario across a restart boundary).
- **Cross-refs:** SR-6/SR-9/SR-10/SR-11, GR-7; synthesis R-5/OQ-5/N4;
  `syncthing-source` D3-1, `syncthing-bep` §8; decision
  `decisions/merkle/leaf-shape-and-structural-hash.md` (§Consequences hands this
  forward).
