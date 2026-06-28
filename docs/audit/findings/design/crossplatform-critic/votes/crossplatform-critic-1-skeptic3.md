# Skeptic #3 vote — crossplatform-critic-1 (REFUTE assignment)

- Finding: `docs/audit/findings/design/crossplatform-critic/crossplatform-critic-1.md`
- Verdict: **REFUTED** (severity overstated; central claim manufactured from an
  uncharitable reading)
- Confidence: medium-high
- Date: 2026-06-28

## What I checked

Read the finding and all three cited artifacts plus the authoritative path-rules:
- `docs/audit/findings/crossplatform/path-separators.md` (lines 44–52)
- `docs/audit/decisions/crossplatform/maxpath-longpath-handling.md` (Decision steps 1–3)
- `docs/audit/decisions/crossplatform/illegal-name-strategy.md` (Decision, escape scheme, "Where")
- `.claude/skills/merkle-sync/SKILL.md` §6 (lines 247–283) — the SKILL the finding
  itself names as the single authoritative path-rules location.
- `find ... -name '*.go'`: only `cmd/msync/main.go` exists; **`internal/pathnorm/`
  does not exist**. This is a pure design-stage finding — there is no converter to
  "corrupt" yet.

## Why the finding is refuted

### 1. The "three mutually inconsistent pipelines" framing is false — these are three orthogonal, cross-referencing modules, not three competing complete specs.

Each artifact addresses one axis and explicitly defers the others by citation:
- **path-separators.md** is about XP-1 ("never store `\`"), severity *Medium*. It
  cross-refs `filename-legality.md` for escaping and `maxpath-longpath-handling.md`
  for long-paths.
- **maxpath-longpath-handling.md** is about XP-3's MAX_PATH sub-item only. Its
  formula `filepath.Join(absRoot, FromSlash(rel))` is presented to show the path
  must be *absolute* so `os.fixLongPath` engages — and it cross-refs the illegal-name
  decision (lines 8–9). It is not, and does not claim to be, the full `ToOSPath` spec.
- **illegal-name-strategy.md** is the *authoritative* escaping spec: escaping is
  **per component**, predicate `IsWindowsUnsafe(component)`, applied **only on the
  rejecting platform**, "in the `ToOSPath` boundary conversion."

Treating a deliberately-scoped MAX_PATH decision as "omitting escaping" (Evidence #2)
is like faulting the TLS decision for not mentioning Merkle hashing. Modular specs
that cross-reference each other are normal, not "mutually inconsistent."

### 2. The "corrupts every Windows path" claim rests on an uncharitable misreading of one summary sentence.

The finding reads path-separators.md line 51 ("…applies `FromSlash` + Windows
escaping … in that order") as "escape the entire joined absolute string `C:\root\…`,"
then derives `C%3A%5Croot…`. But the **authoritative** escaping spec
(illegal-name-strategy.md) says escaping is per-component via
`IsWindowsUnsafe(component)`, and escapes the **component**, never `absRoot`. Under
the actual spec:
- the drive colon lives in `absRoot`, which is never a component of the relative key,
  so it is never escaped;
- separators are the `/` delimiters between components, not characters *inside* a
  component, so they are never escaped.

The garbage-path scenario simply does not exist in the design taken as a whole.

### 3. The content-backslash conflation is already prevented by the explicit escape table.

illegal-name-strategy.md lists `\` → `%5C` in its escape table and includes `\` in
`IsWindowsUnsafe`. SKILL §6.2 repeats `\` in the unsafe set. Per-component escaping
runs on the `/`-delimited canonical key, so a Mac component `a\b` is escaped to
`a%5Cb` **before** any `FromSlash` — exactly the protection the finding says is
missing. The conflation only appears if you assume the maxpath decision's
escaping-free illustrative formula is the complete pipeline, which it explicitly is
not.

### 4. The finding's own "only correct pipeline" is already the union of the two authoritative decisions — so "not stated by any of the three documents" is wrong.

Recommended recipe vs. what is already written:
- Steps 1–2 (split on `/`, per-component `EscapeForWindows` on rejecting target) =
  illegal-name-strategy.md verbatim ("per component", "only on the platform/volume
  that rejects the name").
- Steps 3–4 (`Join(absRoot, FromSlash(...))`, rely on `fixLongPath`, never
  hand-prepend `\\?\`) = maxpath-longpath-handling.md Decision steps 1–2 verbatim.

The recommendation is a *consolidation/wording* request, not the discovery of a
missing or wrong design.

### 5. Severity (high) is unjustified; the test gap is largely already covered.

No code exists; the implementer's contract is the SKILL + the cross-referenced
decisions together, which already yield the correct converter. The recommended
unit test overlaps existing obligations: illegal-name-strategy.md Consequences
already mandates "round-trip asserts `unescape(escape(x)) == x` and that `escape(x)`
is Windows-legal," and path-separators.md already mandates the
Mac→wire→Windows→wire→Mac round-trip. "Ships a broken converter" assumes an
implementer reads one scoped document in isolation and reads it uncharitably.

## Honest residual (does not change the vote)

There is one legitimate, narrow nit: path-separators.md line 51 lists "`FromSlash` +
escaping … in that order," which puts `FromSlash` *before* escaping — the reverse of
the correct per-component order. That is a one-line wording imprecision in a
*Medium* summary finding that explicitly defers escaping to the authoritative
decision. It warrants a trivial copy-edit (and optionally pinning the consolidated
order in SKILL §6), not a *high*-severity "no coherent spec / corrupts every path /
data corruption" finding. The kernel of truth is real but the inflation is not.

## Vote

REFUTED — the central claims (three mutually inconsistent pipelines; corrupts every
Windows path; correct pipeline stated nowhere) are manufactured by reading scoped,
cross-referencing modules as competing complete specs and by ignoring the explicit
per-component escaping decision. Severity is overstated; the substantive recommendation
already exists distributed across two authoritative decisions. At most a one-line
doc-wording fix is warranted.
