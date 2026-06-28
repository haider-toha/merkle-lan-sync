# Decision: Windows-unsafe names — reversible escape on the OS form, refuse+flag as fallback

- Area: crossplatform / pathnorm (confirms XP-3, the escape-vs-reject strategy)
- Status: **decided** (Phase 2 — crossplatform-researcher)
- Date: 2026-06-28
- Decider: crossplatform-researcher
- Confirms: `docs/audit/rules/crossplatform-rules.md` XP-3 ("decide & log the
  escape vs reject strategy"); cross-refs SR-7 (no data loss), SR-13, GR-12, and
  the MAX_PATH decision (`decisions/crossplatform/maxpath-longpath-handling.md`).

## Context

A Mac or Linux peer can legitimately create filenames that **Windows refuses**.
Authoritative list, verbatim from
[Microsoft, *Naming Files, Paths, and Namespaces*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)
(ms.date 2024-08-28, updated 2025-04-11, accessed 2026-06-28):

- **Reserved characters:** `< > : " / \ | ? *`; "Integer value zero, sometimes
  referred to as the ASCII *NUL* character"; "Characters whose integer
  representations are in the range from 1 through 31".
- **Reserved device names:** "CON, PRN, AUX, NUL, COM1 … COM9, COM¹, COM², COM³,
  LPT1 … LPT9, LPT¹, LPT², and LPT³. Also avoid these names followed immediately
  by an extension; for example, NUL.txt and NUL.tar.gz are both equivalent to
  NUL." Note: "Windows recognizes the 8-bit ISO/IEC 8859-1 superscript digits ¹,
  ², and ³ as digits and treats them as valid parts of COM# and LPT# device
  names … `echo test > COM¹` fails to create a file."
- **Trailing space/period:** "Do not end a file or directory name with a space or
  a period … However, it is acceptable to specify a period as the first character
  of a name."

If such a name arrives from a peer and we write it verbatim on Windows, the write
**fails** (file silently not synced — divergence) or, with the `\\?\` prefix, a
trailing-space/dot name is created that Explorer and most tools cannot open
(`Mkdir(\\?\c:\foo )` keeps the trailing space — see the long-path decision). A
lossy "strip the bad characters" sanitiser is worse: it can map two distinct names
(`a?b`, `a*b`) onto one (`a_b`) → collision and silent data loss, violating SR-7.

The colon is especially dangerous on NTFS: `name:stream` opens an **alternate
data stream**, so writing a Mac file literally named `a:b` does not create a file
`a:b` — it writes a stream of `a`. Escaping `:` is mandatory.

## Options (scored 1–5, 5 = best)

### Option A — refuse + flag only (never materialise an unsafe name)
- Correctness/no-loss: **3** — no clobber, but the file simply never lands on the
  Windows side; the two peers cannot converge on that path (asymmetric, persistent
  "needs attention").
- Testability: 5. Cross-platform: 3. Concurrency-safety: 5.
- Good as a *fallback*, too blunt as the *only* policy.

### Option B — reversible escape on the **on-disk OS form only** (PROPOSED)
- The **canonical tree key + wire form keep the ORIGINAL (NFC) name**. Only when
  materialising on a platform that rejects the name do we transform it to a safe,
  **reversible** on-disk form. Decoding the on-disk form reproduces the original
  byte-for-byte, so a round-trip Mac→Windows→Mac is lossless and both peers key the
  file identically (SR-13 intact).
- Correctness/no-loss: **5** — nothing is lost or merged; reversible.
- Testability: **5** — `escape(name)`/`unescape(s)` are pure functions; the
  Windows-hostile table round-trips under unit test on the Mac.
- Cross-platform: **5** — Windows gets a legal name; Mac/Linux keep the original.
- Concurrency-safety: **5** — pure functions.

### Option C — lossy sanitise (replace unsafe chars with `_`)
- Correctness/no-loss: **1** — non-reversible; distinct names collide; silent data
  loss. Rejected.

### Option D — escape primary, refuse+flag only the unrepresentable (PROPOSED, = B+A)
- Same as B, but when even the escaped form is impossible (e.g. it would exceed the
  path limit when long-path support is off, or it collides with a real existing
  escaped name), fall back to refuse+flag (A) rather than guess.
- This is B with an explicit, tested escape hatch. Best of both.

## Decision

Adopt **Option D: reversible escape (Option B) as the primary strategy, with
refuse+flag (Option A) as the fallback** for the residual unrepresentable cases.

**The detection predicate `IsWindowsUnsafe(component)` is computed on every OS**
(so a Mac can warn that a name will be unsafe on Windows, and the Windows side
escapes deterministically). A name component is unsafe if it:
(a) contains any of `< > : " / \ | ? *`; (b) contains a control char (U+0001–
U+001F) or NUL; (c) is a reserved device name — case-insensitively, **stem only**
(`NUL`, `NUL.txt`, `COM1`, `COM¹` …; "stem" = the part before the first `.`); or
(d) ends in a space or a period.

