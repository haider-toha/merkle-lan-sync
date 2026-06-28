# Skeptic-3 vote on crossplatform-critic-3

- Finding: crossplatform-critic-3 ("Reversible-escape spec is self-contradictory;
  IsWindowsUnsafe-gated escaping is non-injective; `a:b` -> `a%3Ab` clobbers a
  literal `a%3Ab`; the fold/collision index never sees the escaped OS namespace")
- Vote: **REFUTED**
- Confidence: **high**

## What the finding claims

That `EscapeForWindows` is *predicate-gated per name* — invoked only when
`IsWindowsUnsafe(component)` is true — so a Windows-legal-but-escape-looking name
(`a%3Ab`, contains no reserved char) is written verbatim and collides on disk with
the escaped form of `a:b` (`-> a%3Ab`), causing silent clobber. It calls the
decision and the repro "two different functions" and says no injectivity guarantee
exists.

## Why it does not hold

1. **The escape is platform-gated, not name-gated. The finding misreads the
   "Where" clause.** `illegal-name-strategy.md:102-106` reads: "escape happens in
   the `ToOSPath` boundary conversion **only on the platform/volume that rejects
   the name** (**always on Windows targets**; on macOS/Linux the original is
   already legal so no escape)." The parenthetical is explicit: the gate is
   *Windows vs macOS/Linux*, i.e. **always on every Windows-target component**, not
   "only the components the predicate marks unsafe." The finding silently rewrites
   "the platform that rejects the name" into "the names the predicate calls unsafe,"
   then derives a contradiction from its own rewrite.

2. **The `% -> %25` rule is dead code under the finding's reading, which proves the
   reading is wrong.** The decision (`:88-92`) states: "A literal `%` in the
   original is **first escaped to `%25`** (makes the scheme **total/reversible**)."
   If escaping ran only on predicate-unsafe names, a `%`-only name such as `a%3Ab`
   is *already* Windows-legal, would never trip the predicate, and the `% -> %25`
   rule could never fire — making it pointless. The decision's own stated rationale
   for that rule ("makes the scheme total/reversible"; "total" = defined on all
   inputs) only makes sense if `EscapeForWindows` is applied universally to every
   Windows component. So the spec self-evidently means universal escaping, and
   under universal escaping `a%3Ab -> a%253Ab` while `a:b -> a%3Ab`: **distinct
   outputs, no clobber.**

3. **The repro the finding cites as "the opposite behaviour" is in fact the spec,
   and confirms universal `%`-escaping.** `filename-legality.md:77` shows
   `"100%done" -> "100%25done"  unsafe? false`: a name the predicate marks **safe**
   still has its `%` escaped. The finding admits this is "the *correct* (injective)
   behaviour." There is no contradiction to resolve — the repro is the normative
   implementation of the decision, and it escapes `%` universally exactly as the
   decision's `% -> %25` "total" rule requires. Both artifacts describe the **same**
   total function.

4. **Round-trip already guarantees injectivity — the finding's central "no
   injectivity guarantee" claim is mathematically false.** The consequences pin
   `unescape(escape(x)) == x` for the whole hostile table
   (`illegal-name-strategy.md:129`, `filename-legality.md:91`). A function with a
   left inverse is injective **by definition**: if `escape(a:b) == escape(a%3Ab)`
   then `unescape` would have to return both `a:b` and `a%3Ab` from one string —
   impossible. So the round-trip assertion *is* a cross-input injectivity
   guarantee. Recommendation #2 ("round-trip but not cross-input collision")
   rests on a math error.

5. **The collision-index "namespace gap" (rec #3) is moot.** Its premise is that
   escaping *introduces* collisions into OS-name space that the canonical-key fold
   index cannot see. But because `escape` is injective (point 4), it introduces
   **zero** new collisions — distinct canonical keys map to distinct on-disk names.
   The fold index correctly operates on the canonical key because the canonical-key
   layer is exactly where logical identity lives; the escaped layer is collision-
   free by construction, so there is nothing for it to police.

## Severity assessment

Claimed "medium / silent data loss (SR-7 violation)." Actual data-loss risk under
the real spec: **none** — the converter is injective and round-trip-lossless. The
alleged self-contradiction is an artifact of the finding's misreading of one clause
against the decision's own explicitly-stated, internally-consistent design. The
only residual is editorial: the "must NOT change" label on `100%done`
(`filename-legality.md:100`) is loosely worded given the repro escapes its `%`, and
an explicit injectivity *unit* test would be nice belt-and-suspenders. Those are
doc-polish items, not a medium-severity correctness defect.

## Verdict

Refuted. The "predicate-gated, non-injective" reading contradicts the decision's
"always on Windows targets" platform gate, its "total/reversible" `% -> %25` rule,
and the cited repro — all of which describe one universal, injective transform.
The round-trip guarantee already implies the injectivity the finding says is
missing, and an injective escape cannot create the OS-namespace collisions the
recommendation tries to guard against.
