# Skeptic #1 vote — crossplatform-critic-2 (REFUTE brief)

- Vote: **REFUTED** (severity overstated, central data-loss claim unsupported by its
  own cited evidence)
- Confidence: medium
- Reviewer role: skeptic #1 of 3, tasked to refute.

## What the finding must prove to earn its severity (high = silent data loss)

The finding's *only* data-loss mechanism is the "engine fold **finer** than NTFS
upcase" direction: a pair `(K1,K2)` where `fold(K1) != fold(K2)` (engine treats them
as two files) **but** `upcase_NTFS(K1) == upcase_NTFS(K2)` (NTFS stores one slot),
so writing K2 overwrites K1. The finding itself concedes the other direction (engine
coarser) is "Bad, but not data loss" — only over-refusal. So the entire `high`
severity rides on demonstrating that **finer pairs actually exist** for the
SimpleFold-vs-NTFS-`$UpCase` pairing.

## Refutation 1 — the cited evidence points the WRONG way (supports the harmless direction)

The load-bearing citation (DFIR.ru) is: "Unicode 13 (2020) introduced **169 new
case-folding entries** compared to version 8 (2015)," and `$UpCase` is **frozen** at
format time at an older Unicode version. Combine those two facts:

- Newer Unicode SimpleFold conflates **more** pairs than an old frozen `$UpCase`.
- Therefore the engine fold is **coarser** than (a stale) NTFS table for exactly the
  characters this evidence is about.
- Coarser = **over-refusal**, which the finding admits is *not* data loss.

The finding's own headline evidence demonstrates the availability direction, not the
silent-clobber direction. The parenthetical "e.g. case pairs added to Unicode after
the volume's `$UpCase` was frozen" (lines 76-77) is offered as proof of *finer* pairs
but is itself a *coarser*-direction example. The evidence contradicts the claim it is
attached to.

## Refutation 2 — zero concrete finer-direction counter-example

The contract's evidence standard ("no memory-only claims; ground current facts...
cite them") demands a concrete codepoint pair where NTFS `$UpCase` conflates and
`unicode.SimpleFold` does not. The finding cites:
- the a-z-passes-through-the-table quirk — but `$UpCase` maps `a->A` exactly as
  SimpleFold folds `a/A`; this quirk produces **no** new conflation beyond Unicode,
  so it yields no finer pair; and
- "newer Unicode" — which is the coarser direction (see Refutation 1).

Not one runnable repro or named codepoint pair is given for the only mechanism that
causes the alleged data loss. By the "default refuted if the finding is weak, vague,
or unsupported" rule, the high-severity claim is unsupported.

## Refutation 3 — severity is overstated relative to what is evidenced

Strip the unproven finer-direction and what remains is: the engine's fold may not be
provably equal to the target's equality relation, with the *evidenced* consequence
being **over-refusal on stale-`$UpCase` volumes** — an availability/convergence
nuisance, recoverable, no bytes lost. That is medium at most, not high. The design
already treats refuse+flag as the safe default and retains both versions in the tree
(decision Option 2B), so the evidenced direction degrades gracefully by design.

## Refutation 4 — the genuinely-useful remedy is already inside the design's toolkit

Recommended-change 2(b) (pre-write existence check on the OS name via directory
listing; refuse+flag if the OS already holds an equal-by-its-rules entry under a
different canonical key) is exactly Syncthing's "detect the real on-disk case using
directory listing methods," which the design **already cites and adopts** as its
detection technique (`case-and-normalization-collision-policy.md` lines 47-49, 103-104;
`case-sensitivity.md` lines 59-61). The Consequences also already forbid
`os.OpenFile`+truncate and route through a clash check before materialising. So the
robust fix is not a reversal of the status quo — it is a one-line tightening
("consult the live listing, not just the in-memory fold index, immediately before
`os.Rename`") of an approach the design already endorses. Recommended-change 2(a)
(probe per "character classes in use") is itself unsound for the same reason it
criticises: you cannot enumerate the Unicode space, so it remains a guess.

## Honest caveat (does not rescue the finding)

A theoretical soundness gap (fold not *provably* >= target equality) is real, and
finer pairs plausibly exist for some locale-specific characters. But the finding does
not carry that burden of proof, mis-directs its only quantitative citation, and
inflates an availability nuisance into "high / silent data loss." A finding may not
claim data loss it never evidences. The actionable kernel (tighten the pre-write
check to the OS verdict) survives as a low/medium design-hygiene note — not a
high-severity unsoundness.

## Verdict

REFUTED. The high severity rests on a finer-direction clobber that the finding's own
evidence argues against and never exemplifies; the evidenced behaviour is recoverable
over-refusal; and the sound part of the recommendation is already in the design's
cited mechanism.
