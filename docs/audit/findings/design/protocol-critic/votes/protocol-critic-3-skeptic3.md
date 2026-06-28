# Skeptic #3 vote — protocol-critic-3 (conflict-copy non-deterministic identity)

**VOTE: REFUTED** (confidence: medium-high)

## Summary

The finding identifies a genuine *documentation-precision* gap — PR-3 §4's worked
proof spells out only the `<deviceID>` suffix and does not explicitly name the
field that supplies `<date>-<time>` or assign the conflict copy's version vector.
But it then inflates that gap into a HIGH-severity correctness defect ("duplication"
and "non-convergence") by ignoring two load-bearing clauses of the very spec it
critiques. Both inflated outcomes are already prevented by the design as written.
The claim as stated is overstated and partly self-defeating.

## Why the timestamp prong is overstated

The finding's own wording concedes the timestamp source is *unspecified*, then
assumes a worst-case implementer chooses `time.Now()`. But PR-3 does not leave
determinism to chance:

- The finding is literally titled "deterministic & symmetric" and §4 requires the
  copy be "**identically named**" on both peers.
- §6 calls the UTC formatting "a **cross-platform determinism requirement**"
  (`PR-3-conflict-copy-policy-and-tiebreaker.md:126`), i.e. determinism of the
  whole name is an explicit obligation, not just timezone-neutrality.
- Test obligation **#3** asserts "the **same conflict-copy filename** is produced
  on both peers" for the equal-mtime case (`:140`) — the *full* filename, which
  includes the timestamp. Test **#5** pins the UTC suffix across TZs (`:143`). An
  implementation that used `time.Now()` would **fail test #3** and never ship.

So the timestamp determinism is mandated and *tested*; only the exact source field
is unnamed in the prose. And the obvious, already-implied answer — the loser
`FileInfo`'s replicated mtime — is exactly what the finding itself recommends
(rec #1). That is a clarity edit to one sentence, not a HIGH design flaw.

## Why the version-vector prong is refuted

The finding's central correctness claim — "same-named copy under different VVs ⇒
different leaf hashes ⇒ the engine can never reach equal root" — assumes the
conflict copy is a one-shot artifact whose VV must be byte-identical *at creation*.
That contradicts the explicit design:

- PR-3 §2: "the renamed copy is **fed back into the scanner so it itself syncs**"
  and conflict copies "are **treated as normal files** after they are created, so
  they are propagated between devices" (`:44-46`).

The conflict copy therefore converges by the **same SR-5 reconciliation engine
that converges every other file**. Two peers independently creating a file at the
same new path with *initially different VVs* is the **normal pre-convergence
state of any file**, not a permanent divergence — if differing initial VVs caused
permanent non-convergence, the engine would not work for a single ordinary edit.

Crucially, the two conflict-path files hold the **loser's identical bytes** (same
`content_hash`). So when they reconcile:

- It is **not** a real conflict: PR-3 conflict resolution runs only when
  "Concurrent **AND contents differ**" (`:50-51`, SR-7). Equal content ⇒ no
  conflict-of-conflict, no recursion, no second `.sync-conflict` copy, no
  "duplication."
- VVs reconcile by dominate/merge exactly as for any file; SR-3 idempotent,
  content-addressed apply (`sync-rules.md:48-60`) plus the documented
  "equal-VV-differing-content backstop" route (PR-3 §8) cover the residual cases.
  Content is equal here, so there is no data hazard — only VV merge to a common
  value, then equal roots (SR-5).

The transient leaf-hash difference the finding points at is real but is precisely
the divergence SR-5 is defined to resolve ("after changes settle"). The finding
conflates "initial VV differs" with "never converges."

## Why the recommendation does not clearly beat the status quo

Rec #1 (loser's mtime) is already implied by §6 + tests #3/#5 — fine as a wording
clarification. Rec #2 ("inherit the loser's VV verbatim" on both peers) is worse
than the status quo: the conflict copy is a **new path**; the scanner sees a new
file and, per **SR-6**, the authoring device bumps its own counter. Forcing both
peers to assign a verbatim inherited VV requires special-casing that *suppresses
the authorship bump on conflict-rename*, fighting SR-6/SR-8 and adding complexity —
to buy a byte-identical-at-creation property the status quo does not need because
the file converges normally. Rec #3 (adopt Syncthing's asymmetric single-creator
model) is a legitimate alternative, but the finding never shows the symmetric model
is *broken*; it shows only that Syncthing chose differently. A symmetric,
deterministic creation of an identical-content file followed by normal sync is a
valid (arguably simpler) design.

## Verdict

A real but minor spec-clarity nit (name the timestamp field; one sentence on the
copy's VV in the §4 proof) dressed up as a HIGH-severity duplication/non-convergence
defect. The duplication outcome is blocked by "concurrent AND contents differ"
(equal content ⇒ no copy). The non-convergence outcome is blocked by "treated as a
normal file that syncs" + SR-5. Severity is overstated and the headline correctness
claim does not hold. **Refuted.**
