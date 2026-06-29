# Cross-platform finding — mode / mtime / symlink portability (a convergence bug in the leaf shape)

> **WS-1 status (fixed, Mac-side):** the structural hash commits to the portable
> 2-state mode `{executable, isSymlink}` (not raw mode) in
> `internal/merkle/{fileinfo.go,node.go,codec.go}`, and symlinks are typed leaves
> whose content is the hash of the normalised target in `internal/merkle/scanner.go`
> (not followed); 2-state and symlink-distinct-hash tests green. Decision
> `docs/audit/decisions/ws1/structural-hash-grammar-finalization.md`. The exec-bit /
> symlink on-disk behaviour on Windows (incl. refuse+flag on unprivileged) is
> WS-4/Phase-6. Commit `__WS1_SHA__`.

- Slug: `mode-symlink-portability` · confirms **XP-6** and **amends** the Phase 0
  leaf-shape structural-hash recipe
- Phase: 2 (crossplatform-researcher, elevated track)
- Reads-first: `docs/audit/rules/crossplatform-rules.md` (XP-6), `sync-rules.md`
  (SR-4, SR-5), `decisions/phase0/merkle-leaf-shape.md`.
- Decision logged before this finding:
  `docs/audit/decisions/crossplatform/mode-symlink-mapping.md`.
- Access date for all URLs: **2026-06-28**.
- Severity: **High** — as currently specified (raw `mode` in the structural hash),
  any executable file synced Mac→Windows makes the roots diverge (SR-5 broken),
  even with identical bytes. This finding fixes that.

## Claim

POSIX `mode` bits, the executable bit, high-resolution `mtime`, and symlinks are
**not portable** Mac↔Windows. The structural hash must commit only to a
**canonical 2-state** `{executable, fileType}` derived from `mode` — not the raw
`mode` — or convergence breaks. `mtime` stays out of the structural hash (tiebreaker
only, SR-4). Symlinks are typed leaves; creating them on unprivileged Windows is
refuse+flag.

## The bug this surfaces

`decisions/phase0/merkle-leaf-shape.md` currently lists **`mode`** among the fields
**included in the structural hash**. That is safe within one OS but breaks across
OSes:

- **NTFS has no POSIX permission model and no executable bit.** A Mac file at
  `0755` cannot be stored as `0755` on Windows; Go's `os.Stat` on Windows derives
  `mode` from file attributes (e.g. the read-only bit), so the Windows peer rescans
  a *different* `mode` for the same file. Different `mode` → different structural
  hash → **roots never converge (SR-5)**, despite identical content. This is the
  same failure class as a stored `\` or an NFD-vs-NFC name: a non-portable value in
  the hash. (Go's Windows `FileMode` mapping:
  [pkg.go.dev/io/fs#FileMode](https://pkg.go.dev/io/fs#FileMode) and
  [pkg.go.dev/os#Stat](https://pkg.go.dev/os#Stat), accessed 2026-06-28; Windows's
  lack of POSIX permissions is documented on the Microsoft naming page's
  permissions model and CreateFile attribute semantics, accessed 2026-06-28.)

## Decision applied

`decisions/crossplatform/mode-symlink-mapping.md`:
- **`mode` → canonical 2-state before hashing** (Option B). The structural hash
  commits to `{ isExecutable: any owner/group/other x-bit set on a regular file;
  fileType: file|dir|symlink }`. Full `mode` is retained in `FileInfo` as advisory
  and applied best-effort, but never hashed and never a conflict trigger on its own.
  This **amends** the leaf-shape "Included in structural hash" list from `mode` to
  `canonical(mode)` and is handed to **merkle-researcher** (OQ-4, exact
  serialisation) and **tree-critic**. Chosen over keeping raw mode (diverges) and
  over dropping mode entirely (would stop `chmod +x` from propagating).
- **Symlinks = typed leaves** carrying the forward-slash NFC target (Option S-A);
  on Windows without `SeCreateSymbolicLinkPrivilege` / Developer Mode, **refuse +
  flag** rather than error the sync. (Windows symlink creation requires that
  privilege — [Microsoft, *Symbolic Links* / CreateSymbolicLink](https://learn.microsoft.com/en-us/windows/win32/fileio/symbolic-links),
  accessed 2026-06-28.) Not followed (cycle/root-escape risk) and not materialised
  as a text file (lossy/ambiguous).
- **`mtime`** confirmed **excluded** from the structural hash (SR-4, leaf-shape),
  used only as the SR-7 conflict tiebreaker, compared with a tolerance large enough
  to absorb FAT's 2 s granularity and cross-OS sub-second precision differences.

## Test obligations

- Mac-testable: `canonicalMode(0755) == canonicalMode(0775) == {exec}` and
  `canonicalMode(0644) == {regular}`; a file's structural hash is invariant under a
  non-exec permission change but changes on a `+x` toggle.
- Symlink typed-leaf hashing (Mac-testable); creation-refusal path covered by a
  Windows test.

## Cannot be verified on the Mac → Phase 6

The exec-bit round-trip Mac→Windows→Mac and the symlink-creation-privilege
behaviour are Windows-only (plan/README pins "MAX_PATH … reserved names …" and the
permissions gap to a real Windows target). Closed by the CI `windows-latest` job +
`docs/audit/CROSS_PLATFORM_CHECKLIST.md`. The 2-state mapping is fully
Mac-unit-testable.
