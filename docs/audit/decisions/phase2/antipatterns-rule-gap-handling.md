# Decision: how the anti-slop pass handles antipatterns and the rule gaps it finds

- Area: phase2 / antipatterns-researcher
- Status: decided (Phase 2). The **new rules this pass proposes (SR-14..SR-17)
  are PROPOSED, not yet binding** — they are ratified or killed by the Phase 3
  critics + skeptic vote like any other finding.
- Date: 2026-06-28
- Decider: antipatterns-researcher

## Context

My contract (`.claude/agents/antipatterns-researcher.md`) says: catalogue what
makes sync engines **subtly lose data**, tie each antipattern to the hard rule
that prevents it (`SR-n`/`GR-n`/`XP-n`), and **"if no rule covers it, that is a
gap — flag it for the rules set."** The research surfaced ~19 data-integrity
antipatterns. Most map cleanly to an existing rule (SR-1..SR-13, GR-1..GR-13,
XP-1..XP-6). But **five** describe real, cited, severe data-loss modes that **no
current rule names**:

1. A received path/filename is not constrained to the sync root → writes escape it
   (path traversal / "zip slip" / symlink-following).
2. An empty or unverified scan is propagated as a **mass deletion** (deletion-as-
   absence with no root-sanity guard).
3. A reassembled file is renamed into place **without** a whole-file hash check →
   silent corruption is accepted as "converged".
4. A file mutated **while it is being hashed or streamed** has torn content
   propagated as if consistent (a content-level TOCTOU, distinct from SR-11's
   missed-event case).
5. `size`+`mtime` is trusted to **skip** rehashing → an in-place edit that
   preserves mtime is never detected, so a real change never propagates.

The consequential choice is **what to do with those gaps**, because whatever I
choose becomes the checklist Phase 3 critics and Phase 5 implementers act on.

## Options (scored 1–5; criteria adapted for a rules/research artifact:
correctness = does it actually close the data-loss gap · concurrency-safety =
does it compose with the single-writer/RWMutex model without new hazards ·
testability = does it yield a concrete failing assertion · cross-platform = does
it address the Mac↔Windows target)

### Option A — Catalogue only; map to existing rules; note gaps inline, propose nothing
- correctness **2** — names the gaps but leaves them unowned and unactioned;
  implementers may not close them.
- concurrency-safety 3 · testability **2** (no asserted target) · cross-platform 2.
- Rejected: the contract explicitly says to *flag gaps for the rules set*, and a
  data-loss gap with no proposed control is how the bug ships.

### Option B — Catalogue + propose concrete new hard rules (SR-14..SR-17) for the gaps + spin out one individual finding per SEVERE gap / cross-platform refinement (CHOSEN)
- correctness **5** — every gap gets a concrete, testable control and an owner.
- concurrency-safety **5** — each proposed rule is written to compose with GR-5
  (zero I/O under the lock; apply is the single writer) and SR-3 idempotency.
- testability **5** — each antipattern ships a *failing assertion* (the contract's
  requirement) and each proposed rule a "how tested" line.
- cross-platform **5** — the Windows-rename and path/symlink items are framed for
  the exact Mac↔Windows target the project exists to serve.
- Cost: I am proposing rules I do not own (rules-architect owns sync-rules.md).
  Mitigated by labelling SR-14..SR-17 **PROPOSED** and routing them through the
  normal Phase 3 critique/skeptic vote — I do not edit sync-rules.md.

### Option C — Catalogue + open a separate `decisions/` file per gap before proposing any rule
- correctness 4 · testability 4 · but **process-heavy 2**: five more decision
  files in a research pass duplicates what a finding + Phase 3 vote already does.
- Rejected as ceremony that slows the pilot without adding rigour.

### Option D — Close the gaps by directly editing `sync-rules.md` to add SR-14..SR-17 now
- correctness 4 but **overstepping**: `sync-rules.md` is the rules-architect's
  Phase 0 artifact; unilaterally mutating another agent's binding output with
  un-voted rules violates the "flag it for the rules set" instruction and the
  adversarial-vote design (Phase 3 is where findings become binding).
- concurrency-safety n/a · Rejected.

## Decision

Adopt **Option B**. Concretely:

1. Write the full catalogue to `docs/audit/rules/sync-antipatterns.md`. Every
   antipattern carries: the tempting wrong code shape, *why it loses/corrupts
   data (not merely slows)*, a failing-assertion test, the correct approach with
   a current citation (access date 2026-06-28), a **severity**, and the rule it
   maps to — or, if none, a **PROPOSED** rule.
2. For each gap, propose a new rule in the catalogue, numbered **SR-14..SR-17**
   and **clearly marked PROPOSED** (status, not yet binding). Phase 3 ratifies.
3. Spin out an **individual finding** (`docs/audit/findings/antipatterns/*.md`,
   `status: open`) only for the **SEVERE** ones — the genuine gaps plus the one
   cross-platform refinement (Windows non-atomic rename) that the project's
   Mac↔Windows mandate makes critical. Antipatterns already fully prevented by a
   *tested* existing rule stay in the catalogue and cite that rule; they do **not**
   get a duplicate finding.

Severity rubric (data-integrity only; performance is out of scope per contract):
- **critical** — can silently destroy/overwrite data **outside** a single file's
  expected update, or wipe many files (path traversal, mass-delete).
- **high** — silently corrupts or loses a single file's content/edit (no-verify,
  change-during-hash, mtime-skip, Windows-rename, symlink-follow).
- **medium/low** — narrower or already strongly mitigated; catalogue only.

## Rationale

- The contract demands gaps be flagged *for the rules set*; a proposed,
  numbered, testable rule is the most actionable form of "flagged", and routing
  it through Phase 3 respects the adversarial-vote design that makes findings
  binding (`plan/agent_roster.md` Phase 3).
- Spinning out findings only for SEVERE gaps (not for restatements of SR-1/2/4/
  6/7/10/12/13 etc.) keeps the skeptics' Phase 3 load on the items that actually
  need defending, per the roster's "kill weak findings" intent.

## Consequences

- Proposes **SR-14** (received-path containment), **SR-15** (mass-delete /
  empty-scan guard), **SR-16** (verify-after-reconstruct), **SR-17**
  (change-during-hash/transfer detection) as PROPOSED rules for Phase 3.
- Produces 7 individual findings under `docs/audit/findings/antipatterns/`.
- Hands rules-architect (or the Phase 3 consolidator) the job of folding ratified
  SR-14..SR-17 into `sync-rules.md` with a stable ID, exactly as SR-1..SR-13.
- Cross-references R-4/R-5 (synthesis risk register): SR-16/SR-17 harden R-4
  (interrupted/corrupt transfer); SR-15 hardens R-5 (persisted-state /
  deletion-across-restart gap).
