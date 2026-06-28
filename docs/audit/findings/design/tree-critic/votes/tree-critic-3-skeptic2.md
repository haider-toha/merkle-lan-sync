# Skeptic #2 vote — tree-critic-3 (VV bootstrap + concurrent-equal-content)

- Finding: `docs/audit/findings/design/tree-critic/tree-critic-3-vv-bootstrap-and-concurrent-equal-content.md`
- Verdict: **REFUTED** (severity overstated; marquee scenario already specified; one residual gap is real but low, not high)
- Confidence: medium-high
- Date: 2026-06-28

## What I checked (evidence read)

- `docs/audit/rules/sync-rules.md` — SR-3, SR-4, SR-6, SR-7.
- `docs/audit/findings/protocol/PR-2-version-vector-comparison.md` §3, §4.
- `docs/audit/decisions/protocol/vv-counter-seeding.md` — Option A (CHOSEN) and its **three guards**.
- `docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md` — VV in structural hash (D.1).

## The finding bundles two claims. Both are weaker than the "high" framing.

### Claim 1 (initial-scan authorship is undefined → first-sync conflict storm) — REFUTED

The finding's central worry is that the initial VV value (`{A:1}` vs `{}`) is "a
coin the design never flips," and that if first scan bumps, the out-of-band-copy
pairing mints a `.sync-conflict` copy of *every* file. This is refuted by an
**already-specified** mechanism the finding mentions only to dismiss:
`vv-counter-seeding.md` Option A **guard 2 (cold-start reseed)** fires on
`"no snapshot is found (true wipe / **first run**)"` — i.e. it explicitly covers
the first-pairing case, not just the wipe case the finding claims.

Guard 2 makes the undefined coin **moot**, because *both* interpretations converge
with **no conflict**:

- First scan seeds `{}` on both: reseed Merges → `{}`/`{}`; identical
  `content_hash` ⇒ `Compare` = `Equal` + equal hash ⇒ SR-3 no-op. No conflict.
- First scan bumps `{A:1}` / `{B:1}`: each device, in reseed mode, **Merges the
  peer's VV before asserting authorship** → both become `{A:1,B:1}`; and a local
  file is bumped on top **only if its `content_hash` differs from the merged-VV
  version** (`vv-counter-seeding.md:60-65`). For identical bytes it is **not**
  bumped ⇒ both sides reach `{A:1,B:1}` + identical hash ⇒ `Equal` ⇒ no-op.

So the marquee "whole-folder conflict storm" requires *ignoring the specified
reseed*. The finding's own decisive lever ("the two consistent ways to resolve (1)
lead to opposite failures") collapses: the existing design routes both ways to the
same safe outcome. The conflict-storm impact — the only thing carrying the "high"
operational severity and the Syncthing #10590 analogy — is therefore not supported
for the scenario the finding chose as canonical.

### Claim 2 (no resolver rule for concurrent-VV + equal content) — REAL but LOW, not HIGH

This cell *is* literally unspecified: PR-2 §4 conditions on `"Concurrent **AND
contents differ** → CONFLICT"`, and SR-3's no-op requires *both* content and
version to match, so `Concurrent` + identical bytes is genuinely uncovered. Credit
where due. But:

1. **The reachable trigger is narrow.** With the out-of-band case handled by reseed
   (claim 1), the residual way to reach this cell is two devices *independently
   editing a file to byte-identical content*. That is rare and, by the finding's own
   admission, **"Data is not lost."** The worst real outcome is an occasional
   spurious, byte-identical conflict copy — a cosmetic/cleanup nuisance of the exact
   minor class in the cited Syncthing forum thread, not a data-integrity event.

2. **The "latent silent overwrite (high)" impact is unsupported.** It rests on
   *hypothetical implementer deviation* ("an implementer may resolve it by treating
   differing leaves as an apply/overwrite… one wrong line away"). The design
   explicitly forbids this: PR-2 §4 routes `Concurrent` to conflict, SR-7 mandates
   the loser is **renamed, never deleted**, and SR-4 forbids content/leaf-difference
   from driving an overwrite. A conforming implementation cannot reach silent loss
   here; "an implementer might code it wrong" is not a design defect and cannot
   justify a high severity.

3. **The recommended change is a clarification, not a status-quo-beating fix.** The
   "complete 2×2" + "Concurrent+equal ⇒ Merge VVs, keep one file" rule is sound and
   worth adding — but it is a one-line table-completion that prevents an occasional
   identical conflict copy. That is a **low/minor** documentation hardening, not the
   high-severity, two-failure-mode defect the finding describes.

## Why refuted overall

- The finding's headline (first-sync conflict storm from undefined bootstrapping)
  is already prevented by the **specified** cold-start reseed, which explicitly
  covers "first run," and which makes the allegedly-undefined initial VV value
  irrelevant to the outcome.
- The one genuine residual (specify the `Concurrent` + equal-content cell) is real
  but **low severity, no data loss**, and the silent-overwrite escalation is
  unsupported speculation about implementer error against rules that explicitly bar
  it.
- A "high" finding whose marquee scenario is handled and whose second impact is
  unsupported is overstated. The kernel survives only as a minor table-completion
  nit, which does not meet the bar for this finding as written.

VOTE: REFUTED
