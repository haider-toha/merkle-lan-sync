# Antipattern finding — assuming os.Rename atomically replaces an existing file on Windows

- **Catalogue ID:** AP-03 · **finding slug:** `windows-rename-not-atomic`
- **Source slug:** antipatterns
- **Phase / role:** Phase 2 — antipatterns-researcher (anti-slop pass)
- **Status:** open
- **Severity:** high
- **Refines rule:** **SR-1 / SR-2** (the Windows replace caveat both already flag, made concrete)
- **Reads-first honoured:** `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md` (R-4)
- **Access date for all URLs:** 2026-06-28

## Claim

The atomic-write rule (SR-1/SR-2) assumes `os.Rename(tmp, dst)` atomically
*replaces* an existing `dst`. That holds on macOS (POSIX) but **not on Windows**,
which is half of this project's entire reason to exist. A naive rename on the
Windows peer either errors when `dst` exists (so the received update is silently
**never applied** → stale/lost edit) or, via a non-atomic fallback, can leave a
corrupt `dst`. SR-2 already notes "on non-Unix platforms Rename is not an atomic
operation"; this finding makes the failure and the fix concrete and testable.

## Wrong shape

```go
// works on macOS; on Windows fails or is non-atomic when dst already exists
func finalize(tmp, dst string) error {
    if err := tmpFile.Sync(); err != nil { return err }
    return os.Rename(tmp, dst)        // Windows: ERROR_ALREADY_EXISTS or non-atomic replace
}
```

## Why it LOSES/CORRUPTS data (not merely slow)

The Go issue is explicit:

> "In Posix-compliant OSes, the os.Rename() function works properly (and
> atomically), including when replacing an existing file. In Windows this does not
> work correctly as designed and fails when replacing an existing file."
> ([golang/go #8914, *os: make Rename atomic on Windows*](https://github.com/golang/go/issues/8914))

Even the right Windows primitive is only conditionally atomic: `ReplaceFile`/
`MoveFileEx` are atomic for a move **within one NTFS volume** (internal transaction
journal) but **not** across volumes or on FAT/network shares, and the temp,
target, and backup must all be on the same volume
([golang-nuts, *Atomic replacement of files on Windows*](https://groups.google.com/g/golang-nuts/c/AFdIJYK5IZk)).
Consequences on the Windows side of a Mac↔Windows pair:
- naive `os.Rename` over an existing file → error → the apply path drops the
  update (the user's edit from the Mac silently never lands), or
- a "helpful" copy-fallback reintroduces the in-place-write window (AP-01) → a
  crash mid-replace corrupts `dst`.
This is risk **R-4** (non-atomic / interrupted-transfer corruption) realised on
the exact target the project promises to support, and it is **unverifiable on the
Mac** — it must be closed by the windows-latest CI job + the cross-platform
checklist (plan/README.md).

## How to test (the failing assertion)

Run on Windows (CI `windows-latest`) and via `GOOS=windows` build verification:
```go
write(dst, []byte("old"))                       // dst already exists
writeTemp(tmp, []byte("new"))
err := finalize(tmp, dst)
assert.NoError(t, err)                           // FAILS for naive os.Rename on Windows
assert.Equal(t, "new", read(dst))                // replace succeeded
// interruption: kill between sync and replace; assert dst is "old" or "new", never partial
```
Also: a same-volume invariant test asserting tmp and dst share a volume.

## Correct approach (refine SR-1/SR-2 with a platform replace)

- Use the platform replace primitive on Windows:
  `MoveFileEx(tmp, dst, MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH)` or
  `ReplaceFileW`; on POSIX keep `os.Rename`. A vetted library does this:
  `natefinch/atomic` "uses `ReplaceFileW`" on Windows and `os.Rename` elsewhere
  ([natefinch/atomic](https://github.com/natefinch/atomic);
  [google/renameio](https://pkg.go.dev/github.com/google/renameio) for the POSIX
  side) — adopting one is a logged GR-11 dependency decision, or reimplement the
  syscall behind a build-tagged `replaceFile(tmp, dst)`.
- Enforce **same-volume** temp placement (AP-04) so the Windows replace can be
  transactional.
- Keep SR-2's ordering (`Sync` temp → replace → fsync dir) on both platforms.

Lands in `internal/reconcile/transfer.go` behind a small
`rename_windows.go` / `rename_unix.go` split.

## Cross-references

- Catalogue: `docs/audit/rules/sync-antipatterns.md` AP-03 (+ AP-04 same-volume).
- Rules: makes the SR-1/SR-2 "non-Unix Rename is not atomic" caveat concrete and
  testable; cross-platform (XP) — closed only by the CI windows job + checklist.
- Synthesis: realises **R-4** on the Windows target.
- Decision: `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.

## Sources (accessed 2026-06-28)

- golang/go #8914, *os: make Rename atomic on Windows* — https://github.com/golang/go/issues/8914
- golang-nuts, *Atomic replacement of files on Windows* — https://groups.google.com/g/golang-nuts/c/AFdIJYK5IZk
- natefinch/atomic — https://github.com/natefinch/atomic
- google/renameio — https://pkg.go.dev/github.com/google/renameio
