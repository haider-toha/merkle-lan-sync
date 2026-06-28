# Skeptic #2 vote — tree-critic-1 (Directories not first-class)

- Finding: tree-critic-1 — "Directories are not first-class versioned entities — empty-dir
  sync and directory deletion/metadata are broken (no directory version-vector,
  tombstone, or mode)"
- Stance assigned: REFUTE
- Vote: **REFUTED = true** (the finding as stated is overstated and its headline impacts
  are unsupported)
- Confidence: **medium**
- Date: 2026-06-28

## What the finding gets right (conceded)

The narrow structural fact is accurate and verifiable: `nodeEncoding`
(`docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md:154-158`) carries only
`childCount` + `(nameLen, nameBytes, childHash)` and indeed has no VV, no `deleted`, no
`mode`; the leaf fields live only in `leafEncoding` (`:148-152`). And `INDEX` is "a set of
`FileInfo`" (`.claude/skills/merkle-sync/SKILL.md:233`). So "a directory node, as currently
encoded, carries none of the file leaf's two-way metadata" is true on its face.

That concession is the ceiling of the finding's support. Everything load-bearing above it —
the severity, the three "impacts," and the claim that the recommended change beats the
status quo — does not hold up.

## Why I vote to refute

### 1. The flagship "high" impact (permanent non-convergence) rests on an unstated, worst-case implementation assumption that the design's own oracle contradicts.

The Impact section claims empty dirs make "the two trees permanently non-convergent by
design … SR-5's 'equal root ⇔ converged' oracle will report them as differing forever …
because one side's parent node lists a child the other can never materialise."

This only follows if A's local tree is built by **walking the filesystem** (so an empty
`photos/` enters A's root hash) while the **wire** transmits only the `FileInfo` set (so B
never learns of `photos/`). The design never states that. The canonical wire truth is the
`FileInfo` set (`SKILL.md:233-234`), and SR-5 is defined as "converged ⇔ identical root
hash" over that shared state (`leaf-shape-and-structural-hash.md:34,50-63`). The natural
reading — tree derived from the same `FileInfo` set both sides exchange — yields the exact
**opposite** outcome: an empty dir is simply absent from *both* trees, roots match, and the
peers converge (the standard, accepted git behavior: an empty dir is just not tracked). The
finding silently picks the one interpretation that produces a "high" bug and presents it as
"by design," with no file:line establishing the FS-walk-but-index-FileInfo split it
requires. An impact that depends on an unmade implementation choice is speculation, not a
design defect.

### 2. The directory-deletion-resurrection impact is largely self-defeating, by the finding's own admission.

The finding concedes: "For a *non-empty* directory the contained-file tombstones mask it"
(Impact, line 105) and D.4 already shows deleting files tombstones them and re-hashes
ancestors correctly (`leaf-shape-and-structural-hash.md:188-206`). A deleted file becomes a
**tombstone leaf that remains a child** of its directory node, so the directory is not
"empty with a constant hash" — it still has children (the tombstones), and those tombstones
carry dominating VVs that defeat resurrection (SR-9/SR-10). The residual exposure is
therefore confined to (a) the directory *entry itself as a distinct object* and (b)
genuinely-empty directories — a far narrower surface than the "directory-deletion
resurrection (high)" headline implies. The common case (`rm -r` of a populated tree) is
already handled by the very tombstone machinery the finding says is "left wide open."

### 3. The recommended change does NOT demonstrably beat the status quo — the finding's own evidence shows it stays broken.

Evidence bullet 4 cites Syncthing #9371: Syncthing **does** model directories as
first-class `FileInfo` with their own version and `deleted` flag — i.e. exactly the
finding's recommended Option 1 — and **still** has long-standing empty-dir resurrection.
The finding waves this away as giving Syncthing "even a *chance*," but a chance is not a
demonstrated improvement. The autonomy contract's bar for a recommendation is that it
"beats the status quo"; the finding's own cited counter-example shows the recommended
architecture ships the same bug class plus new cost (directory tombstone retention, dir VV
bumps on every child churn, the dir-vs-leaf hashing question). The recommendation is not
established as superior; it may merely relocate the bug while adding complexity.

### 4. The area is an explicitly deferred, open design question handed to *this* critic — flagging it as a "high"-severity broken design overstates a documented-as-open item.

The decision doc explicitly defers directory-representation refinements to Phase 3:
"**Handed to Phase 3 tree-critic:** review the 'name committed by parent only' refinement …
and the persisted last-synced snapshot" (`leaf-shape-and-structural-hash.md:240-243`), and
flags the leaf/name refinement "for the Phase 3 tree-critic" (`:167`). The empty-dir case
is even pre-defined as "well-defined" at `:181-182`. The design also openly adopts the git
tree model (MK-1), whose empty-dir limitation is well known and frequently a *deliberate*
scope choice. The finding's own Option 3 admits the correct resolution may simply be a
one-line scope note in `decisions/`/SKILL — i.e. a documentation gap, not a high-severity
correctness defect. Severity "high" is not warranted for "this deliberately-deferred,
git-inherited area needs a scope sentence."

### 5. The "lost metadata (medium)" sub-claim is partly mooted by the existing field set.

`FileInfo.mode` is documented as "perm bits / **type** / exec bit"
(`SKILL.md:37`; `leaf-shape-and-structural-hash.md:111`). The type bit is precisely how a
directory record (Syncthing `Type=DIRECTORY`) is distinguished. Nothing in the spec asserts
`FileInfo` is file-*exclusive*; the finding assumes that and builds on the assumption. The
machinery to carry directory mode already exists in the field set, so "directory
permission/mode changes never propagate" is an unproven consequence of an implementation
that has not been written, not a closed-design defect.

## Net assessment

One true structural observation (node grammar lacks VV/deleted/mode), wrapped in three
impact claims that are respectively (1) speculative on an unmade implementation choice and
contradicted by the SR-5 oracle, (2) self-admittedly masked in the common case, and (3)
recommending a fix the finding's own evidence shows is still buggy. The genuinely-open part
(empty-dir scope, dir-as-object) is already explicitly deferred to this phase's critic and
is at most a documentation/scope decision. As written — "high severity, directories are
broken, must promote to first-class entities" — the finding is overstated and its
recommendation is not shown to beat the status quo. Per the default-refute rubric for weak/
overstated findings, I vote **refuted = true** (medium confidence; the underlying
empty-dir scope note has merit and should be logged via Option 3, but that does not sustain
the finding as filed).
