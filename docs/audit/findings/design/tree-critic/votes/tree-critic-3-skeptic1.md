# Skeptic #1 vote — tree-critic-3 (VV bootstrap + concurrent-equal-content)

- Finding: `docs/audit/findings/design/tree-critic/tree-critic-3-vv-bootstrap-and-concurrent-equal-content.md`
- Role: skeptic #1 of 3 — job is to REFUTE
- Vote: **REFUTED** (severity overstated; marquee scenario already prevented; recommendation #1 is already the design)
- Confidence: medium
- Date: 2026-06-28

## Summary

The finding bundles a real-but-minor documentation gap (PR-2 §4's action table does
not contain an explicit `Concurrent + equal content` row) inside a "high-severity
conflict storm / silent overwrite" wrapper. When the cited evidence is read in full,
the two dramatic impacts do not survive: the conflict-storm scenario is already
prevented by the existing rules the finding itself cites, and the silent-overwrite
impact is a no-op by construction. The lead recommendation is not a change at all —
it is already the specified behaviour. I refute the finding *as framed*; the residual
kernel is at most a low/medium completeness edit, not a high data-integrity finding.

## Point-by-point

### 1. Claim "initial-scan authorship is undefined" — NOT supported.

SR-6 defines authorship as "a settled watcher event or a rescan delta whose new
content_hash differs from **the recorded one**" (`sync-rules.md:90-99`). On a first
scan there is no recorded baseline, so there is **no delta against a recorded one** —
which means, by the plain reading of SR-6's own definition, an initial scan is **not**
authorship and does **not** bump. The boundary is determined, not "a coin the design
never flips."

It is determined a *second* time, independently, by the chosen seeding decision.
`decisions/protocol/vv-counter-seeding.md` Option A guard 2 (cold-start reseed)
explicitly says a device with no snapshot "Merges the peer's VV for every shared path
**before** asserting any local authorship; a local file whose `content_hash` **differs**
from the merged-VV version is then bumped." Identical content ⇒ the differ-from-merged
condition is false ⇒ no bump ⇒ convergence. The bootstrap is pinned by two existing
artifacts the finding itself cites.

Notably, the finding's own **Recommendation #1** ("initial scan is not authorship;
seed empty VV; bump only on a subsequent observed change against a recorded baseline,
SR-6 as intended") is a verbatim restatement of SR-6's existing semantics. A
recommendation that equals the status quo cannot "beat the status quo."

### 2. The marquee "whole-folder conflict storm" — NOT reachable under the chosen design.

The finding's storm is explicitly conditional: "**If** first-scan bumps … a
`.sync-conflict` copy per file." But first-scan does **not** bump (point 1). Under the
actual design the out-of-band-copy case is `{} vs {}` ⇒ `Compare = Equal` ⇒
content-identical ⇒ SR-3 no-op. No conflict copy is minted. The finding constructs the
storm out of a branch (first-scan-bumps) that SR-6 + the reseed decision already
foreclose, then attributes the resulting severity to the finding. That is a strawman of
the design.

### 3. "Concurrent + equal content" cannot produce a conflict copy — SR-7/MK-2 already gate it.

SR-7 fires "when version vectors show two edits … are **concurrent** … **and the
contents differ**" (`sync-rules.md:108-114`). MK-2 repeats the gate verbatim:
"`Concurrent` **+ differing content** → conflict" (`MK-2-diff-reconciliation.md:48-52`).
The conflict-copy path is **gated on content differing**. The complement — concurrent
VV with *identical* bytes — therefore does **not** satisfy the SR-7 trigger and cannot
mint a `.sync-conflict` copy. The "storm" requires the resolver to treat
concurrent-identical as a conflict; the specified resolver does the opposite by
construction. The cell is "unlabelled in the PR-2 table," but it is not "an
implementation-defined conflict trigger" — SR-7's explicit content-differs predicate
already excludes it.

### 4. "Latent silent overwrite (high)" — a no-op by construction.

The impact posits an implementer "resolving" concurrent-equal-content by overwriting.
But the cell is, by definition, **identical `content_hash`** on both sides. Overwriting
identical bytes with identical bytes loses nothing; SR-3 (content-addressed idempotent
apply) makes it a no-op (no write, no event). The "genuine concurrent edit that happened
to collide in bytes" is, byte-for-byte, the same file — there is no second version to
lose. The "one wrong line away" data-loss framing is speculative ("an implementer may…",
"may resolve it…") and, even if an implementer did the wrong thing, there are no
distinct bytes to destroy. This does not clear the bar for a *high* data-integrity
finding.

### 5. What actually remains — a low/medium completeness edit.

The one valid kernel: PR-2 §4's table does not list an explicit `Concurrent + content
equal` row, and the VV-in-the-structural-hash design does make a `content equal, VV
divergent` leaf *visible* to the differ (it is emitted rather than pruned). The only
real consequence is potential **cosmetic non-convergence / re-emit churn**: the
structural hashes stay unequal until the VVs reconcile, so the differ may re-flag the
leaf on each cycle. That is a tidiness/SR-5-completeness issue, not data loss and not a
conflict storm. The finding's Recommendation #2 (merge VVs pointwise max, keep the
single file) is a sensible completeness addition and worth a one-line table entry — but
it is a minor hardening, not a high-severity fix, and it should be filed as such rather
than under a conflict-storm headline.

### 6. The cited external evidence is weakly connected.

The Syncthing "conflicts when file only changed on one device" forum thread and the
8,591-conflict #10590 case (`PR-2:108-110`) are ghost-counter / de-pairing pathologies,
already owned by `vv-pruning-counter-cleanup.md`. They are not the
"first-scan-of-out-of-band-copy" path the finding claims, and they do not demonstrate
that Merkle Sync's specified rules (SR-6 + SR-7 + reseed) produce the cited storm.

## Verdict

REFUTED. Severity is overstated (the two "high" impacts are, respectively, a
foreclosed-branch strawman and a no-op-by-construction). Recommendation #1 already *is*
the design. The single legitimate item — adding an explicit `Concurrent + content
equal ⇒ Merge VVs, no copy` row to PR-2 §4 and SR-3's neighbourhood — is a
low/medium completeness edit that does not need a high-severity design finding to carry
it. If the consolidator wishes to keep anything, keep only that one-line table-cell
addition at reduced severity.

VOTE: REFUTED
