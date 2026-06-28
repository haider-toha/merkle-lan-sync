# Skeptic #2 vote — tree-critic-2 (snapshot not crash-safe / VV rollback)

- Finding: `docs/audit/findings/design/tree-critic/tree-critic-2-snapshot-not-crash-safe-vv-rollback.md`
- Vote: **REFUTED** (refuted = true)
- Confidence: medium
- Date: 2026-06-28
- Skeptic: skeptic #2 of 3

## Summary

The finding cites its sources accurately (MK-6 step 1 really does say "on clean
shutdown (and/or periodically)"; vv-counter-seeding guard 1 really does phrase the
guarantee as covering "a normal daemon restart"). But the load-bearing piece of
evidence — the "Worked rollback (silent data loss)" example in
`tree-critic-2…md:50-63` — is **technically wrong**: when you replay it with MK-6's
*own* startup rule included, it lands in a guard-3 conflict copy or a benign
same-content convergence, **not** silent data loss. The marquee claim is therefore
unsupported by the example offered for it, the residual hole is far narrower than
"every crash," and the design already documents and mitigates the exact dependency
this finding re-raises.

## Refutation 1 — the worked example omits the MK-6 startup rescan-vs-snapshot bump, and that omission is the whole bug

The finding's example (steps 1–5) assumes that after the crash A reloads stale
`{A:5}` and a new local edit bumps **directly** `{A:5}→{A:6}` (one bump). That skips
the step MK-6 itself mandates.

Replaying with MK-6 step 2 (`MK-6-…md:57-61`, "present in both, content differs ⇒ a
normal local edit (bump VV per SR-6)"):

- At crash, A's **disk** holds the content of the last broadcast edit, `X2`
  (the `{A:7}` bytes — editing writes to disk). Only the *snapshot* is stale at
  `{A:5}` / content `X0`.
- On restart A rescans, sees disk `X2` ≠ snapshot `X0` ⇒ **MK-6 step 2 fires and
  bumps once**: `{A:5}→{A:6}`, content `X2`. The finding's step 4 silently consumes
  this same `{A:6}` for a *brand-new* edit instead — it spends the counter twice in
  the narrative but only once on disk.

Now the two terminal cases:

- **Case A (A reconnects to B before the next local edit):** B has `{A:7}`/`X2`;
  A has `{A:6}`/`X2` — *same content*. `Compare({A:6},{A:7})` ⇒ DominatedBy ⇒ A adopts
  `{A:7}`/`X2`. The "overwrite" replaces `X2` with identical `X2`. **No data loss.** A
  subsequent edit bumps from `{A:7}` and Dominates cleanly.
- **Case B (user edits before reconnect):** A goes `{A:6}`/`X2` → bump → `{A:7}`/`X3`.
  B reconnects with `{A:7}`/`X2`. `Compare({A:7},{A:7})` ⇒ **equal VV, differing
  content** ⇒ **guard 3 fires ⇒ conflict copy** (`vv-counter-seeding.md:64-65`).
  **No silent loss.**

So contrary to the finding's "All three Option A guards miss it"
(`tree-critic-2…md:65-71`), **guard 3 does catch the presented case**, precisely
because the startup snapshot-diff bump re-aligns the counter onto the collision point.
The mechanism is self-correcting: a stale VV implies stale snapshot *content* too
(both are recorded atomically in one snapshot), so the startup diff always detects the
content delta and bumps. The finding's example is the strongest evidence it offers and
it does not hold.

## Refutation 2 — "exposure is every crash" is overstated

The residual hole that *does* survive Refutation 1 requires a **conjunction**, not a
bare crash: (a) multiple un-snapshotted edits that were each **broadcast** to the peer
(so the peer's counter is ≥ 2 ahead of the snapshot), AND (b) a crash, AND (c) a new
local edit **after** restart but **before** the LAN reconnect/INDEX exchange (seconds
on a LAN), AND (d) the post-restart deficit still > 1 after the single startup bump.
That is a narrow race, not the claimed "every non-clean exit"
(`tree-critic-2…md:107-108`). The Impact section's "silent data loss (high) … every
crash" framing is inflated by an order of magnitude relative to what the corrected
analysis supports.

## Refutation 3 — the dependency is already documented and mitigated; the recommendation does not clearly beat the documented status quo

vv-counter-seeding **already flags this exact dependency** and ships a documented
fallback (`vv-counter-seeding.md:126-129`): "Hard dependency on OQ-5/R-5 … If that
snapshot is not delivered, the rollback guarantee weakens … the §'fallback' hybrid
floor must be reconsidered — flagged for the Phase 3 protocol-critic and
concurrency-critic." Option B (hybrid `max(prev+1, now)`,
`vv-counter-seeding.md:76-89`) neutralises counter rollback **with zero per-bump
durability** — it degrades any rollback to a safe conflict copy
(`vv-counter-seeding.md:41-44`). So the "hole" is a known, escalated, already-mitigated
dependency, not an un-addressed silent gap.

The finding's recommendation #1 — fsync a monotonic counter log **before every
outbound INDEX_UPDATE** (`tree-critic-2…md:116-124`) — puts a synchronous fsync on the
hot broadcast path for every local change. That is a real, measurable cost, and it is
not shown to beat the already-documented Option B fallback, which buys the same
rollback resistance for free. A recommendation that adds hot-path fsync without
comparing against the cheaper mitigation the project already wrote down does not
"beat the status quo" as required.

## Refutation 4 — the sibling false-authorship tombstone is cosmetic, not data loss

The false-authorship case (`tree-critic-2…md:80-90`) results in A synthesising a
tombstone for a file the peer also deleted. Both sides agree the file is **deleted**;
the worst outcome is a spurious "conflict on a deletion" / extra VV churn that
converges to the correct state (file stays deleted). No user bytes are lost. The
finding itself rates this "medium," and on inspection it is closer to cosmetic. It
does not lift the overall severity to "high."

## Honest residual (why confidence is medium, not high)

A genuine narrow window survives (Refutation 2, case where the post-restart deficit is
still > 1 and a local edit precedes reconnect): there the collision is
`Compare({A:k},{A:k+n>k})` ⇒ DominatedBy with *different* content ⇒ guard 3 does not
fire ⇒ silent overwrite. This is real. But: (i) the finding never demonstrates this
case — its presented example is the disproven single-edit one; (ii) it is a narrow
race, not "every crash"; (iii) it is already covered by the documented Option B
fallback the decision flagged. A finding whose marquee evidence is incorrect, whose
severity/exposure is overstated, and whose fix is not weighed against the existing
documented mitigation is weak as written. Per the skeptic charter ("kill weak
findings; default refuted if weak/unsupported"), I vote to refute. If the author
re-grounds it on the corrected multi-edit-deficit example, scoped to medium severity,
and argues Option-A-must-stand vs Option-B explicitly, it would merit reconsideration.

## Vote

**REFUTED** — central worked example is technically wrong (guard 3 + the MK-6 startup
bump catch it), exposure/severity overstated, dependency already documented and
mitigated by the Option B fallback, recommended hot-path fsync not shown to beat that
fallback.
