# Cross-platform finding — Filename legality (Windows-illegal chars, reserved names, trailing dot/space, MAX_PATH)

- Slug: `filename-legality` · confirms **XP-3** (and its MAX_PATH sub-item)
- Phase: 2 (crossplatform-researcher, elevated track)
- Reads-first: `docs/audit/rules/crossplatform-rules.md` (XP-1..XP-6), the rest of
  `docs/audit/rules/`, `findings/synthesis/problem-space-map.md`.
- Decisions logged before this finding (autonomy contract):
  `docs/audit/decisions/crossplatform/illegal-name-strategy.md`,
  `docs/audit/decisions/crossplatform/maxpath-longpath-handling.md`.
- Access date for all URLs: **2026-06-28**.
- Severity: **High** — a Mac/Linux peer routinely produces names Windows refuses;
  writing them verbatim fails the sync (divergence) and a lossy "sanitise"
  loses/merges data (violates SR-7). The reversible-escape strategy is the fix.

## Claim

A filename is **Windows-unsafe** if it (a) contains a reserved character
`< > : " / \ | ? *`, (b) contains a control character (NUL or U+0001–U+001F),
(c) is a reserved device name (stem-only, case-insensitive), or (d) ends in a
space or a period. Such a name arriving from a peer must be transformed to a
**reversible**, Windows-legal on-disk form (the canonical tree key keeps the
original), with **refuse + flag** as the fallback for the unrepresentable residue.
Never write verbatim (fails) and never lossy-sanitise (collides → data loss).

## Evidence (primary, verbatim)

All from [Microsoft, *Naming Files, Paths, and Namespaces*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)
(ms.date 2024-08-28, page `updated_at` 2025-04-11, accessed 2026-06-28):

- **Reserved characters:** `< > : " / \ | ? *`; "Integer value zero, sometimes
  referred to as the ASCII *NUL* character"; "Characters whose integer
  representations are in the range from 1 through 31, except for alternate data
  streams".
- **Alternate-data-stream trap (NTFS):** the `:` is not just illegal cosmetically —
  `name:stream` addresses an NTFS ADS, so a literal write of a Mac file `a:b`
  writes a *stream* of `a`, not a file `a:b`. (Microsoft documents the `:` as
  reserved and points to *File Streams*.) Escaping `:` is mandatory.
- **Reserved device names:** "CON, PRN, AUX, NUL, COM1 … COM9, COM¹, COM², COM³,
  LPT1 … LPT9, LPT¹, LPT², and LPT³. Also avoid these names followed immediately by
  an extension; for example, NUL.txt and NUL.tar.gz are both equivalent to NUL."
  Superscripts: "Windows recognizes the 8-bit ISO/IEC 8859-1 superscript digits ¹,
  ², and ³ as digits and treats them as valid parts of COM# and LPT# device names …
  `echo test > COM¹` fails to create a file."
- **Trailing space/period:** "Do not end a file or directory name with a space or a
  period … However, it is acceptable to specify a period as the first character of
  a name. For example, `.temp`." (So leading dots are fine; trailing dot/space is
  not.)
- **Case:** "Do not assume case sensitivity … consider the names OSCAR, Oscar, and
  oscar to be the same" (handled by XP-4, see `case-sensitivity.md`).
