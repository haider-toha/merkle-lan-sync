---
id: crossplatform-critic-1
title: "ToOSPath conversion pipeline is under-specified and contradictory; the one stated order corrupts Windows paths (drive colon + content-vs-separator backslash conflation), breaking the Mac->Windows->Mac round-trip"
severity: high
status: rejected
phase: 3
critic: crossplatform-critic
focus: round-trip + never-store-OS-separators + illegal-char escaping
created: 2026-06-28
---

# ToOSPath has no single coherent order; the stated order corrupts paths

- Reads-first honoured: `docs/audit/rules/crossplatform-rules.md` (XP-1/XP-3),
  `docs/audit/rules/sync-rules.md` (SR-13), `.claude/skills/merkle-sync/SKILL.md` §6,
  and the three cross-platform decisions/findings cited below.
- Cross-refs: SR-13 (no OS separators in tree/wire), XP-1, the illegal-name and
  MAX_PATH decisions. Distinct from AP-20 (`path-traversal-received-path`), which is
  about `..`/absolute escape on apply; this is about the *boundary conversion order*
  of an already-canonical relative key.

## Claim

There is **no single, coherent specification** of how `pathnorm.ToOSPath` turns a
canonical forward-slash relative key into an on-disk Windows path. Three design
artifacts describe **three mutually inconsistent** pipelines, and the only one that
is stated as an explicit ordering would **corrupt every Windows path** (it escapes
the drive-letter colon and cannot distinguish a backslash that is *content* in a
filename component from a backslash that is a *separator*). Because materialising a
received file on the far OS goes through this exact conversion, the
`Mac(NFD) -> Windows(NFC) -> Mac` round-trip the design promises (SR-13) is not
actually guaranteed by any pinned recipe.

## Evidence

The three artifacts disagree on what `ToOSPath` does and in what order:

1. **`docs/audit/findings/crossplatform/path-separators.md`** (§"Interaction with
   the other cross-platform rules") states a concrete order on the *joined absolute
   path*:
   > "The OS form (`ToOSPath`) joins the relative key against the absolute root and
   > applies `FromSlash` + Windows escaping (`filename-legality.md`) + Go's
   > long-path fixup (`maxpath-longpath-handling.md`) — **in that order**."
   So: Join(root, rel) -> `FromSlash` (whole string) -> escape (whole string).

2. **`docs/audit/decisions/crossplatform/maxpath-longpath-handling.md`** (Decision
   step 2) gives a concrete formula with **no escaping step at all** and `FromSlash`
   applied to the whole relative path:
   > "Construct OS paths as absolute (`filepath.Join(absRoot, FromSlash(rel))`) at
   > the filesystem boundary, so Go's `fixLongPath` engages..."

3. **`docs/audit/decisions/crossplatform/illegal-name-strategy.md`** says escaping
   is **per path component** and runs "in the `ToOSPath` boundary conversion": the
   predicate is `IsWindowsUnsafe(component)` ("computed on every OS"), and
   `EscapeForWindows`/`UnescapeFromWindows` operate on a component.

These cannot all be right. Working them through:

- **The path-separators order (escape the joined, FromSlash'd absolute path) is
  unsafe.** After `Join` + `FromSlash`, the string is e.g.
  `C:\root\dir\name`. Windows escaping uses the reserved set
  `< > : " / \ | ? *` (Microsoft, *Naming Files, Paths, and Namespaces*,
  ms.date 2024-08-28, accessed 2026-06-28,
  https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file). Applied to
  that whole string it would escape the **drive-letter colon** (`C:` -> `C%3A`) and
  **every path separator** (`\` is reserved -> `%5C`), producing
  `C%3A%5Croot%5Cdir%5Cname` — not a path at all.

- **The maxpath formula (no escaping, `FromSlash` on the whole `rel`) is also
  unsafe**, for two reasons:
  (a) an unsafe *component* (e.g. a Mac/Linux file literally named `a:b`, which is
  legal on POSIX) reaches the filesystem unescaped — on NTFS `name:stream` opens an
  **alternate data stream**, so `a:b` silently writes a *stream* of `a`, not a file
  `a:b` (Microsoft naming page, "File Streams", accessed 2026-06-28; corroborated in
  `docs/audit/findings/crossplatform/filename-legality.md`);
  (b) a backslash that is **content** in a component is conflated with separators.
  `\` is a legal filename byte on macOS/Linux, so a Mac file named `a\b` canonicalises
  (via `filepath.ToSlash`, a no-op on POSIX where the separator is `/`) to the
  single-component key `a\b`. `filepath.FromSlash("dir/a\\b")` yields
  `dir\a\b` on Windows — now the content backslash is indistinguishable from the
  separator backslashes, so the component `a\b` is silently split into a directory
  `a` and a file `b`. The reserved-char escaping that was supposed to catch `\`
  (XP-3) can no longer run, because it can only see the post-`FromSlash` string where
  separators and content are the same byte.

The **only** correct pipeline — escape *each component of the forward-slash relative
key* (so `:` and content-`\` are neutralised while the key still uses `/` as the
unambiguous separator), *then* `FromSlash` + `Join(absRoot, ...)`, *then* let Go's
`fixLongPath` add `\\?\` if length requires — is **not stated by any of the three
documents**. SR-13's own test obligation ("assert no stored key or wire payload ever
contains `\`") does not catch this, because the corruption happens *after* the key,
inside `ToOSPath`, at materialisation time.

## Impact

- **Round-trip failure / data corruption on the Windows leg.** Materialising any file
  whose path contains a drive colon (always, on Windows) or whose component contains a
  content backslash or a reserved char goes through `ToOSPath`. Under the
  path-separators order it produces a garbage path; under the maxpath formula it
  produces an ADS write or a wrongly-split directory. Either way the
  `Mac -> Windows -> Mac` round-trip does not preserve the file, violating SR-13 and,
  for the `\`/`:` cases, silently losing or misplacing data (SR-7 spirit).
- **Hidden until a real Windows run.** All three documents defer the actual Windows
  write to Phase 6, so the contradiction will not surface in Mac unit tests; an
  implementer picking any one document in good faith ships a broken converter.

## Recommended-change

1. **Pin one canonical `ToOSPath` pipeline, ordered and per-component**, in a single
   authoritative place (the SKILL §6 path-rules and one decision), superseding the
   three divergent descriptions:
   1. split the canonical relative key on `/`;
   2. for each component, on a rejecting target apply `EscapeForWindows`
      (per-component, so content `\` and `:` are escaped while real separators are
      untouched);
   3. `Join(absRoot, filepath.FromSlash(strings.Join(escapedComponents, "/")))`;
   4. rely on Go's `os.fixLongPath` (never hand-prepend `\\?\`).
   State explicitly that escaping is **never** applied to `absRoot` (so the drive
   colon and root separators are preserved) and **always** before `FromSlash`.
2. Add a **`ToOSPath` unit test** (Mac-runnable) asserting: the drive/root prefix is
   untouched; a component containing a literal `\` round-trips
   (`UnescapeFromWindows` of the last path element == `a\b`); a `:`-bearing component
   never produces an OS path with an un-escaped `:`. This closes the gap that SR-13's
   key-level test leaves open.
3. Reconcile the maxpath decision's formula `filepath.Join(absRoot, FromSlash(rel))`
   with the escaping step — as written it omits escaping entirely and must be amended.
