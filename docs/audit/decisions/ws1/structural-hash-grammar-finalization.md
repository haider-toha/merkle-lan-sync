# Decision: Finalize the byte-exact structural-hash grammar (fold the 2-state mode in)

- Area: WS-1 / merkle (implementer finalization of OQ-4)
- Status: decided
- Date: 2026-06-29
- Decider: WS-1 implementer
- Ratifies / completes: `docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md`
  §D (Option D byte grammar), folding in the amendment from
  `docs/audit/decisions/crossplatform/mode-symlink-mapping.md` (hash a portable
  2-state mode, NOT raw `mode`). Closes the merkle-researcher → WS-1 hand-off named
  in MK-1, the leaf-shape §Consequences, and `crossplatform-rules.md` XP-6.
- Consumes findings: MK-1 (domain separation), MK-3 (VV+tombstone in the hash),
  the XP-6 mode amendment. Implements plan WS-1 criterion 4 (R-1 gate).

## Context

The structural hash is the convergence ABI: "converged ⇔ identical root hash"
(SR-5) only holds if both peers compute the *same bytes* for the same logical tree
on Mac and Windows (SR-13). The leaf-shape decision pinned a byte grammar with
`mode : uint32` (raw) in the leaf. The later mode-symlink decision proved that
hashing **raw** `mode` is itself a cross-OS convergence bug: NTFS cannot store
POSIX bits, so a Mac `0755` file rescanned on Windows reports a different `mode` →
different leaf hash → roots never converge even though the bytes are identical.
The amendment says the hash must commit to a **portable 2-state** `{executable,
fileType}` instead. That change was explicitly handed to the implementer (this
file) to fold into the exact grammar. Getting the leaf encoding's mode field wrong
is the project's #1 risk (R-1).

## Options (scored 1-5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — keep `mode : uint32` (raw POSIX bits) in the leaf encoding
- Correctness **2**: reproduces the XP-6 convergence bug — Windows' inability to
  represent POSIX bits manufactures a permanent root difference for identical bytes.
- Concurrency **5** · Testability **4** · Cross-platform **1**.
- Rejected: defeats SR-5 across the OS boundary (the whole point of the project).

### Option B — exclude mode from the hash entirely
- Correctness **4**: converges, but a `chmod +x` (no content change) produces *no*
  structural signal, so an exec-bit toggle never propagates via the tree diff.
- Cross-platform **5** · Testability **5** · Concurrency **5**.
- Rejected: weaker than C for the one permission users actually sync; loses a real
  edit class.

### Option C — hash a canonical **2-state mode byte** `{executable, fileType}` (CHOSEN)
- One `uint8`: bit 0 = executable (any of owner/group/other `x` set on a regular
  file), bits 1-7 = `FileType` (0 = regular file, 1 = symlink). Directories never
  appear as leaves (they are nodes), so `fileType` distinguishes file vs symlink.
- Correctness **5**: an intentional `+x` still converges (both peers agree on the
  single exec bit); Windows' missing POSIX bits no longer manufacture diffs;
  symlink-vs-file is committed so a type change shows in the diff.
- Concurrency **5** (pure function of an immutable `FileInfo` snapshot) ·
  Testability **5** (golden vector + 2-state table) · Cross-platform **5**.
- Chosen.

## Decision

The structural hash is **`SHA-256(0x00 || leafEncoding)`** for a leaf and
**`SHA-256(0x01 || nodeEncoding)`** for a directory node (RFC 9162 §2.1.1 domain
separation, MK-1 — *required* for second-preimage resistance, one byte, not
optional). All integers big-endian. The grammar, finalized:

```
modeByte  : uint8  =  (executable ? 0x01 : 0)  |  (uint8(fileType) << 1)
                       # executable := fileType==File && (rawMode & 0o111 != 0)
                       # fileType: 0=File, 1=Symlink  (Dir is never a leaf)

leafEncoding =  content_hash[32]            # raw bytes; tombstone => 32x 0x00
             || modeByte : uint8            # the portable 2-state mode (replaces raw uint32)
             || deleted  : uint8            # 0x00 | 0x01
             || vvEncoding                  # = VersionVector.Encode():
                                            #   vvCount:uint16 || vvCount x (id:uint64 || value:uint64)
                                            #   counters SORTED ASCENDING by id, no zero values

nodeEncoding =  childCount : uint32
             || childCount x ( nameLen:uint16 || nameBytes(NFC, no '/') || childHash[32] )
                                            # children SORTED ASCENDING by bytewise compare of nameBytes
```

Pinned, load-bearing details:
- **Name committed by the parent only** (git model): the leaf encoding excludes the
  name; the node's child entry carries it once. Preserves identical-content dedup.
- **Excluded from the hash: raw `mode`, `mtime`, `size`.** `mtime` is per-machine
  volatile (would never converge); `size` is redundant with `content_hash`; raw
  `mode` is non-portable (Option A bug). All three stay in `FileInfo` for
  transfer/tiebreak/advisory use, none enter the hash.
- **VV reuses `protocol.VersionVector.Encode()`** (already byte-deterministic,
  sorted-ascending, big-endian, copy-on-write — WS-0). One grammar, one place.
- **Tombstone leaf:** `content_hash = 32x0x00`, `deleted = 0x01`, VV bumped ⇒ a
  hash distinct from the pre-delete leaf (WS-1 criterion 5).
- **n-ary, never duplicate-last** (CVE-2012-2459, MK-1): a directory node hashes its
  real children only; no power-of-two padding.
- **Empty directory** would be `SHA-256(0x01 || 00000000)`, but per CDD-8 empty
  dirs are not synced, so a dir node only exists on a path to a leaf and always has
  childCount >= 1 in practice.

## Rationale

- Option C is the minimum that both **converges Mac↔Windows** and still
  **propagates the one permission users sync** (`+x`). Strictly dominates A
  (diverges) and B (loses `+x`).
- Domain separation is a standards-blessed one-byte fix (RFC 9162) for a real
  collision class; the cost is negligible.
- A byte-exact grammar makes convergence a property of a fixed pure function,
  provable by a golden vector and the Mac→wire→Windows→wire→Mac root round-trip
  (the only defence against `merkle-tree` §4.3, the highest-probability bug here).

## Consequences

- Drives `internal/merkle/{fileinfo.go (FileType, modeByte), node.go (domain bytes
  + rebuild), codec.go (leafEncoding/nodeEncoding)}`.
- Tests: `TestStructuralHash_GoldenVector` (pins exact bytes + a stable hash hex so
  any future recipe change fails loudly), `TestCrossPlatformRoot_RoundTrip`,
  `TestOneByteChange_MinimalBranch`, `TestTombstone_DistinctHash`,
  `TestModeByte_TwoState`.
- Forward-compat: the recipe is versioned by the protocol `algo_version` /
  `featureFlags` (chunking decision); changing it is a fail-closed negotiated
  change, never silent.
- Cross-refs: SR-4/5/9/13, XP-6, GR-12; MK-1, MK-3; RFC 9162 §2.1.1.
