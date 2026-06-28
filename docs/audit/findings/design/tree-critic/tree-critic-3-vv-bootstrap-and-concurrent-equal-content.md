---
id: tree-critic-3
title: VV bootstrapping is unspecified (does an initial scan author a bump?) and the resolver has no rule for "concurrent version vectors but identical content" — first sync of an out-of-band-copied folder risks a whole-folder conflict storm
severity: high
status: rejected
phase: 3
role: tree-critic
area: leaf-shape / version-vector semantics for two-way sync
date: 2026-06-28
---

# tree-critic-3 — Two unspecified version-vector branches make first sync of identical-but-independently-scanned content either a conflict storm or a silent overwrite

## Claim

Putting the version vector **inside the structural hash** (so a VV difference makes
the leaf hashes differ and the differ emit the leaf) interacts with two branches the
design never pins down:

1. **Does an *initial* scan of a pre-existing file count as local authorship (a VV
   `Bump`)?** SR-6 defines authorship as "a rescan delta whose new `content_hash`
   differs from the **recorded** one" — but on a first scan there is *no recorded
   one*, so the rule is undefined at the boundary.
2. **What does the resolver do when `Compare` returns `Concurrent` but the two
   leaves have *identical* `content_hash`?** The specified rules cover
   `Concurrent` + *differing* content (→ conflict) and `Equal` + equal content (→
   no-op). The "concurrent VV, identical bytes" cell — which the
   VV-in-the-structural-hash design makes *reachable* — has **no specified action.**

The two consistent ways to resolve (1) lead to opposite failures, and (2) is the
guard that the worst of them depends on. Concretely, the canonical real-world case —
a user copies the same folder to both machines out of band, then pairs them — lands
exactly in this unspecified region. If first-scan bumps and (2) is treated like any
other concurrent diff, **every identical file becomes a `.sync-conflict` copy**: a
whole-folder conflict storm. If first-scan does *not* bump, the storm is avoided but
the design then rests entirely on the cold-start reseed (tree-critic-2 / Option A
guard 2) for the wipe case, with no in-tree backstop.

## Evidence

- **VV is in the structural hash → a VV-only difference forces the differ to emit the
  leaf.** "`version_vector` … **yes** (in structural hash)"
  (`docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md:113`; same in
  `.claude/skills/merkle-sync/SKILL.md:53-57`). The differ recurses into any subtree
  whose hashes differ and emits differing leaves
  (`docs/audit/findings/merkle/MK-2-diff-reconciliation.md:27-52`). So two files with
  identical bytes but different VVs are *not* pruned — they are emitted and handed to
  the resolver.

- **The authorship rule is undefined on first scan.** SR-6: bump "when the scanner
  confirms a genuine local modification (a settled watcher event or a rescan delta
  whose new content_hash differs from **the recorded one**)"
  (`docs/audit/rules/sync-rules.md:90-99`). On the very first scan there is no
  recorded baseline (the snapshot is empty / absent), so whether each freshly-seen
  file is "a delta" (→ bump, e.g. `{A:1}`) or "not yet authorship" (→ empty VV `{}`)
  is a coin the design never flips. MK-3 and the leaf-shape decision specify the VV
  *mechanics* but never the *initial value of a scanned-but-never-edited file*
  (`docs/audit/findings/merkle/MK-3-leaf-metadata-two-way-sync.md:51`).

- **The resolver has no "concurrent + equal content" rule.** PR-2 §4 enumerates
  exactly three actions: `Dominates`/`DominatedBy` → apply, no copy; `Equal` (+equal
  hash) → no-op, (+differing hash) → conflict backstop; "`Concurrent` **AND contents
  differ** → CONFLICT" (`docs/audit/findings/protocol/PR-2-version-vector-comparison.md:72-83`).
  MK-2 repeats only "`Concurrent` + differing content → conflict"
  (`MK-2-diff-reconciliation.md:48-52`). SR-3 idempotency requires *both* content
  *and* version to match ("that exact content+version", `sync-rules.md:48-60`), so it
  does **not** cover concurrent-VV-equal-content. The cell is genuinely empty.

