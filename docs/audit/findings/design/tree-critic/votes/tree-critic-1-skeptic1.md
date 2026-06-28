# Skeptic vote — tree-critic-1 (skeptic #1 of 3)

- Finding: tree-critic-1 — "Directories are not first-class versioned entities — empty-dir
  sync and directory deletion/metadata are broken (no directory version-vector, tombstone,
  or mode)"
- Vote: **REFUTED** (severity overstated; headline impacts unsupported or internally
  contradictory; recommended change does not beat the status quo — proven by the finding's
  own evidence)
- Confidence: medium
- Date: 2026-06-28
- Skeptic: design skeptic #1

## What the finding gets right (acknowledged, not disputed)

The narrow, verifiable technical core is **true**:

- `nodeEncoding` in the chosen grammar carries no VV / `deleted` / `mode`
  (`docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md:154-158`). Only
  `leafEncoding` does (`:148-152`).
- `INDEX`/`INDEX_UPDATE` are "a set of `FileInfo`" and `FileInfo` is a per-file record
  (`.claude/skills/merkle-sync/SKILL.md:33-42,233-234`), so a directory with no files
  emits no `FileInfo`.
- The design adopts the git tree model (MK-1), and git genuinely cannot represent empty
  directories.

So the *observation* is accurate. The finding fails on **severity, impact, and remedy** —
the three things this vote is asked to test.

## Refutation 1 — the headline "high" impact (SR-5 permanent non-convergence) is internally contradictory and false

The Impact section claims empty directories make the two trees "permanently non-convergent
by design … SR-5's 'equal root ⇔ converged' oracle will report them as differing forever …
because one side's parent node lists a child the other can never materialise."

This is self-contradictory. The tree is **built from the scanned `FileInfo` set / scanned
entries**, not from some external ground truth. The very mechanism the finding identifies —
an empty directory produces no `FileInfo` — means the empty directory is **absent from the
producing side's tree too**. It is therefore never a child of any parent node on either
side. The exclusion is **symmetric**: A's root hash does not reflect `photos/` because A's
scanner emitted nothing for it; B never had it. Roots stay equal. SR-5 reports
**converged**, not "differing forever." The scenario the finding describes (one side lists
a child the other cannot materialise) cannot arise from the stated cause, because the cause
removes the child from *both* the tree and the wire at once.

The honest characterisation is "an empty directory is silently not mirrored" — a (minor)
missing-data nicety, **not** a convergence failure and **not** an SR-5 violation. The
finding inflates a silent no-op into a permanent oracle failure. That is the load-bearing
"high" justification, and it does not hold.

## Refutation 2 — the recommended change does NOT beat the status quo (the finding's own evidence proves it)

The recommendation (Option 1) is "promote directories to first-class `FileInfo` with VV +
tombstone + mode, exactly like Syncthing's `Type=DIRECTORY`." Yet the finding itself cites
**syncthing #9371** — Syncthing implements *precisely* this recommendation (first-class
directory `FileInfo` with version + `deleted`) and **still** has the empty-directory
resurrection / "put back" bug. The finding tries to spin this ("a system that doesn't is
strictly more exposed"), but the plain reading is fatal to the remedy: the proposed
mechanism is demonstrably **insufficient** to fix the cited failure mode in a mature,
production sync engine. A recommendation whose own supporting citation shows it does not fix
the problem cannot be said to "beat the status quo." It imports substantial new complexity
(per-directory version vectors, directory tombstones, file-vs-directory-at-same-path
conflict resolution, create-parent-before-child and delete-children-before-parent ordering
constraints) and still ships the bug.

## Refutation 3 — directory deletion of populated directories already converges

The finding concedes (`Impact`, deletion bullet) that "for a *non-empty* directory the
contained-file tombstones mask it." That concession is the whole game for the realistic
case: `rm -r photos/` tombstones every contained file
(SKILL.md §4, `leaf-shape-and-structural-hash.md` D.4 lines 188-206), the tombstones
propagate with dominating VVs (SR-9/SR-10, resurrection-resistant), and both sides converge
to the directory containing zero live entries. The empty directory node hashes to the same
well-defined constant `SHA-256(0x01 || 00000000)` on both peers
(`leaf-shape-and-structural-hash.md:181-182`), so **the roots still match**. The only
residue is whether an empty folder *shell* remains on disk. That is a cosmetic/metadata
detail, not data loss, and certainly not a "high" SR-10 hole. The finding labels this "high"
while simultaneously admitting tombstones handle the data.

## Refutation 4 — directory `mode` loss is out-of-scope by the design's own declared posture

"Lost metadata (medium)" for directory permission bits ignores that the design already
declares `mode` **best-effort and lossy on the cross-platform target** (XP-6;
SKILL.md:37,274-276). File `mode` is already best-effort across Mac↔Windows; directory
traversal/exec bits are even less portable between APFS and NTFS ACLs. Flagging "directory
mode change invisible" as a *defect* when the design has already scoped `mode` portability
as best-effort is double-counting a known, accepted limitation. Low, not medium — and
arguably not a finding at all under the project's stated scope.

## Refutation 5 — empty-dir non-support is a deliberate, standard model the design adopted with eyes open

The finding asserts the design "inherits this limitation without acknowledging it." That is
partly false: the leaf-shape decision **explicitly defines** the empty-directory encoding
and its hash (`leaf-shape-and-structural-hash.md:181-182`), i.e. the designers considered
empty directories and gave them a deterministic representation. The git tree model is a
deliberate, widely-accepted choice (MK-1), and "no empty-dir tracking" is its
best-understood tradeoff (used by git for ~20 years without being classed a data-loss bug).
At most this is a **documentation/scope gap** — recommendation #3 ("log it as a scope
decision; mark the unreachable empty-dir grammar line dead") is the cheap, proportionate
fix. That is a low-severity cleanup, not the "high" structural overhaul the finding leads
with.

## Net assessment

The finding bundles one true-but-minor observation (empty directories are silently not
mirrored; `nodeEncoding` omits dir VV/tombstone/mode) with three overstated impacts:

1. the "high" SR-5 non-convergence claim is internally contradictory and false (Refutation 1);
2. the "high" directory-deletion-resurrection claim is conceded away for populated dirs and
   reduces to a cosmetic empty-shell residue (Refutation 3);
3. the "medium" mode-loss claim double-counts an already-scoped best-effort limitation
   (Refutation 4);

and a recommended remedy that the finding's **own** Syncthing citation shows does not fix
the problem while adding real complexity (Refutation 2).

A legitimate but small kernel survives: empty-directory non-support should be stated plainly
in SKILL/structure and the unreachable empty-dir grammar line marked dead (the finding's own
Option 3). That is a low-severity documentation item, not a high-severity "directories are
broken" defect. As written — severity `high`, "broken," first-class overhaul "required" —
the finding is not supported.

**Vote: REFUTED.** Confidence: medium (the technical observation is real, but the severity,
the two "high" impacts, and the recommended change as stated do not survive scrutiny).
