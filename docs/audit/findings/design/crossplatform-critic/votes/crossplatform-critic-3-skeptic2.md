# Skeptic #2 vote — crossplatform-critic-3

**VOTE: REFUTE** (confidence: high)

## The finding's central claim is a misreading of the binding decision

The finding asserts the decision is "predicate-gated" per-name — that
`EscapeForWindows` runs **only on components where `IsWindowsUnsafe` is true**,
so a Windows-legal-but-escape-lookalike name (`a%3Ab`) is "written verbatim" and
collides with the escaped form of `a:b` (`a%3Ab`). That premise is false. It rests
entirely on quoting one clause —

> "escape happens in the `ToOSPath` boundary conversion **only on the
> platform/volume that rejects the name**"

— while dropping the parenthetical that immediately disambiguates it
(`illegal-name-strategy.md:102-106`):

> "...**only on the platform/volume that rejects the name** (**always on Windows
> targets**; on macOS/Linux the original is already legal so no escape)."

The gating axis is the **platform**, not the per-name predicate. On a Windows
target the transform is applied to **every** component; on a Mac/Linux target it
is applied to **none**. "the name" in "rejects the name" refers to the class of
names Windows rejects (i.e. Windows is the rejecting platform), not "skip the
individual components the predicate currently marks safe." The finding's gloss of
"escape only the names the predicate calls safe" is its own invention.

## The escape scheme is explicitly total and injective — by the decision's own text

The very first step of the documented escape scheme
(`illegal-name-strategy.md:89-91`):

> "A literal `%` in the original is **first escaped to `%25`** (**makes the scheme
> total/reversible**)."

"Total" means *applied to all inputs*. A predicate-gated transform that skipped
"safe" names would never reach this step for a name like `a%3Ab`, and the step's
stated justification ("makes the scheme total/reversible") would be meaningless.
The decision wrote this step precisely so that the only `%XX` triplets on disk are
escapes — which is exactly the universal behaviour the finding's recommendation #1
asks for. **The recommendation restates what the decision already mandates.**

Worked counter-example to the alleged clobber (under the actual decision):
- `a:b`   (Windows target) → `%`-pass (none) → `:`→`%3A` → on-disk **`a%3Ab`**
- `a%3Ab` (Windows target) → `%`→`%25` first → on-disk **`a%253Ab`**

Distinct inputs → distinct on-disk names. **No collision, no clobber.** The
finding's data-loss scenario does not exist under the spec it claims to be quoting.

## The finding's own cited evidence refutes it

The finding calls the repro a *contradiction* of the decision, quoting:

> `"100%done" -> "100%25done"  unsafe? false  round? true  legal? true`

and arguing the decision would leave `100%done` "untouched." But this row is the
decision's "% first, total" step working **exactly as written**: a name the
predicate marks `unsafe=false` still has its `%` escaped. The repro
(`filename-legality.md:64-85`) and the decision describe the **same** function.
The finding manufactures a contradiction by first misreading the decision as
per-name-gated, then noticing the repro isn't per-name-gated. Remove the misread
and the "self-contradiction" evaporates. (`filename-legality.md:81-84` even spells
out: "Reversibility holds because `%` is always escaped to `%25` first.")

## Reversibility already *is* injectivity — recommendation #2 is redundant

The finding claims "no stated guarantee that the canonical→OS-name mapping is
injective." Mathematically false. The decision's Consequences
(`illegal-name-strategy.md:128-129`) require the round-trip assertion
`unescape(escape(x)) == x`. A function possessing a left inverse is injective by
definition: if `escape(x1) == escape(x2)` then
`x1 = unescape(escape(x1)) = unescape(escape(x2)) = x2`. The existing round-trip
table test therefore *already* guarantees injectivity. Recommendation #2 ("add an
injectivity acceptance test") adds, at most, a restatement of a property the
round-trip test already proves; it is not a gap.

## The namespace claim (rec #3) is moot, and already covered

Because the escape is total and injective, two distinct canonical keys can never
produce the same escaped on-disk name. So there is no escaping-induced collision
for the fold index to "miss." The only residual case — an escaped spelling
colliding with a *different real file already on disk* — is explicitly handled by
the chosen Option **D** fallback (`illegal-name-strategy.md:69-71, 109-111`):
"its escaped spelling collides with a different real file ... do **not** clobber —
surface it as a flagged, unsynced path." The "namespace gap" the finding wants
closed is already closed by the decision's refuse+flag fallback.

## Severity is overstated

Claimed: medium, "silent data loss (SR-7 violation) under the binding spec." The
data-loss path requires the per-name-gating misreading. Under the spec as written
the transform is total and injective, so the SR-7 violation does not occur. With
the premise removed, what remains is, at most, a documentation-clarity nit: the
clause "only on the platform/volume that rejects the name" could be reworded to
"on every component written to a Windows target" to remove the ambiguity the
finding exploited. That is a low-value wording tweak, not a medium correctness
defect, and it changes no behaviour and no test.

## Conclusion

The finding's three pillars all collapse: (1) the "predicate-gated" reading
contradicts the decision's explicit "always on Windows targets" + "% first, total"
text; (2) its own cited repro demonstrates the total behaviour it claims is absent;
(3) round-trip reversibility already entails injectivity, and the residual on-disk
collision is already a refuse+flag case. No counter-example survives. The
recommendations either restate existing requirements or are moot.

**VOTE: REFUTE**