- **MAX_PATH:** "In editions of Windows before Windows 10 version 1607, the maximum
  length for a path is **MAX_PATH**, which is defined as 260 characters. In later
  versions … changing a registry key or using the Group Policy tool is required to
  remove the limit." The `\\?\` prefix "disable[s] all string parsing … you can
  exceed the MAX_PATH limits"; "Unicode APIs should be used"; "Many but not all
  file I/O APIs support `\\?\`."

## Evidence (runnable reproduction — the escape strategy actually works)

`scratchpad/escapeproto/main.go` (Go 1.26.4, run on this Mac) implements the XP-3
reversible percent-escape and pushes the full Windows-hostile table through it. The
predicate, escaper and decoder produce **lossless round-trips and Windows-legal
output for every case** (`ALL round-trip && escaped-is-legal: true`). Selected rows:

```
ORIGINAL               ESCAPED (on-disk Win)   unsafe?  round?  legal?
"a:b"                  "a%3Ab"                 true     true    true
"what?.txt"            "what%3F.txt"           true     true    true
"back\\slash"          "back%5Cslash"          true     true    true
"ctrl\x01char"         "ctrl%01char"           true     true    true
"CON"                  "%43ON"                 true     true    true
"con"                  "%63on"                 true     true    true
"NUL.tar.gz"           "%4EUL.tar.gz"          true     true    true
"COM1.txt"             "%43OM1.txt"            true     true    true
"COM¹"                 "%43OM¹"                true     true    true
"trailingdot."         "trailingdot%2E"        true     true    true
"trailingspace "       "trailingspace%20"      true     true    true
"100%done"             "100%25done"            false    true    true
"résumé.txt"           "résumé.txt"            false    true    true
```

Reversibility holds because `%` is always escaped to `%25` first (so every `%` in
the output begins a 2-hex escape), reserved-stem disambiguation escapes only the
first character (`CON`→`%43ON` decodes back to `CON`), and trailing dot/space are
escaped so the on-disk name no longer ends in one. Legal names (`résumé.txt`,
`.hiddenleadingdot`, `normal.txt`) pass through untouched.

## The Windows-hostile test table (XP-3 / SR-13 acceptance)

The implementation in `internal/pathnorm/windows.go` (WS-1) must round-trip at
least this set under unit test (`unescape(escape(x)) == x` **and** `escape(x)` is
Windows-legal), and the full pathnorm round-trip Mac→wire→Windows→wire→Mac must
preserve the canonical key (SR-13):

| class | examples |
|---|---|
| reserved chars | `a:b`, `a*b`, `q?`, `a\|b`, `a"b`, `a<b>c`, `back\slash` |
| control chars | `x\x01y`, `tab\ty`, NUL-bearing |
| device stems | `CON`, `con`, `Con.txt`, `NUL`, `NUL.tar.gz`, `COM1`, `COM1.txt`, `LPT9`, `COM¹`, `AUX`, `PRN.log` |
| trailing dot/space | `name.`, `name `, `dots...`, `mix. ` |
| must NOT change | `.hiddenleadingdot`, `normal.txt`, `100%done`, `résumé.txt` (NFC) |
| long path | a component chain whose OS path exceeds 260 chars |

## Decision applied

- **Strategy:** reversible escape on the on-disk OS form, refuse+flag fallback —
  `decisions/crossplatform/illegal-name-strategy.md` (Option D). The canonical key
  always carries the original NFC name; only the platform that rejects the name
  sees the escaped form, keeping SR-13 true.
- **MAX_PATH / long paths:** rely on Go's `os.fixLongPath` (build absolute OS
  paths; never hand-prepend `\\?\`, which would re-enable trailing-space/dot
  creation and defeat the escaping) —
  `decisions/crossplatform/maxpath-longpath-handling.md` (Option B). Go adds the
  `\\?\` extended prefix itself only when length requires it
  ([go.dev/src/os/path_windows.go](https://go.dev/src/os/path_windows.go);
  [Klaus Post, *Long Windows paths in Go*](https://blog.klauspost.com/long-windows-paths-unc-paths-in-go/),
  accessed 2026-06-28).

## Cannot be verified on the Mac → Phase 6

Reserved-name rejection, trailing dot/space behaviour, ADS via `:`, the real >260
write, and long-path-enabled vs not are Windows-only (plan/README pins these as
"needs a real Windows target"). The escape/predicate logic is fully Mac-unit-
testable (shown above); the *actual on-disk write of an escaped name* is closed by
the Phase 6 CI `windows-latest` job + `docs/audit/CROSS_PLATFORM_CHECKLIST.md`.
