# Skeptic #1 vote — crossplatform-critic-3 (REFUTE)

- Finding: "Reversible-escape spec is self-contradictory; IsWindowsUnsafe gating is
  non-injective; `a:b` and a literal `a%3Ab` clobber; fold index never sees the
  escaped OS namespace."
- Vote: **REFUTED**
- Confidence: **high**

## Summary

The finding's entire data-loss mechanism depends on one interpretive move:
reading "escape happens ... only on the platform/volume that rejects the name"
as **per-name** gating (skip any component the predicate calls safe). The
authoritative artifacts say the opposite — gating is **per-platform**, and the
escape transform itself is unconditionally `%`-escaping and therefore injective.
The decision's own primary requirement (reversibility), the runnable
reproduction it cites, and its explicit refuse+flag fallback each independently
defeat the claimed clobber. The three recommendations are already satisfied,
mathematically redundant, or moot.

## Point-by-point

### 1. The gating is per-platform, not per-name — the finding drops the disambiguating clause

The finding quotes `illegal-name-strategy.md`:
> "escape happens in the `ToOSPath` boundary conversion **only on the
> platform/volume that rejects the name**..."

and stops there. The very next words in the source (line 102-103) are:
> "(always on Windows targets; on macOS/Linux the original is already legal so
> no escape)."

"Always on Windows targets" is platform-level gating: on a Windows target the
transform always runs; on Mac/Linux it never runs. There is no per-component
"skip the safe ones" semantics. The finding manufactures the contradiction by
omitting the clause that resolves it.

### 2. The reproduction the finding cites proves the injective behaviour IS the spec

`filename-legality.md` line 77 (the runnable `scratchpad/escapeproto` table):
```
"100%done"  ->  "100%25done"   unsafe? false  round? true  legal? true
```
A name the predicate marks `unsafe=false` still has its `%` escaped to `%25`.
That is direct, runnable evidence that the implemented/specified transform is
**not** per-name gated. The finding even reproduces this row and calls it "the
*correct* (injective) behaviour," then asserts the decision "describes a
different function." It does not: the decision's escape scheme (line 88-91)
explicitly says "A literal `%` in the original is **first escaped to `%25`**
(makes the scheme total/reversible)." The repro is the decision, executed.

### 3. Reversibility REQUIRES always-escaping `%`; "reversible but non-injective" cannot exist here

The finding tries to wedge "reversible" apart from "injective." For a percent
scheme they are the same property. If literal `%` were left unescaped, `a%3Ab`
on disk would be ambiguous (literal, or the escape of `a:b`?) and `unescape`
could not be a function — so the scheme would not be reversible, which is the
whole reason Option B/D was chosen over lossy sanitise (Option C). A
hypothetical per-name implementation that writes `a%3Ab` verbatim FAILS the
decision's own stated acceptance criterion (`unescape(escape(x)) == x`,
consequences line 129): `unescape("a%3Ab")` would decode `%3A`->`:` and yield
`a:b != a%3Ab`. So the only function consistent with the decision's reversibility
requirement is the injective one in the repro. There is exactly one spec.

Concretely, under the actual transform there is no collision:
- `a:b`   -> (no `%`) -> `:`->`%3A` -> `a%3Ab`
- `a%3Ab` -> `%`->`%25` -> (no reserved chars) -> `a%253Ab`
Distinct inputs, distinct outputs.

### 4. Injectivity is already guaranteed by the existing round-trip test — recommendation #2 is redundant

A function with a left inverse is injective: `unescape(escape(x)) == x` for all
`x` implies `escape` is one-to-one (if `escape(x1)=escape(x2)` then
`x1=unescape(escape(x1))=unescape(escape(x2))=x2`). The decision already mandates
that round-trip assertion over the full Windows-hostile table (consequences line
129; filename-legality table lines 94-101, which include the escape-lookalikes
`100%done`). So the claim "no stated guarantee that the mapping is injective and
no test that the escaped namespace is collision-free" is false — the round-trip
guarantee IS the injectivity guarantee. Recommendation #2 asks for something the
existing acceptance criterion already proves.

### 5. The "silent clobber" path is explicitly closed by the decision's fallback — recommendation #3 is moot

Even granting a hypothetical residual escaped-name collision, the decision
(lines 110-111) already states: "if the escaped form ... collides with a
different real file, do **not** clobber — surface it as a flagged, unsynced
path." So the finding's headline outcome — "silently clobbers ... with no error
and no flag" — is precisely the outcome the binding spec prohibits. The namespace
gap the finding raises is also moot: injectivity (point 3) means distinct
canonical keys never map to one on-disk name, so there is no escaped-namespace
collision for the fold index to miss.

### 6. Recommendation #1 restates the status quo

Rec #1 ("always escape `%` to `%25` first ... only `%XX` triplets on disk are
escapes") is verbatim the decision's escape scheme (line 88-91). It does not
beat the status quo; it is the status quo.

## What is actually true (and why it is not medium/data-loss)

There is a defensible documentation nit: the phrase "only on the platform/volume
that rejects the name" is, read in isolation, mildly ambiguous and could be
tightened to "the transform runs on every component written to a Windows target"
to forestall a careless per-name reading. That is a wording polish, not a
correctness defect: the disambiguating parenthetical, the reversibility
requirement, the cited repro, and the round-trip acceptance test all force the
injective implementation, and the refuse+flag fallback forbids the clobber even
in the corner case. The "silent data loss (SR-7 violation) under the binding
spec" severity is unsupported by the evidence the finding itself cites.

## Verdict

REFUTED (high confidence). Core mechanism rests on a misreading of per-platform
gating as per-name gating, contradicted by the finding's own cited evidence
(repro line 77) and by the decision's reversibility requirement, round-trip
acceptance test, and refuse+flag fallback. Recommendations are already in the
spec, mathematically redundant, or moot.