- **The failure is a documented real-world class.** Spurious conflict copies whose
  contents are byte-identical to the synced file are a recurring Syncthing failure:
  users report conflict files where "there was no difference" on `diff`, produced
  when metadata/version state diverges despite identical content
  ([syncthing forum, *conflicts when file only changed on one device*](https://forum.syncthing.net/t/conflicts-when-file-only-changed-on-one-device/16980),
  accessed 2026-06-28). The out-of-band-copy-then-pair scenario is the textbook
  trigger for whole-folder divergence on first sync.

- **Both resolutions of (1) have a cost the design does not acknowledge:**
  - *First-scan bumps* (`{A:1}` vs `{B:1}` for identical files) ⇒ `Compare` →
    `Concurrent` for every shared file ⇒ if (2) is handled like a normal concurrent
    diff, a `.sync-conflict` copy per file (storm); if mishandled as "differing
    leaves ⇒ overwrite," silent loss.
  - *First-scan does not bump* (`{}` vs `{}`) ⇒ `Equal`, identical content ⇒ clean
    no-op (good) — but then a first-ever local edit goes `{}`→`{A:1}` against a peer
    `{}`, and after any **state wipe** the rescan starts at `{}`, `DominatedBy` the
    peer's `{A:5,B:3}`, so the wiped device's edited-while-down file is overwritten
    unless cold-start reseed fires. The whole anti-rollback story then hinges on a
    single mechanism with no in-tree backstop (ties to tree-critic-2).

## Impact

- **First-sync conflict storm (high operational / data-integrity event):** pairing
  two machines that already hold the same content out of band can mint a
  `.sync-conflict` copy of *every file*, doubling the folder and demanding manual
  cleanup. Data is not lost, but a whole-folder conflict storm is a severe,
  user-visible integrity event (the same class as Syncthing #10590's "8,591
  conflicts").
- **Latent silent overwrite (high):** because the "concurrent + equal content" cell
  is unspecified, an implementer may "resolve" it by treating differing leaves as an
  apply/overwrite — silent data loss on a genuine concurrent edit that *happened* to
  collide in bytes is then one wrong line away.
- **Fragile bootstrap:** whichever way (1) is decided, the decision is currently
  implicit, so the two halves of the engine (scanner authorship vs resolver) can be
  implemented under different assumptions and only diverge in the field.

## Recommended change (beats the status quo)

1. **Pin the initial-scan authorship rule explicitly:** an initial scan of a
   pre-existing file is **not** local authorship — it seeds the leaf with an **empty
   version vector `{}`**, not `{A:1}`. A bump happens only on a *subsequent* observed
   change against a recorded baseline (SR-6 as intended). This makes two machines
   that independently scanned identical content compare `Equal` and converge with **no
   conflict** — the correct outcome for the out-of-band-copy case. Log this as a
   decision (`decisions/merkle/` or `decisions/protocol/`) since it is consequential
   and currently undefined.
2. **Specify the missing resolver cell:** `Concurrent` **and** `content_hash` equal ⇒
   **`Merge` the version vectors (pointwise max) and keep the single file; do not
   create a conflict copy.** Both peers compute the same merge and converge; nothing
   is duplicated because nothing differs. Add it to PR-2 §4's table and to SR-3's
   idempotency neighborhood so apply is total over all four `Compare` outcomes.
3. **Keep the equal-VV-differing-content backstop, and add its mirror:** the design
   already routes `Equal` + differing content to a conflict
   (`vv-counter-seeding.md` guard 3); make the resolver a *complete* 2×2 over
   {Equal, Dominates, DominatedBy, Concurrent} × {content equal, content differ} so
   no branch is implementation-defined.
4. **Acceptance test:** copy an identical folder to A and B out of band, then pair —
   assert convergence to the original files with **zero** `.sync-conflict` copies and
   identical roots; and a separate test asserting concurrent edits that collide in
   bytes produce a single merged file, not a conflict copy and not an overwrite.
