# Skeptic #2 vote — protocol-critic-3 (conflict-copy non-deterministic identity)

**Vote: REFUTED** (confidence: medium)

## What the finding gets right (conceded)

- PR-3 prose does not *name the source* of the `<date>-<time>` token.
- PR-3 / SR-7 / SKILL §3 do not *explicitly assign* the conflict copy's version
  vector.

Those are real gaps in the **prose**. The finding fails, however, on its two
load-bearing claims — that these gaps cause (a) duplication and (b) *permanent
non-convergence* on a high-severity path. The surrounding design already forecloses
both. The finding reads PR-3 §6 in isolation and ignores PR-3 §4 + §7 and PR-2 §4.

## Refutation 1 — the "duplication via `time.Now()`" reading is precluded by PR-3's own test obligations

The finding's duplication impact requires an implementer to use `time.Now()` on each
side. But PR-3 does **not** merely hint at determinism — it *mandates and tests* it:

- §4 (Worked symmetry check, lines 106-109): both peers must end with an
  "**identically named** `.sync-conflict-…-<M>.txt`".
- §6 (lines 124-128): "UTC, so **both peers format the same string**"; "Both peers
  compute the same loser ⇒ same suffix."
- §7 Test obligations #3 and #5
  (`docs/audit/findings/protocol/PR-3-conflict-copy-policy-and-tiebreaker.md`
  lines 140-143): "Equal mtime → assert the **same** conflict-copy filename is
  produced on **both** peers"; "two instances in different `TZ` produce the identical
  suffix."

A `time.Now()` implementation **fails test #3 by construction**. The design as
specified (requirement + acceptance test) does not admit the duplication failure; an
implementation that exhibits it is rejected by the spec's own gate. The finding's
citation of Syncthing's `conflictName` (which uses `time.Now()`) is a non-sequitur:
Syncthing is single-creator and PR-3 *explicitly diverges* from copying it verbatim
on naming — §6 substitutes a deterministic UTC recipe precisely because the model is
symmetric. The "implementer naively ports Syncthing" scenario contradicts the spec it
is supposedly implementing.

The finding's own recommended fix #1 ("timestamp = the loser `FileInfo`'s mtime,
already replicated, identical on both peers") is the obvious — and only —
determinism-satisfying reading of §6 + §7#3. That is a one-line prose clarification,
not a correction of a *wrong* decision. The recommended change does not "beat" the
status quo on correctness; it just writes down what the test obligation already
forces.

## Refutation 2 — "permanent non-convergence via unpinned VV" is contradicted by PR-2 §4

The finding's non-convergence claim is: same-named copy under different VVs ⇒
different structural hashes ⇒ "the engine can never reach equal root."

This treats the divergent-VV state as terminal. It is not. By construction the
conflict copy holds the **loser's bytes**, which *both* peers possess (the loser
`FileInfo` is replicated on the wire). So if M's copy carries VV `{M:1}` and P's
copy carries `{P:1}`, the two leaves are **Concurrent with identical `content_hash`**.
PR-2 §4 (`docs/audit/findings/protocol/PR-2-version-vector-comparison.md` line 81)
makes a conflict copy **only** when "`Concurrent` AND contents differ." Concurrent +
same content is benign: the engine merges the VVs to `{M:1,P:1}` and both leaves
become byte-identical → convergence after one extra exchange.

So the worst case under the finding's unfavourable reading is a *transient* extra
round-trip, not the claimed "can never reach equal root / cannot signal done."
Permanent non-convergence would require the engine to mishandle concurrent-same-content
— a *different*, hypothetical bug not attributable to PR-3, and one PR-2 already
specifies away. The finding's Impact section overstates a transient into a terminal
data-integrity failure.

(Note the finding's own internal tension: it argues "non-convergence **even though
both hold both versions' bytes**." Holding identical bytes under concurrent VVs is
exactly the merge-and-converge case, not a stuck state.)

## Severity is overstated

The actual residue, after accounting for §7's tests and PR-2 §4, is: *PR-3 should
state in prose (i) the timestamp = normalized loser mtime and (ii) the copy inherits
the loser's VV verbatim.* That is a documentation-tightening / acceptance-test-pinning
task, with at most transient runtime effect even under the adversarial reading — not a
"high" no-data-loss defect. No version's bytes are ever lost in any reading (the loser
is renamed, never deleted; SR-7 holds throughout). The headline ("non-convergent
conflict copies", "high") does not survive contact with PR-3 §4/§7 and PR-2 §4.

## Conclusion

The finding correctly spots that two tokens are not pinned in PR-3's prose, but
mischaracterizes a prose/acceptance-test clarification as a high-severity correctness
defect. Its duplication scenario is excluded by PR-3 §7 test #3; its non-convergence
scenario is excluded by PR-2 §4 (concurrent-same-content merges). The recommended
change is a reasonable doc tightening but does not beat the status quo on correctness,
and the severity is overstated.

**REFUTED — medium confidence.** (Confidence is not "high" because the VV-of-copy line
genuinely deserves to be written into PR-3 §6; a maintainer may legitimately want that
captured as a low/medium documentation follow-up rather than dismissed entirely.)
