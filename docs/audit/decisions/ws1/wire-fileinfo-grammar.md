# Decision: The wire FileInfo payload grammar (WS-0/WS-1 seam)

- Area: WS-1 / merkle + protocol seam
- Status: decided
- Date: 2026-06-29
- Decider: WS-1 implementer
- Completes: the seam named in `docs/audit/plan/implementation-plan.md` WS-0 "Seam"
  and `internal/protocol/doc.go` — "The INDEX / INDEX_UPDATE payload carries the
  wire FileInfo grammar, which is finalized in WS-1." WS-0 ships the INDEX envelope
  with an opaque `Body`; this decision fixes the per-entry bytes.
- Consumes: MK-3 (the leaf field set a peer needs), PR-1 (length-prefixed,
  big-endian framing primitives), the WS-0 `VersionVector.Encode/Decode`.

## Context

INDEX / INDEX_UPDATE carry a set of `FileInfo` between peers. The *structural*
hash (separate decision) deliberately commits to only a subset of fields and a
2-state mode; the **wire** form must carry the *full* `FileInfo` the reconcile
layer (WS-4) needs to act: the canonical path (identity), `content_hash` (transfer
key), `size` + `mtime` (pre-filter + conflict tiebreaker), full `mode` (advisory
apply), `fileType`, `deleted`, and the version vector (causality). It must be
byte-deterministic and identical on Mac and Windows (SR-13), and bounds-checked at
the trust boundary (GR-7/GR-8): a peer's bytes are validated, never trusted.

## Options (scored 1-5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — reuse the structural `leafEncoding` as the wire form
- Correctness **1**: the structural encoding omits `path`, `size`, `mtime`, and
  full `mode` by design — a receiver could not place the file, pre-filter, break a
  conflict tie, or apply permissions. Rejected.

### Option B — `gob`-encode `FileInfo` on the wire
- Correctness **2** at the trust boundary: GR-7 forbids decoding `gob` from the
  network (self-describing, unhardened against adversarial input). Rejected for the
  wire (gob is reserved for the *local* snapshot — separate decision).

### Option C — a fixed, length-prefixed, big-endian record per FileInfo (CHOSEN)
- A hand-rolled record mirroring the framing primitives: every variable region
  length-prefixed, fixed integer widths, big-endian, VV via the existing
  `VersionVector.Encode`. Bounds-checked decode (a truncated/adversarial record →
  typed error, never a panic or over-read).
- Correctness **5** · Concurrency **5** (pure) · Testability **5** (round-trip +
  truncation table) · Cross-platform **5** (no OS-specific bytes).
- Chosen — it is the same discipline WS-0 used for HELLO/REQUEST.

## Decision

Adopt **Option C**. One FileInfo on the wire:

```
wireFileInfo =  pathLen      : uint16            # canonical NFC forward-slash key
             || pathBytes    : pathLen
             || content_hash : [32]byte
             || size         : uint64
             || mode         : uint32            # FULL advisory mode (not the 2-state)
             || mtime        : int64             # ns; tiebreaker only
             || fileType     : uint8             # 0=File 1=Dir 2=Symlink
             || deleted      : uint8             # 0x00 | 0x01
             || vvEncoding                       # protocol.VersionVector.Encode()

EncodeFileInfos(set) = count:uint32 || count x wireFileInfo
```

The INDEX/INDEX_UPDATE envelope (WS-0) already carries `FolderID` + `Count`;
`Index.Body` is exactly `count x wireFileInfo` (so `EncodeFileInfos` and the
envelope agree on `count`). Decoding validates: `pathLen` within remaining bytes,
the path is a valid canonical key (re-`CanonicalizeSlash`, reject `..`/absolute),
`fileType` in range, VV canonical (the WS-0 decoder already rejects
non-canonical) — anything else is `ErrMalformedFileInfo` and the peer is dropped.

`fileType` includes `Dir = 1` for forward-compatibility even though v1 does not put
directory `FileInfo`s on the wire (empty dirs not synced, CDD-8); a received
`Dir` entry is currently ignored by the reconcile layer.

## Rationale

- Carrying the full `FileInfo` (including the fields the structural hash omits) is
  exactly what lets the receiver pre-filter (size/mtime), break ties (mtime), and
  apply advisory metadata — without ever using those fields for *identity* (SR-4).
- A fixed length-prefixed record is the GR-7/GR-8-compliant boundary format, and is
  trivially fuzz/round-trip testable on the Mac.
- Reusing `VersionVector.Encode` keeps a single VV grammar shared by the wire form,
  the structural hash, and the snapshot.

## Consequences

- Drives `internal/merkle/codec.go` (`EncodeFileInfo`/`DecodeFileInfo`,
  `EncodeFileInfos`/`DecodeFileInfos`, `ErrMalformedFileInfo`).
- Tests: `TestWireFileInfo_RoundTrip`, `TestWireFileInfo_TruncationRejected`,
  `TestWireFileInfos_CountMismatch`.
- WS-4 builds INDEX bodies from `EncodeFileInfos` and decodes peer bodies with
  `DecodeFileInfos`; the envelope `Count` must equal the decoded count.
- Cross-refs: SR-4/13, GR-7/8; MK-3; PR-1.
