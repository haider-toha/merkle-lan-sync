# Skeptic #3 vote — tree-critic-1 (Directories not first-class versioned entities)

- Finding: `docs/audit/findings/design/tree-critic/tree-critic-1-directories-not-first-class.md`
- Skeptic: #3 of 3
- Vote: **REFUTED** (severity overstated; flagship recommendation self-undermining; reduces to a documentation/scope decision)
- Confidence: medium
- Date: 2026-06-28

## What the finding gets right (granting the kernel)

The factual core is accurate and verifiable. The chosen grammar
(`docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md:147-158`) gives
`nodeEncoding` no version vector, no `deleted` byte, and no `mode`, while
`leafEncoding` carries all three. `FileInfo` is per-file
(`.claude/skills/merkle-sync/SKILL.md:33-42`); `INDEX`/`INDEX_UPDATE` carry only
`FileInfo` sets (`SKILL.md:233-234`). So "a directory is a bare structural node"
is true. That much is not in dispute.

But the skeptic's job is to test whether the *consequences*, *severity*, and
*recommended change* hold. They do not.

## Refutation 1 — the flagship recommendation is refuted by the finding's own evidence

The finding's primary recommendation (Option 1) is "emit a `FileInfo` for every
directory, Syncthing `Type=DIRECTORY` style, with VV + tombstone" and claims this
"restores SR-9/SR-10 for dirs → no resurrection." Yet the finding's *own* cited
evidence is that Syncthing — which already does exactly this (first-class
directory `FileInfo` with version and `deleted`) — *still* has the empty-directory
resurrection bug ([syncthing #9371](https://github.com/syncthing/syncthing/issues/9371),
cited in the finding's own Evidence section). So by the finding's own source, the
recommended mechanism demonstrably does **not** prevent the failure it is sold as
preventing. A recommendation that the finding itself proves ineffective cannot
"beat the status quo." It instead imports a whole new entity class (and the
attendant Syncthing-class bugs) for an unproven benefit.

## Refutation 2 — the "high" directory-deletion-resurrection impact collapses on inspection

The finding admits in its own Impact section: "For a *non-empty* directory the
contained-file tombstones mask it." That is the normal case, and the design
handles it correctly — `rm -r photos/` tombstones every contained file
(versioned, dominating deletes, SR-9/SR-10), and a tree built "from `FileInfo`
set" (`structure.md:77`) that has no remaining children simply has no node — the
directory ceases to exist structurally with no separate entity to resurrect.
Strip that admitted case away and the remaining "resurrection" claim is solely
about *empty* directories. Empty directories in a git model are never put on the
wire in the first place, so there is nothing to resurrect — at worst an empty
directory is *not created*, not *resurrected*. Calling a "feature not present"
case "resurrection (high)" relabels an absent feature as a data-integrity bug.
There is no data loss and no clobber in either sub-case; severity "high" is
overstated.

## Refutation 3 — the "permanently non-convergent by design" claim contradicts the model it assumes

The finding asserts SR-5 will "report them as differing forever … because one
side's parent node lists a child the other can never materialise." This assumes
the empty directory *is* a child node in the local tree yet *not* transferable.
But `tree.go` builds the tree "from `FileInfo` set" (`structure.md:77`); under the
pure git model the finding says is adopted, an empty directory contributes no
`FileInfo` and therefore no tree node — so the parent lists no such child, both
roots agree, and the trees converge (with the empty dir simply absent on both).
The finding cannot have it both ways: either empty dirs are not in the tree (→
convergence holds, the only cost is the well-known git "no empty dirs" limitation)
or they are (→ the not-yet-designed WS-4 reconcile layer can create them from node
info, since "no resolution path" presumes a reconcile layer that is not yet
specified). Asserting *permanent* non-convergence "by design" as a certainty is
premature against an unspecified reconciliation stage.

## Refutation 4 — "the design declares this mandatory for directories" misreads MK-3

MK-3 (`docs/audit/findings/merkle/MK-3-leaf-metadata-two-way-sync.md`) is titled
and scoped explicitly to "what metadata each **LEAF** must carry," and its
field-by-field table is about a file. It never declares directories must carry a
VV/tombstone/mode. A directory in a git/Merkle model is a different kind of entity
whose identity *is* its children; because the structural hash already commits to
each child leaf's VV and `deleted` flag (decision §D.1, line 113-114, and the node
hash includes child hashes), "equal root ⇔ converged" already holds for the
directory *state that carries data* — the set of files and their versions. The
"directories violate a mandatory rule the file design declared" framing
manufactures a contradiction that the cited document does not contain.

## Refutation 5 — already an open Phase-3 item; the residue is a documentation nit

The decision explicitly defines the empty-directory encoding as "well-defined"
(`leaf-shape-and-structural-hash.md:181-182`) and explicitly hands directory/leaf
modelling questions to "Phase 3 tree-critic" (lines 167, 240-243). So this is a
known, logged, open design question, not a settled-and-broken decision. The
finding's own Option 3 concedes the legitimate resolution may simply be "git-style,
no empty-dir support, log the scope decision" — i.e. the real residual ask reduces
to a one-paragraph scope statement in SKILL/structure plus removing or marking the
unreachable empty-dir encoding. That is a documentation/scope nit (low–medium),
not a high-severity design defect, and it does not require the costly,
self-refuted Option 1.

## Net

Facts correct; consequences and severity inflated; the recommended fix is shown
ineffective by the finding's own citation; the convergence-failure claim
contradicts the model it assumes; and the genuine residue is a scope-documentation
decision the design already routed to this very phase. The finding does not justify
its "high" severity or its structural recommendation. Refuted.
