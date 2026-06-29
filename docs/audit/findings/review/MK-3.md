# Review verdict — MK-3 (Leaf metadata for two-way sync)

- Reviewed: 2026-06-29 (Phase 6 reviewer)
- Finding: `docs/audit/findings/merkle/MK-3-leaf-metadata-two-way-sync.md` (claimed `status: fixed`, WS-1, commit `182ff00`)
- **Verdict: FIXED**

## What was claimed
Each leaf is a `FileInfo{content_hash,size,mode,mtime,version_vector,deleted}`. The
structural hash commits to `{content_hash, 2-state mode, deleted, VV}` and EXCLUDES raw
`mode`/`mtime`/`size`, or convergence breaks. Tombstone-distinct-hash test green.

## Evidence verified against code
- The leaf struct matches the claim field-for-field: `internal/merkle/fileinfo.go:26-35`
  (`Path, ContentHash, Size, Mode, ModTimeNS, Version, Deleted, Type`), with per-field
  comments stating `Size`/`Mode`/`ModTimeNS` are NOT hashed.
- Structural-hash include/exclude is enforced in `leafEncoding`
  (`internal/merkle/codec.go:30-42`): hashes `ContentHash`, `canonicalModeByte()`,
  `Deleted`, `Version.Encode()` — and never appends `Size`, raw `Mode`, or `ModTimeNS`.
- 2-state mode: `fileinfo.go:49-58` `canonicalModeByte` = bit0 exec, bit1 symlink (raw
  POSIX bits excluded — XP-6).
- VV is part of the hashed identity (sorted, fixed-width, big-endian):
  `internal/protocol/versionvector.go:222-230` `Encode`.

## Evidence verified against tests (all PASS under `-race`)
- `internal/merkle/scanner_test.go:42` `TestTombstone_DistinctHash` — a tombstone's
  structural hash differs from its pre-delete leaf, and the tree root flips.
- `internal/merkle/merkle_test.go:236` `TestModeByte_TwoState` — the exec bit and the
  symlink type DO change the hash; a non-exec permission-only change (0644→0640) does NOT
  (proves raw mode is excluded, not leaked).
- `merkle_test.go:127` `TestStructuralHash_GoldenVector` pins that the encoding contains
  exactly `content_hash || modeByte || deleted || vv` (independent reconstruction l.141-150).
- `internal/merkle/scanner_test.go:149` `TestScanner_SizeMtimePrefilter` — identity is
  content_hash; size/mtime are hints only.

## Run-log corroboration
- `docs/audit/runs/race-all.log:6` `ok internal/merkle`; fresh 2026-06-29 run all `--- PASS`.

## Skeptical check
The exclusion of mtime/size from the hash is the make-or-break property (hashing mtime
would prevent cross-machine convergence). `TestModeByte_TwoState` directly proves a
permission-only difference does not move the hash, and the golden vector pins the exact
field set — a regression that started hashing mtime/size would fail both. No gap found.
