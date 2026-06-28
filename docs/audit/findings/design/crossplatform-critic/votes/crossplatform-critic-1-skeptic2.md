# Skeptic #2 vote — crossplatform-critic-1 (REFUTE)

- Finding: "ToOSPath conversion pipeline is under-specified and contradictory; the one
  stated order corrupts Windows paths (escapes the drive-letter colon, conflates
  content backslashes with separators), breaking the Mac->Windows->Mac round-trip."
- Vote: **REFUTED**
- Confidence: medium-high
- Reviewed: the finding plus all three cited artifacts
  (`docs/audit/findings/crossplatform/path-separators.md`,
  `docs/audit/decisions/crossplatform/maxpath-longpath-handling.md`,
  `docs/audit/decisions/crossplatform/illegal-name-strategy.md`).

## Why the finding does not hold

### 1. The "three mutually inconsistent pipelines" are three differently-scoped, mutually cross-referencing documents, not three competing full specs.

The finding treats each doc as if it independently claimed to be the complete,
authoritative definition of `ToOSPath`. They do not:

- **`illegal-name-strategy.md`** is the authoritative escaping decision. It is
  unambiguous and **per-component**: the predicate is `IsWindowsUnsafe(component)`
  (line 80), the functions are `EscapeForWindows`/`UnescapeFromWindows` on a
  component, the unsafe set explicitly includes both `:` and `\` (lines 81-82), the
  table maps `\ -> %5C` and `: -> %3A` (line 92), the transform is reversible
  (`unescape(escape(x)) == x`, line 128), the canonical key never changes
  (lines 105-106), and it is applied "only on the platform/volume that rejects the
  name" (line 102). This is exactly the per-component pipeline the finding says is
  "not stated by any of the three documents." It *is* stated — in the decision whose
  entire job is to specify escaping.

- **`maxpath-longpath-handling.md`** is scoped to the MAX_PATH / long-path axis
  (its title, and Option B). Its formula `filepath.Join(absRoot, FromSlash(rel))`
  is there to show how to keep the path *absolute* so Go's `fixLongPath` engages —
  it is not claiming to be the full pipeline. It explicitly cross-refs the
  illegal-name decision (line 9) and says escaping is preserved by this construction
  (Option B: "This also preserves our trailing-dot/space escaping", line 76). Reading
  "no escaping step at all" into a decision that is about long-path handling and that
  cross-references the escaping decision is uncharitable.

- **`path-separators.md`** is a Phase-2 *confirming finding* that says outright
  "Decision: none new" (line 8). Its single summary sentence (line 51) lists the
  three operations and delegates each to its own doc: "FromSlash + Windows escaping
  (`filename-legality.md`) + Go's long-path fixup (`maxpath...`)". "Windows
  escaping (filename-legality.md)" is a pointer to the per-component scheme, not an
  independent instruction to escape the whole joined absolute string. The finding's
  "C%3A%5Croot..." corruption follows only from the naive literal reading
  `escape(FromSlash(Join(root,rel)))` — which contradicts the very doc that sentence
  cross-references.

So the documents are complementary and cross-referenced, not contradictory. The
escaping decision pins the per-component, reversible, `:`- and `\`-handling pipeline;
the other two address separators and long paths and defer escaping to it.

### 2. The concrete corruption scenarios are already precluded by the pinned design.

- **Drive-colon escape**: never happens, because escaping operates on components of
  the *relative* key, never on `absRoot`. The colon in `C:` lives in the root, which
  is joined *after* per-component escaping. The recommended fix's "never apply
  escaping to absRoot" is already implied by per-component escaping of the relative
  key.

- **Content `\` vs separator conflation**: precluded. A Mac file `a\b` is a single
  forward-slash key component; `IsWindowsUnsafe("a\b")` is true (contains `\`, line
  81), so it is escaped to `a%5Cb` *before* `FromSlash`, then joined. It materialises
  as `a%5Cb` and reverses to `a\b`. The conflation the finding describes only arises
  if you apply the maxpath formula *without* the escaping step — i.e., only if you
  ignore the decision that explicitly governs escaping.

- **`a:b` ADS write**: precluded for the same reason — `:` is in the unsafe set and
  escaped per-component to `a%3Ab`.

### 3. The "only correct pipeline" the finding proposes is materially the existing design.

The recommended 4-step pipeline (split on `/`, per-component `EscapeForWindows`,
`Join(absRoot, FromSlash(join))`, rely on `os.fixLongPath`) is a restatement of
illegal-name-strategy.md (per-component reversible escape in `ToOSPath`) +
maxpath-longpath-handling.md (absolute join, never hand-prepend `\\?\`). The
recommended change therefore does not beat the status quo on substance; it mostly
re-documents it in one place.

## What is actually true (and why it is low, not high)

There is a genuine but minor **documentation-clarity** nit: the one summary sentence
in the *confirming finding* `path-separators.md` (line 51) lists "FromSlash + Windows
escaping ... in that order" without spelling out that escaping is per-component and
precedes `FromSlash`. A careless reader could misorder it. Consolidating the ordering
into the SKILL §6 path-rules and adding the proposed `ToOSPath` unit test is a
reasonable, cheap cleanup.

But that is a low-severity wording/consolidation issue, not the finding's claimed
**high-severity data corruption / guaranteed round-trip failure**, because:

- No implementation exists yet; this is design phase. A competent implementer reads
  the *decision* that governs escaping (illegal-name-strategy.md), which is correct.
- The authoritative escaping decision already pins the correct per-component,
  reversible, `:`/`\`-handling transform, so the round-trip is in fact specified.
- The severity rests on the worst-case literal misreading of a delegating summary
  sentence in a non-authoritative confirming finding — overstated.

## Verdict

Severity overstated; core technical claim (round-trip corruption is unspecified /
unprevented by any pinned recipe) is unsupported — the pinned recipe exists in
illegal-name-strategy.md and is exactly the "only correct" per-component pipeline.
The residual real issue is a low-severity doc-consolidation nit. REFUTED.

VOTE: REFUTED
