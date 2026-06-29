# Decision (WS-4): total resolver over Compare×content, deterministic conflict identity, sync-loop gating

- Area: ws4 / internal/reconcile (apply.go, conflict.go, broadcast.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-4 implementer
- Plan items: WS-4 #2 (conflict copy, neither lost, byte-identical name on both),
  #4 (received file ⇒ zero outbound broadcasts), #5 (resolver total over
  Compare×content).
- Reads-first: PR-2 (`Compare` → Equal/Dominates/DominatedBy/Concurrent; `Merge`),
  PR-3 (`W` total + commutative; loser renamed never deleted; UTC name;
  larger-ShortID-loses tiebreak), CDD-3 (the missing cells: Concurrent+equal ⇒ Merge;
  Equal-VV+differing-content ⇒ conflict; unknown tombstone ⇒ no-op; initial scan
  seeds `{}`), CDD-4 (conflict-copy identity is a pure function of replicated fields;
  timestamp = loser mtime truncated to whole seconds, UTC), PR-6 + SR-6/SR-8/SR-3 (the
  three sync-loop guards), `decisions/protocol/vv-counter-seeding.md` (pure `prev+1`,
  cold-start reseed, equal-VV-differing-content backstop), `internal/merkle`
  (`FileInfo`, `SetDeleted`, `Version.Compare/Merge/Bump`).

## Context

After `merkle.Diff` flags a differing path, the engine must decide, for the
`(localFI, remoteFI)` pair, exactly one total action with no silent overwrite and no
spurious conflict, **identically on both peers** (so they converge). The decision is a
function of `Compare(localV, remoteV)` × whether `content_hash` is equal, plus the
single-sided/tombstone cases. Separately, applying a received file must produce **zero
outbound `INDEX_UPDATE`** (PR-6), or the watcher echo becomes an infinite sync loop.

## Options — resolver structure (scored: correctness / concurrency / testability / cross-platform)

### Option S1 — branch ad hoc per message handler
- correctness **2** — cells get missed (the exact CDD-3 gap: Concurrent+equal,
  Equal+differ, unknown tombstone); "total over the matrix" is unprovable. Rejected.

### Option S2 — one pure `resolve(local, remote *FileInfo, self) Action` total over the matrix (CHOSEN)
A single pure function returns one of a closed set of actions; the engine merely
executes the action under the lock + via the transfer layer. The matrix:

| Compare(localV,remoteV) | content_hash | Action |
|---|---|---|
| (remote only, local nil) | remote live | **Fetch** remote (create) |
| (remote only, local nil) | remote tombstone | **NoOp** (unknown tombstone — never create/re-mint, CDD-3) |
| (local only, remote nil) | — | **Keep**/advertise local (peer will fetch) |
| Equal | equal | **NoOp** (idempotent, SR-3) |
| Equal | differ | **Conflict** (never silent overwrite — CDD-3/vv-seeding backstop) |
| Dominates | any | **Keep** local (we are causally newer; peer applies ours) |
| DominatedBy | any | **AdoptRemote** — Fetch (live) or ApplyTombstone (deleted); Merge VV; **no copy** |
| Concurrent | equal | **MergeVV** (pointwise max, keep one file, no copy — CDD-3) |
| Concurrent | differ | **Conflict** (keep both, loser → conflict copy — SR-7/PR-3) |

- correctness **5** — every cell is enumerated; "total" is a table test, not a hope;
  matches PR-2 §4 + CDD-3 exactly.
- concurrency-safety **5** — `resolve` is pure (no I/O, no lock); the engine maps its
  result to a fetch/apply/conflict step.
- testability **5** — `TestResolver_*` drive the table directly incl. the three CDD-3
  cells and the SR-10 tombstone-dominates-absent substrate.
- cross-platform **5** — pure integer/byte comparisons.

## Options — conflict winner `W` + copy identity

### Option W1 — "local always wins" / first-writer-wins
- correctness **1** — asymmetric: each peer keeps its own copy ⇒ divergence + data
  loss. Rejected (FM-6).

### Option W2 — total, commutative `W` over intrinsic fields + pure-function copy name (CHOSEN)
`W(a,b)` (returns the winner; the other is the loser), evaluated in order:
1. `mtime` differs → **newer mtime wins** (older loses; SR-7, the only use of mtime).
2. else author (`ModifiedBy`/largest-ShortID-in-VV) differs → **smaller ShortID wins**
   (larger ShortID loses — Syncthing/SKILL §3).
3. else (defensive) → **smaller `content_hash` wins** (bytewise; a real conflict has
   differing hashes so this never ties).
