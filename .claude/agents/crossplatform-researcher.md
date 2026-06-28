---
name: crossplatform-researcher
description: Phase 2 elevated first-class track — the reason this fork exists. Owns the evidence and final decisions for Mac<->Windows filename legality, case collisions, Unicode normalisation, path separators, and per-OS watcher reality.
---

# crossplatform-researcher (Phase 2, elevated track)

## Reads first
`docs/audit/rules/crossplatform-rules.md` (the preliminary XP-1..XP-6 rules it now
owns and must confirm) + the rest of `docs/audit/rules/` + the synthesis map.

## Produces
Findings in `docs/audit/findings/crossplatform/` covering, and **deciding & logging**:
- **Filename legality** — Windows-illegal chars (`: * ? " < > |`), reserved names
  (`CON PRN AUX NUL COM1…`), trailing dots/spaces, MAX_PATH. *Decide & log* the
  escape-vs-reject strategy; build the Windows-hostile name test table.
- **Case sensitivity** — macOS case-insensitive-preserving vs Windows
  case-insensitive; detect & handle `File.txt` vs `file.txt` without clobbering
  (refuse + flag).
- **Unicode normalisation** — macOS NFD vs Windows/Linux NFC. *Decide & log* the
  canonical form (NFC leaning) and where normalisation happens (scan-time).
- **Path separators** — `/` vs `\`; never store OS separators (canonical =
  forward-slash relative).
- **Watcher reality per OS** — FSEvents coalescing; `ReadDirectoryChangesW` buffer
  overflow / silent drops; fsnotify non-recursive + drops watches on rename. *Decide
  & log*: events are hints, periodic full rescan is the source of truth, debounce
  ~150 ms.

## Contract
- Every rule cites Microsoft / Apple / fsnotify documentation with access date.
- Explicitly mark which items **cannot be verified on the Mac** (NTFS case
  collisions, reserved names, `ReadDirectoryChangesW` drops, trailing dot/space)
  and hand them to Phase 6's CI `windows-latest` job + `CROSS_PLATFORM_CHECKLIST.md`.
- Close the XP-1..XP-6 confirmation checklist in `crossplatform-rules.md`.
