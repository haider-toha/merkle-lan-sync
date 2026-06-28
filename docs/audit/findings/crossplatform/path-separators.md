# Cross-platform finding — Path separators (forward-slash relative; never store an OS separator)

- Slug: `path-separators` · confirms **XP-1**
- Phase: 2 (crossplatform-researcher, elevated track)
- Reads-first: `docs/audit/rules/crossplatform-rules.md` (XP-1), `go-rules.md`
  (GR-12), `sync-rules.md` (SR-13).
- Decision: none new — XP-1 is already a project **hard rule** (SR-13, GR-12, the
  autonomy contract). This finding supplies the confirming evidence and the
  round-trip obligation.
- Access date for all URLs: **2026-06-28**.
- Severity: **Medium** — the underlying risk (a stored `\` poisons the cross-OS
  hash → non-convergence) is high, but it is already pinned as a hard rule and is
  trivially enforced; residual risk is low.

## Claim

Every path in the tree, on the wire, and as a map key is a **forward-slash,
relative** path (relative to the sync root, the only absolute path). Convert to/from
the OS-native separator **only** at the filesystem call, with `filepath.FromSlash`
(write) / `filepath.ToSlash` (read). Never store `\`.

## Evidence

- **Windows uses `\` as the path separator and reserves both `\` and `/`** as
  component separators that cannot appear in a name: "Use a backslash (`\`) to
  separate the *components* of a *path* … You cannot use a backslash in the name for
  the actual file or directory because it is a reserved character", and the reserved
  character list includes both `/` and `\`
  ([Microsoft, *Naming Files, Paths, and Namespaces*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file),
  ms.date 2024-08-28, accessed 2026-06-28).
- **Go's separator is OS-specific**, confirmed by reproduction
  (`scratchpad/normprobe/main.go`, Go 1.26.4): on this Mac
  `filepath.Separator == '/'` and `os.PathSeparator == '/'`; on Windows both are
  `\`. So `filepath.Join`/`filepath`-built strings differ byte-for-byte across
  OSes — storing them would make the *same* file hash differently on Mac vs
  Windows, breaking SR-5/SR-13. Go provides `path` (always `/`) for canonical keys
  and `filepath` for the OS boundary (GR-12;
  [pkg.go.dev/path/filepath](https://pkg.go.dev/path/filepath), accessed
  2026-06-28).
- Because `/` and `\` are *both* reserved inside a Windows name, a forward-slash key
  is unambiguous: a `/` in a canonical key is always a separator, never part of a
  component.

## Interaction with the other cross-platform rules

The canonical key is forward-slash **and** NFC (`unicode-normalization.md`) **and**
case-preserved (`case-sensitivity.md`). Normalisation is applied **per component**
(split on `/`, normalise each, re-join) so the separator can never be touched by
NFC folding. The OS form (`ToOSPath`) joins the relative key against the absolute
root and applies `FromSlash` + Windows escaping (`filename-legality.md`) + Go's
long-path fixup (`maxpath-longpath-handling.md`) — in that order.

## Test obligations (SR-13 acceptance)

- Round-trip a Windows-style input set Mac→wire→Windows→wire→Mac and assert
  identical canonical keys and identical subtree hashes (plan WS-1 acceptance:
  "pathnorm round-trips a Windows-hostile name set without loss").
- Assert no stored key or wire payload ever contains `\`; assert `ToSlash` is
  idempotent on already-canonical keys.
- Assert UNC / `\\?\` / drive-letter prefixes are stripped during relativisation so
  the key is clean (`maxpath-longpath-handling.md`).

## Cannot be verified on the Mac → Phase 6

The Mac side (separator `/`) is confirmed above; the Windows side (separator `\`,
prefix stripping, deep-tree round-trip) is closed by the CI `windows-latest` job +
`docs/audit/CROSS_PLATFORM_CHECKLIST.md`.
