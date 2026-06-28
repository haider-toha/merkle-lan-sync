# Decision — Phase 3 consolidation: how to dispose of unanimously-refuted findings that still carry skeptic-conceded kernels

- Area: phase3 / design-consolidation
- Date: 2026-06-28
- Author: design-consolidator
- Status: decided (acted on in `docs/audit/findings/design/consolidated/overview.md`)

## Context

Phase 3 produced 16 adversarial design findings across four critics (tree, protocol,
concurrency, crossplatform). Each finding was voted on by 3 skeptics whose job is to
refute. The consolidation rule (task brief + `plan/agent_roster.md:67-68`):

> Keep findings where >=2/3 skeptics **failed to refute** (verified); else rejected.
> i.e. KEEP only when `refuted <= 1`.

I read all 16 finding files and all 48 vote files. The result is unambiguous and
uniform: **every finding drew 3/3 REFUTED votes** (`refuted = 3` for all 16). By the
mechanical rule, all 16 are therefore **REJECTED** — none reaches the verification bar.

But the votes are not flat dismissals. In nearly every case all three skeptics:
1. **concede the technical core is real** ("the arithmetic is right", "the mechanism
   is technically real", "the underlying OS facts are accurate", "the cell is genuinely
   uncovered"), and
2. **refute on severity / framing / remedy** (the "high"/"medium" grade is overstated;
   the marquee scenario is precluded by an existing rule; the recommended fix restates
   the status quo or is harmful), and
3. **explicitly instruct the consolidator to keep a downgraded kernel** — e.g. "keep
   only that one-line table-cell addition at reduced severity", "fold its one actionable
   nugget into …", "salvage rec (b) as a Phase-6 hardening note", "downgrade to a
   one-line nit on GR-13", "a one-line clarification to the decision would fully address
   the real content".

So the question this decision settles is: **what does the consolidated overview carry
forward** when the rule rejects 100% of findings but the reviewers unanimously identify
a real, low/medium, actionable residue in each?

## Options

### Option A — Strict discard. Mark all 16 rejected; carry nothing forward. Overview lists rejections only; "verified/merged decisions" sections are empty.
- correctness: **Low.** Mechanically faithful to the rule, but throws away genuine,
  unanimously-conceded defects the reviewers themselves said to fix (discovery-registry
  ownership wording, the engine outbound-send rule, conflict-copy timestamp determinism,
  the ToOSPath ordering consolidation, the filesystem-verdict no-clobber check). The
  evidence (the votes) explicitly contradicts discarding them.
- concurrency-safety: **Low.** Drops the cc-1..4 hardening every skeptic endorsed.
- testability: **Low.** Drops the concrete `-race`/leak/back-pressure/round-trip test
  obligations the skeptics agreed are worth adding.
- cross-platform: **Low.** Drops the ToOSPath consolidation, the pre-rename existence
  check, and the per-directory case granularity — all conceded.
- Net: faithful to the letter, defeats the purpose; Phase 4 gets nothing actionable.

### Option B — Reject-and-distil (CHOSEN). Mark all 16 rejected per the rule; separately distil the unanimously-conceded kernels into a small set of severity-corrected, workstream-tied, de-duplicated "consolidated design decisions", with full provenance to the source findings.
- correctness: **High.** Honors the rule (every finding `status: rejected`) **and**
  discharges the consolidator's actual mandate ("merge duplicates/overlaps into
  project-wide design decisions"). Each carried item is exactly what >=3 reviewers said
  to keep, at the severity they assigned.
- concurrency-safety: **High.** The four concurrency kernels merge into one explicit
  engine-concurrency contract for the planner.
- testability: **High.** Each consolidated decision carries the skeptic-endorsed test.
- cross-platform: **High.** The crossplatform kernels merge into a filesystem-verdict
  no-clobber decision + a canonical ToOSPath decision.
- Net: the only option that satisfies both the rule and the pipeline's purpose.

### Option C — Consolidator override. Keep some findings as "verified" despite 3/3 REFUTED because the consolidator judges the kernel important.
- correctness: **Low.** Directly violates the consolidation rule and makes the
  adversarial vote meaningless. The skeptics also raised *correct* rebuttals (e.g.
  cc-4's factual error that a double `net.Conn.Close()` returns an error, not a panic;
  cp-2's observation that its own divergence evidence points the *safe* way), so
  re-promoting findings at their filed severity would propagate overstated/!incorrect
  claims into Phase 4.
- concurrency-safety / testability / cross-platform: no advantage over B; same content,
  mislabelled as "verified high".
- Net: unauthorised and propagates refuted framing.

## Decision

**Option B.** Mark all 16 findings `status: rejected` (every one drew `refuted=3`, i.e.
0/3 skeptics failed to refute, below the `refuted<=1` keep bar). Then distil the
unanimously-conceded, severity-corrected kernels into a small set of **Consolidated
Design Decisions (CDD-1..CDD-8)** in `overview.md`, each de-duplicated across overlapping
findings, tied to a workstream, and carrying its test obligation and provenance.

The overview states plainly: **zero findings were verified** (none met the >=2/3 bar),
so there are no "verified findings"; the CDDs are the *merged decisions* — the actionable
carry-forward the reviewers explicitly asked to retain.

## Rationale

- The rule governs **findings**, not the consolidator's separate duty to "merge
  duplicates/overlaps into project-wide design decisions." Rejecting a finding-as-filed
  and retaining a reviewer-endorsed downgraded kernel are not in tension; the votes
  literally instruct both.
- Carrying the kernels forward is grounded in the evidence (the vote files), not in my
  own judgement overriding the vote — which keeps the adversarial process meaningful
  (Option C avoided) while not discarding real bugs (Option A avoided).
- Severity is taken from the skeptics' corrected assessment (mostly low; one medium —
  the crossplatform filesystem-verdict no-clobber check), never from the finding's filed
  grade.

## Consequences

- All 16 finding files get `status: rejected`.
- `overview.md` has: (A) method + verdict tally; (B) "Verified design decisions: none,
  with reason"; (C) the rejected list with the load-bearing refutation for each; (D) the
  8 merged Consolidated Design Decisions with workstream ties, provenance, severity, and
  tests.
- Phase 4 (planner) consumes the 8 CDDs, **not** the rejected findings, and must not
  re-introduce any finding at its filed (overstated) severity.
- Two findings' headline remedies are explicitly recorded as **rejected and not carried**
  because skeptics showed them harmful: protocol-critic-4 rec #2 (quarantine every
  peer-only path on reseed — breaks first sync) and the "high-severity / corrupts every
  path" framings of crossplatform-critic-1/3 (based on a misreading of a per-component
  escaping spec). Their *kernels* (a documented wipe-limitation; a consolidated escaping
  spec) are carried; their overstated remedies/claims are not.
