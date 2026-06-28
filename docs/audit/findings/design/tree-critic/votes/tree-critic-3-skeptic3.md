# Skeptic #3 vote — tree-critic-3 (VV bootstrap + concurrent-equal-content)

**Vote: REFUTED** (severity overstated; marquee failure modes are precluded by
rules the finding itself cites). Confidence: medium.

## What the finding gets right (the small kernel)

The literal observation is accurate: the resolver's enumerated action table
(PR-2 §4, MK-2 §"after the structural diff") is **not exhaustive over the full
2×2** of {Equal,Dominates,DominatedBy,Concurrent} × {content equal, content
differ}. The specific cell `Concurrent` + `content_hash` equal has no row. And
because the VV sits inside the structural hash
(`decisions/merkle/leaf-shape-and-structural-hash.md:113`), two byte-identical
files with different VVs do have different structural hashes and *are* emitted by
the differ (`MK-2:32`). So the cell is reachable. That much is supported.

That is the entire defensible core. The two **failures** the finding builds on
top of it — a "whole-folder conflict storm" and a "latent silent overwrite" —
are both refuted by the written design.

## Refutation 1 — the conflict storm is explicitly precluded, not "implementation-defined"

The finding's storm requires that `Concurrent` + equal-content be "treated like
any other concurrent diff" and produce a `.sync-conflict` per file. But the
conflict rule is **gated on differing content in two independent places**:

- SR-7: conflict fires when VVs are concurrent "**and the contents differ**"
  (`sync-rules.md:108-114`).
- PR-2 §4: "`Concurrent` **AND contents differ** → CONFLICT"
  (`PR-2-version-vector-comparison.md:81`).
- MK-2: "`Concurrent` **+ differing content**: conflict" (`MK-2:50`).

So a `.sync-conflict` copy is, by the written rule, **impossible** when
`content_hash` is equal. The storm scenario is a strawman of an implementation
that ignores the content gate every one of these rules states. The spec does not
"have no rule" that lets the storm happen — three rules' explicit precondition
*prevents* it.

## Refutation 2 — the cold-start reseed already collapses the canonical scenario to Equal

The finding's canonical trigger (copy a folder out of band to A and B, then pair)
is the **first INDEX exchange with no snapshot on either side** — i.e. *both*
devices are in cold-start reseed mode (`vv-counter-seeding.md:60-64`, guard 2).
Reseed "`Merge`s the peer's VV for every shared path **before** asserting any
local authorship." Work it through under the finding's own worst case (first scan
*does* bump):

- A scans → `{A:1}`, B scans → `{B:1}`.
- First INDEX: A merges B's vector → `{A:1,B:1}`; B merges A's → `{A:1,B:1}`.
- `content_hash` is identical (same bytes), so no "bump on top" fires.
- `Compare({A:1,B:1},{A:1,B:1})` = **Equal**, content equal → SR-3 no-op.

Zero conflict copies, clean convergence — the exact outcome the finding's
recommendation #1 demands, already produced by a mechanism the finding *cites*
(it calls reseed "Option A guard 2"). The finding asserts reseed only covers the
wipe case; it does not — reseed merges VVs for **every shared path** on first
pairing, which is precisely the out-of-band-copy case. The "coin the design never
flips" is flipped: under either the no-bump reading (`{}` vs `{}` → Equal) *or*
the bump reading (reseed merge → Equal), the first pairing converges with no
conflict.

## Refutation 3 — "silent overwrite / data loss" is incoherent for equal content

The Impact section claims a "latent silent overwrite … silent data loss on a
genuine concurrent edit that *happened* to collide in bytes." By definition this
cell has `content_hash` **equal** — both sides hold byte-identical content. There
is nothing to overwrite and nothing to lose: any "apply" is a content no-op
(content-addressed, SR-3). You cannot lose a user's data when both replicas
already hold the same bytes. The high-severity data-integrity framing collapses.

## Refutation 4 — the bootstrapping branch is weak on its own terms

The finding concedes the intended reading: "an initial scan … is **not** local
authorship … (SR-6 as intended)" (rec #1). SR-6 bumps on "a rescan delta whose
new content_hash differs from **the recorded one**" (`sync-rules.md:90-99`); a
first scan has no recorded baseline, so it is not a delta against a baseline →
no bump. The natural reading of the rule already answers the question the finding
calls undefined. And per Refutation 2, the reseed merge makes the answer
**not outcome-determining** for the canonical scenario anyway.

## Severity / "beats the status quo"

Severity **high** is overstated. With the storm precluded (Ref 1+2) and data loss
incoherent (Ref 3), the genuine residual is narrow: should the VVs *merge* so the
two roots become bit-equal (SR-5 convergence) rather than sitting concurrent
forever? That is a **low-severity convergence-completeness clarification**, and it
is already implied by `Merge` semantics (PR-2 §3: `Merge` fires "when accepting an
update") plus the reseed. Recommendation #2 (merge VVs pointwise-max, keep the one
file, no copy) is sensible and harmless — but it "beats the status quo" only on a
minor completeness axis, not on the catastrophic axes (storm / data loss) the
finding claims, which the status quo already prevents.

## Conclusion

The finding inflates a minor "the resolver table isn't literally exhaustive" nit
into a high-severity "conflict storm or silent overwrite," both of which are
precluded by SR-7/PR-2's content gate and the cold-start reseed VV-merge that the
finding itself references. The bootstrapping branch is answered by SR-6 read
naturally and rendered moot by reseed. Worth a one-line table-completion note at
low severity; not a high finding.

**REFUTED** — confidence medium (a kernel exists: add the explicit
`Concurrent`+equal-content → merge-VV-keep-file row for SR-5 completeness, at low
severity).