Conflict-copy path = `conflictName(loser)` =
`<stem>.sync-conflict-<YYYYMMDD>-<HHMMSS>-<shortID-hex>.<ext>`, where the timestamp is
the **loser's `mtime` in UTC truncated to whole seconds** (CDD-4 — Mac-ns vs Windows/
FAT rounding agree) and `<shortID-hex>` is the loser's authoring ShortID. The name is
re-canonicalised + Windows-escaped on the OS boundary (XP-3).
- correctness **5** — total (tiers exhaustive, tier-3 a strict total order over
  distinct hashes) + commutative (each tier a symmetric relation on intrinsic fields)
  ⇒ both peers pick the same loser and the same filename (PR-3 §4 proof).
- concurrency-safety **5** — pure. testability **5** — `TestW_Commutative` (property)
  + `TestConflict_SymmetricCopyName` (incl. differing `TZ`). cross-platform **5** —
  UTC + whole-second truncation + per-component escaping.

**Conflict materialisation (symmetric, convergent).** On `Concurrent+differ` (or the
`Equal+differ` backstop) at path `P`:
- set `P` ← winner's `FileInfo` with `Version = Merge(localV, remoteV)` (both peers
  compute the same merged VV + same winner content ⇒ `P` converges); materialise the
  winner's content (local-reuse-or-fetch; a tombstone winner just removes the file).
- **iff the loser has content** (not a tombstone), set `conflictName(loser)` ← loser's
  `FileInfo` (content = loser's `content_hash`, `Version = loser.Version`);
  materialise the loser's content (the losing side copies its own bytes locally before
  overwriting `P`; the winning side fetches them, or receives them via the loser's next
  index). A losing *tombstone* creates no copy (nothing to preserve). Either way
  **no content is ever dropped** and both peers reach the same `{P, conflictName}`
  leaves ⇒ identical root (SR-5/SR-7).

## Options — sync-loop gating (PR-6)

### Option L1 — broadcast on every tree change
- correctness **1** — A writes → B applies → B's watcher fires → B broadcasts → ∞.
  Rejected (R-2).

### Option L2 — bump+broadcast only on confirmed local authorship; three guards (CHOSEN)
1. **Bump only on local authorship (SR-6):** a settled local change whose freshly
   hashed `content_hash` differs from the **recorded** `files[path]` leaf ⇒ `Bump`
   self's counter + queue an `INDEX_UPDATE`. Applying a received file calls **`Merge`,
   never `Bump`**, and emits nothing.
2. **Expected-hash record (SR-8):** on apply, record `expected[path]=H` under the lock
   (and `files[path]` already reflects `H`); the settled-change handler treats a path
   whose new hash == the recorded leaf as "no new authorship" ⇒ no bump, no broadcast.
   It filters by **content identity**, not by muting the watcher, so a genuine
   concurrent user edit (different bytes) IS still detected (PR-6 §5).
3. **Idempotent content-addressed apply (SR-3):** incoming `content_hash`+VV already
   equal to the local leaf ⇒ literal no-op (no write, no event).
- correctness **5** — `apply ⇒ 0 outbound` is provable (PR-6 §4); cross-refs SR-3/6/8.
- concurrency-safety **5**, testability **5** (`TestApply_ZeroOutboundBroadcasts`,
  `TestApply_IdempotentRedelivery`), cross-platform **5**.

## Decision

**S2 + W2 + L2.** One pure `resolve` total over the Compare×content matrix (incl. the
three CDD-3 cells and `{}`-seeded initial scan); a total+commutative `W` with the
UTC-whole-second pure-function conflict name (CDD-4); symmetric conflict
materialisation that keeps both versions and converges; and the three PR-6 guards so a
received file yields zero outbound broadcasts. Cold-start reseed (no snapshot): on the
first peer INDEX, `Merge` the peer's VV into every shared path with equal content
before asserting any local authorship (neutralises FM-4 rollback); the
equal-VV-differing-content ⇒ conflict cell is the residual backstop.

## Rationale

- A single enumerated table is the only way to *prove* totality and to make both peers
  agree on every cell — the prerequisite for symmetric convergence (CDD-3, FM-6).
- `W` over intrinsic fields (never "local vs remote") is what makes the loser + the
  copy filename identical on both peers without coordination (PR-3 §4).
- Symmetric materialisation removes the "rename deletes the only copy" hazard: `P`
  always stays present with the winner's content; the copy is a pure new create — no
  tombstone ambiguity.
- The three guards are defence-in-depth: even if a broadcast leaked, the applied leaf's
  merged VV dominates ⇒ the peer's apply is a no-op (the loop cannot sustain).

## Consequences

- Drives `apply.go` (`resolve` + action executor + idempotence/expected-hash),
  `conflict.go` (`W`, `conflictName`, materialisation plan), `broadcast.go`
  (authorship-gated `INDEX_UPDATE`, creates-before-deletes ordering).
- A losing tombstone makes no conflict copy (no data to preserve) — documented,
  tested. The unknown-tombstone NoOp means an ancient tombstone for a never-seen path
  is not adopted (bounded; for 2-device v1, the peer retains its own tombstone — safe,
  never resurrects).
- Cross-refs: PR-2/3/6, CDD-3/4, SR-3/4/5/6/7/8, vv-counter-seeding.