**Escape scheme (reversible):** percent-encoding restricted to the offending
positions.
- A literal `%` in the original is first escaped to `%25` (makes the scheme
  total/reversible).
- Each reserved/control character is replaced by `%` + 2 uppercase hex digits of
  its code point (`:` → `%3A`, `*` → `%2A`, `?` → `%3F`, `|` → `%7C`, `"` → `%22`,
  `<` → `%3C`, `>` → `%3E`, `\` → `%5C`; NUL/1–31 likewise).
- A **trailing** space or period is escaped (`name.` → `name%2E`, `name ` →
  `name%20`); leading periods are left alone (legal per Microsoft).
- A **reserved device stem** is disambiguated by escaping its first character so
  it no longer matches the reserved set yet still decodes back: `CON` → `%43ON`,
  `NUL.txt` → `%4EUL.txt`, `COM1` → `%43OM1`. (Escaping the first byte is enough
  because the match is on the exact stem; decode restores it.)
- `unescape` reverses `%XX` sequences; because `%` itself is always `%25`, decode
  is unambiguous.

**Where:** escape happens in the `ToOSPath` boundary conversion **only on the
platform/volume that rejects the name** (always on Windows targets; on macOS/Linux
the original is already legal so no escape). The canonical key never changes. This
keeps SR-13 ("same canonical key on both OSes") true while giving each OS a
writable name.

**Fallback (refuse+flag):** if the escaped form still cannot be written (exceeds
the path limit with long-path support unavailable, or its escaped spelling
collides with a different real file), do **not** clobber — surface it as a flagged,
unsynced path (same posture as the case-collision policy).

## Rationale

- Reversibility is what makes the no-data-loss contract (SR-7 spirit) literally
  true across the OS boundary: the original name is always recoverable.
- Percent-encoding is familiar, total, and trivially unit-testable; the escape
  characters it introduces (`%`, hex digits) are themselves Windows-legal.
- Computing `IsWindowsUnsafe` on every OS means the Mac (where we build and test)
  can exercise the whole table without a Windows box; only the *actual write* of an
  escaped name needs the Windows runner (Phase 6).

## Consequences

- Drives `internal/pathnorm/windows.go`: `IsWindowsUnsafe`, `EscapeForWindows`,
  `UnescapeFromWindows`, and the reserved-name/superscript tables.
- Feeds the **Windows-hostile test table** (XP-3 acceptance, SR-13): see
  `findings/crossplatform/filename-legality.md` for the concrete cases; round-trip
  asserts `unescape(escape(x)) == x` and that `escape(x)` is Windows-legal.
- The "escaped name collides with a real escaped name" edge ties into the
  canonical-key collision policy.
- Reserved-name behaviour, trailing dot/space, and the `\\?\` interaction **cannot
  be fully verified on the Mac** (plan/README) → Phase 6 CI `windows-latest` +
  `CROSS_PLATFORM_CHECKLIST.md`.
