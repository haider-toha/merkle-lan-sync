# Skeptic #1 vote — crossplatform-critic-1: REFUTED

- Finding: `docs/audit/findings/design/crossplatform-critic/crossplatform-critic-1.md`
- Vote: **REFUTED** (refuted=true)
- Confidence: **high**
- Date: 2026-06-28 · Reviewer: skeptic #1 of 3

## Verdict

The finding's central claim — "three mutually inconsistent pipelines" and "the one
stated order corrupts every Windows path" — does not survive contact with the cited
evidence. Both corruption scenarios depend on a misreading of how escaping is
specified; under the authoritative decision the corruption cannot occur. The severity
("high / data corruption / breaks the round-trip") is overstated; the residual is at
most a minor wording cleanup plus a documentation-consolidation nicety.

## Why the "corruption" charge collapses

The whole impact rests on reading the word "Windows escaping" in
`path-separators.md` §"Interaction" as *whole-string escaping of the joined absolute
path* (`C:\root\dir\name` → `C%3A%5Croot...`). That reading is contradicted by the
document that sentence explicitly cites as the authority for the escaping
(`filename-legality.md` / the illegal-name **decision**):

- `docs/audit/decisions/crossplatform/illegal-name-strategy.md:79-106` defines the
  predicate as **`IsWindowsUnsafe(component)`** and the transforms as
  `EscapeForWindows`/`UnescapeFromWindows` operating **on a component**, running "in
  the `ToOSPath` boundary conversion". A *component* is a single path element with no
  separators and no drive colon, so per-component escaping can never touch `C:` or a
  separator `\`. The `C%3A%5C...` garbage path is impossible under the actual spec.
- `path-separators.md:46-51` itself already establishes the per-component pattern
  ("Normalisation is applied **per component** (split on `/`, normalise each,
  re-join) so the separator can never be touched"). The escaping cross-ref inherits
  that framing; nothing states "escape the joined absolute string."

The finding's second scenario — a content backslash `a\b` being conflated with
separators after `FromSlash` — collapses for the same reason: `\` is explicitly in
the unsafe set, clause (a), at `illegal-name-strategy.md:81-82,92` (`\` → `%5C`). A
component `a\b` is flagged unsafe and escaped to `a%5Cb` **per component, before**
`FromSlash`. The finding's premise that "the reserved-char escaping that was supposed
to catch `\` can no longer run, because it can only see the post-`FromSlash` string"
is true *only* under the finding's own incorrect ordering assumption (escape the
whole string after FromSlash). The actual order is component-escape → FromSlash.

## Why "three mutually inconsistent pipelines" is overstated

The three artifacts operate at different altitudes and **explicitly cross-reference
each other**; read together they describe one consistent pipeline, not three rival
ones:

- `maxpath-longpath-handling.md` is scoped to the `\\?\`/long-path question, not to
  the full `ToOSPath` spec. Its formula `filepath.Join(absRoot, FromSlash(rel))`
  takes `rel` (the already-escaped relative key) and is about making the path
  absolute so Go's `fixLongPath` engages. The doc does **not** "omit escaping in
  ignorance" — it states it "**preserves our trailing-dot/space escaping** (Option A
  would defeat it)" (`maxpath-longpath-handling.md:76-79`) and cross-refs the
  illegal-name decision at lines 8-9. Reading it as "omits escaping entirely and must
  be amended" (finding recommendation #3) is uncharitable to its stated scope.
- `illegal-name-strategy.md` is the **decision** that authoritatively fixes escaping
  as per-component in `ToOSPath`.
- `path-separators.md` is a **Medium-severity confirming finding** (self-rated, line
  11-13), not the authoritative converter spec; its one-line ordering summary defers
  the escape semantics to `filename-legality.md`.

Combine the decision (per-component escape of unsafe components inside `ToOSPath`)
with the maxpath construction (Join absolute → fixLongPath) — both of which
cross-link — and you get exactly the "only correct pipeline" the finding recommends
in recommendation #1. So that pipeline **is** specified by the artifacts, just
distributed across the decision + the maxpath decision.

## Internal contradiction in the finding

Evidence point 3 of the finding itself states the per-component escaping "runs in the
`ToOSPath` boundary conversion" and operates on a component — i.e. it documents the
correct pipeline existing. Yet the conclusion (lines 86-92) asserts "the only correct
pipeline ... is **not stated by any of the three documents**." The finding refutes
its own headline.

## What is left after refutation (and why it is not High)

There is a kernel of legitimate doc-hygiene merit: (a) the summary sentence in
`path-separators.md:50-51` lists "FromSlash + Windows escaping ... in that order,"
which is loosely worded and would be clearer if it said escaping is per-component and
precedes `FromSlash`; (b) consolidating the full ordered per-component pipeline into
one authoritative spot (SKILL §6) would help an implementer who reads only one file.
SKILL §6 already states per-component-style unsafe-name escaping on the on-disk form
(`.claude/skills/merkle-sync/SKILL.md:262-268`).

That is **documentation tidiness**, Low-to-Medium, not a High correctness defect.
The substantive design is correct and consistent; no "every Windows path corrupted,"
no ADS write under the spec, no round-trip data loss. Recommendation #2 (a `ToOSPath`
unit test) is reasonable but is already implied by the SR-13 round-trip obligation
(`path-separators.md:55-61`, SKILL §6 test obligation line 278). Recommendation #3
rests on the scope misreading.

## Bottom line

The technical core of the finding is demonstrably wrong (it depends on whole-string
escaping that the authoritative decision does not specify), it contradicts itself, and
its severity is inflated. Default-to-refute is reinforced, not overridden.

VOTE: REFUTED
