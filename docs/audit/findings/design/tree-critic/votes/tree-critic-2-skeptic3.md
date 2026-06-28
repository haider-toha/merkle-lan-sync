# Skeptic #3 vote — tree-critic-2 (REFUTE assignment)

- Finding: tree-critic-2 — "Periodic / clean-shutdown snapshot persistence is not
  crash-safe, reopening the VV counter-rollback data-loss hole."
- Vote: **REFUTED (severity overstated; exposure mischaracterised; recommended change
  does not clearly beat the documented status quo)**
- Confidence: medium
- Date: 2026-06-28

## What I verified

I read the finding and all primary evidence it cites:
`docs/audit/decisions/protocol/vv-counter-seeding.md` (Option A, three guards, the
hard-dependency consequence), `docs/audit/findings/merkle/MK-6-persisted-snapshot-restart-deletion.md`
(persist "on clean shutdown (and/or periodically)", the step-2a synth-tombstone, the
missing/corrupt step-3 handling), and `docs/audit/rules/sync-rules.md` (the
crash-mid-transfer failure model). The textual quotes the finding pulls are accurate.
The mechanical core — a stale snapshot can carry a per-file VV counter that is behind
a value already broadcast — is *not* fabricated. So this is not a vacuous finding.

It is, however, **weak and overstated** on the three axes that matter for a HIGH
severity design defect.

## Refutation 1 — the data-model is per-path VVs, and the ordinary INDEX exchange (not just guard 2) repairs the rollback

The whole worked example treats A's rolled-back counter as a poisoned high-water mark
that stays poisoned until A re-edits. That ignores how a 2-device LAN tool actually
recovers. The VV is `map[ShortID]uint64` **per file** (vv-counter-seeding.md:11;
leaf metadata in plan/README.md:175). There is no single global device counter; file
`f`'s A-slot is independent of every other file.

On reconnect, B sends its index with `f = {A:7}` (content X). A's restored-from-stale
state is `f = {A:5}` (content X — identical bytes, A merely forgot the bump). The
*normal* domination-based apply (SR-3 / SR-4 convergence) sees B's version dominates,
adopts it, and A's per-file A-slot is restored to 7 via element-wise `Merge`. This is
plain sync convergence, not a special guard. The finding's entire analysis of "all
three Option A guards miss it" is aimed at guard 2 (cold-start reseed) and guard 3
(equal-VV backstop), but the actual repair path here is the **bread-and-butter INDEX
adoption** the engine runs on every reconnect — which the finding never mentions. For
every file the peer holds at a higher VV, the stale counter self-heals on the next
index exchange.

## Refutation 2 — "exposure is every crash" is false; it is a narrow pre-reconnect, same-file race

Given Refutation 1, silent data loss requires a precise conjunction:
1. a non-clean crash **after** an `INDEX_UPDATE` broadcast but **before** the next
   periodic snapshot (already a bounded window: snapshot interval × edit rate), AND
2. on restart, a **local re-edit of that exact file** that happens **before** A
   completes the INDEX exchange with B for that path.

If the re-edit happens after the index exchange (the common case for a daemon that
dials its peer on startup), A's per-file slot is already merged up to {A:7}, so the
next bump is {A:8} and there is no collision. The finding's claim — "Exposure is
**every crash**, not a rare race" (line 107) — is the opposite of true: it is exactly
a rare race (two independent timing conditions plus a same-file edit), not a
guaranteed consequence of any crash. Overstating exposure is precisely the
"severity overstated" failure I am asked to check.

## Refutation 3 — the recommended change does not clearly beat the documented status quo

The status quo is **not** "do nothing." vv-counter-seeding already carries Option B
(hybrid `max(prev+1, now)`) as the *documented fallback* adopted "iff the cold-start
reseed proves too costly" (vv-counter-seeding.md:86-89, 101-102, 128-129). Option B
makes a rolled-back counter degrade to a **safe conflict copy** rather than a silent
overwrite (vv-counter-seeding.md:41-44) — which closes the very FM-4 trap this finding
re-raises, for free, without a synchronous per-bump fsync. The finding even concedes
this (its recommendation §1 parenthetical). So the design *already* contains an
adjudicated escape hatch for exactly this risk.

The finding's headline recommendation (§1: an append-only fsync'd monotonic counter
log written with SR-1/SR-2 discipline **before every outbound broadcast**) adds a
synchronous fsync on the hot send path — a real latency/throughput cost on a LAN
sync engine — to buy a guarantee that Option B already provides cheaply, or that
ordinary INDEX merge provides for free in all but the narrow race window. "Beats the
status quo" is not established.

## Refutation 4 — it restates an already-flagged, already-routed dependency

vv-counter-seeding.md:126-129 explicitly logs: "**Hard dependency on OQ-5/R-5** … If
that snapshot is not delivered, the rollback guarantee weakens … flagged for the
Phase 3 protocol-critic and concurrency-critic." The finding's own closing paragraph
admits it is "the load-bearing dependency vv-counter-seeding already flagged." A
design finding that sharpens an already-documented, already-routed open dependency,
while overstating its exposure and proposing a fix the design already has a cheaper
documented alternative for, is a *medium-at-most* refinement note, not a standalone
HIGH defect.

## Concessions (why confidence is medium, not high)

The narrow race window is genuinely real: edit-same-file-before-reconnect after a
post-broadcast crash *can* produce a DominatedBy silent overwrite under pure Option A
with a stale snapshot. The sibling false-authorship-tombstone concern is also
directionally valid (though it converges to a conflict — a safe outcome per the prime
directive — once B re-feeds its tombstone). So the finding is not *wrong*; it is
**overstated in severity and exposure, and weak on "beats the status quo."** Under the
instruction to default to REFUTE when a finding is weak/overstated, and given the
documented Option-B fallback already neutralises the data-loss outcome, I vote to
refute as filed. A reframed MEDIUM note ("when shipping under pure Option A, gate
guard-2 reseed on 'snapshot provably behind' and add a kill-9-between-snapshots test")
would be defensible — but that is a downgrade, not a ratification of this HIGH finding.

## VOTE: REFUTED
