# Literature finding — Syncthing Block Exchange Protocol (BEP v1)

- Source slug: `syncthing-bep`
- Phase: 1 (literature-mapper)
- Reads-first: `docs/audit/rules/` (sync-rules.md SR-1..SR-13, go-rules.md GR-1..GR-13,
  crossplatform-rules.md XP-1..XP-6) and the four Phase 0 decisions in
  `docs/audit/decisions/phase0/`.
- Access date for all URLs: **2026-06-28**.
- Source pin: all `syncthing/syncthing` file:line citations are against
  **commit `2775f424f228` (branch `main`, committed 2026-06-28T06:00:08Z)**.
- Scope (from roster): FileInfo fields (version vector, deleted, modified_by,
  blocks), Index/IndexUpdate exchange, how blocks are Requested/Responded, the
  local-vs-global model.

> **Role note (autonomy contract / DECISIONS clause).** A literature finding
> documents and *recommends*; it does not commit the project to anything, so it
> creates no `docs/audit/decisions/` file. The consequential BEP-derived baseline
> choices are *already* logged in Phase 0 (`merkle-leaf-shape.md`,
> `framing-format.md`, `message-type-codes.md`,
> `transport-security-tofu-vs-plaintext.md`). The remaining adopt/adapt calls this
> finding surfaces (block size, VV counter seeding, delta-index simplification,
> final message catalogue) are explicitly owned by the Phase 2 **merkle-researcher**
> and **protocol-researcher**, who must log the decision before acting. Each open
> item below names its owner.

---

## 0. Reproduction (how to re-derive the source claims)

The exact bytes behind every `file:line (@2775f424f228)` citation can be re-fetched
with `gh` (GitHub CLI, not `git`):

```
gh api 'repos/syncthing/syncthing/contents/proto/bep/bep.proto?ref=main'           -H 'Accept: application/vnd.github.raw'
gh api 'repos/syncthing/syncthing/contents/lib/protocol/vector.go?ref=main'        -H 'Accept: application/vnd.github.raw'
gh api 'repos/syncthing/syncthing/contents/lib/protocol/bep_fileinfo.go?ref=main'  -H 'Accept: application/vnd.github.raw'
gh api 'repos/syncthing/syncthing/contents/lib/protocol/protocol.go?ref=main'      -H 'Accept: application/vnd.github.raw'
gh api repos/syncthing/syncthing/commits/main --jq '.sha'   # -> 2775f424f228...
```

Note: the `.proto` lives at `proto/bep/bep.proto`; the *generated* Go lives under
`internal/gen/bep/` (imported as `github.com/syncthing/syncthing/internal/gen/bep`);
the hand-written wrapper types live in `lib/protocol/`. The published spec is
[Block Exchange Protocol v1](https://docs.syncthing.net/specs/bep-v1.html)
(accessed 2026-06-28).

---

## 1. Core algorithm (the BEP loop in one page)

BEP is the device-to-device protocol Syncthing uses to keep a *folder* identical
across a set of *devices*. It is **not** a client/server protocol — every peer is
symmetric. The package self-describes as "Package protocol implements the Block
Exchange Protocol" (`lib/protocol/doc.go:7 @2775f424f228`).

The steady-state algorithm per connected pair, per folder:

1. **Authenticate + announce.** Open TCP, exchange pre-auth `Hello`, perform a
   TLS 1.3 handshake (each side presents a self-signed cert; the peer's *device
   ID* is the SHA-256 of that cert). Then each side sends a `ClusterConfig`
   announcing, per folder, its known `index_id` and `max_sequence` for every
   device (`bep.proto:57-68`).
2. **Index exchange.** Each side sends an `Index` (full snapshot: a list of
   `FileInfo`, one per path) or, if the peer already holds prior index data for
   this `{index_id}`, only an `IndexUpdate` carrying the `FileInfo`s changed since
   the peer's `max_sequence` ("delta indexes"). See §6.
3. **Compute global vs local.** On receiving index data a device recomputes, for
   every path, the **global** (best/newest) version across all peers and compares
   it to its **local** (on-disk) version. Files where global ≠ local are flagged
   "needed". See §8.
4. **Pull needed files block-by-block.** For each needed file, diff the global
   block list against any local source; for each *missing* block send a `Request`
   (folder, name, offset, size, expected SHA-256 hash); the peer replies with a
   `Response` carrying the bytes. Verify each block's SHA-256 against the expected
   hash, write into a temporary file, and atomically rename into place. See §7.
5. **Conflict / deletion resolution.** Version vectors decide direction. If the
   two versions are *concurrent* (neither happened-before the other) and the
   content differs, keep both: the loser is renamed to a `.sync-conflict-…` copy
   (§5). A deletion is a versioned tombstone, not an absence (§4.5).
6. **Steady state.** `Ping` keepalives; `DownloadProgress` advertises partially
   downloaded blocks so peers can fetch from each other's temp files; `Close`
   ends the session.

The invariant that makes this converge: once propagation quiesces, every device
holds the identical winning `FileInfo` (version vector included) for every path.

---

## 2. Connection, framing, and message catalogue (EXACT layouts)

### 2.1 Pre-authentication: the Hello

Before any auth, "devices must exchange Hello messages", prefixed with
"an int32 containing the magic number `0x2EA7D90B`, followed by an int16
representing the size of the message"
([BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28):

```
[ int32 magic = 0x2EA7D90B ][ int16 hello_len ][ Hello (protobuf) ]
```

```proto
message Hello {                       // bep.proto:7-13 @2775f424f228
  string device_name    = 1;
  string client_name    = 2;
  string client_version = 3;
  int32  num_connections = 4;         // multi-connection feature (modern)
  int64  timestamp       = 5;
}
```

### 2.2 Post-authentication frame (two-level, big-endian)

After the TLS handshake every message is framed as
([BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28):

```
+-------------------------------+
|  Header Length (16-bit, BE)   |
+-------------------------------+
/  Header  (protobuf)           /   // bep.proto:17-20
+-------------------------------+
|  Message Length (32-bit, BE)  |
+-------------------------------+
/  Message (protobuf, maybe LZ4)/
+-------------------------------+
```

```proto
message Header {                                  // bep.proto:17-20
  MessageType        type        = 1;
  MessageCompression compression = 2;             // NONE=0, LZ4=1 (bep.proto:33-36)
}
```

"All length values use network byte order (big-endian)" — this is the convention
Merkle Sync's framing decision already adopted
([BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html), accessed 2026-06-28;
`docs/audit/decisions/phase0/framing-format.md`).

### 2.3 Message types (the 8-type enum)

```proto
enum MessageType {                                // bep.proto:22-31
  MESSAGE_TYPE_CLUSTER_CONFIG    = 0;
  MESSAGE_TYPE_INDEX             = 1;
  MESSAGE_TYPE_INDEX_UPDATE      = 2;
  MESSAGE_TYPE_REQUEST           = 3;
  MESSAGE_TYPE_RESPONSE          = 4;
  MESSAGE_TYPE_DOWNLOAD_PROGRESS = 5;
  MESSAGE_TYPE_PING              = 6;
  MESSAGE_TYPE_CLOSE             = 7;
}
```

This is the model for Merkle Sync's 7-code catalogue
(`docs/audit/decisions/phase0/message-type-codes.md`); we map INDEX/INDEX_UPDATE/
REQUEST/RESPONSE/PING/CLOSE and replace CLUSTER_CONFIG with a leaner HELLO, and we
defer DOWNLOAD_PROGRESS (§9 table).

### 2.4 Message-size guard

`MaxMessageLen = 500 * 1000 * 1000` (500 MB) is the hard ceiling on a decoded
message (`lib/protocol/protocol.go`, constants confirmed @2775f424f228; also
[pkg.go.dev/.../lib/protocol](https://pkg.go.dev/github.com/syncthing/syncthing/lib/protocol),
accessed 2026-06-28). This is the same DoS-guard role as Merkle Sync's
`MaxFrameLen = 16 MiB` (SR-12 / GR-8 / `framing-format.md`); BEP's is larger
because a whole `Index` can be one message.

---

## 3. FileInfo — EXACT field layout (the leaf)

`FileInfo` is the per-path record exchanged in `Index`/`IndexUpdate`. It is the
direct reference for Merkle Sync's `merkle-leaf-shape.md` (Option C). Verbatim
proto (`bep.proto:103-143 @2775f424f228`):

```proto
message FileInfo {
  // field ordering optimizes struct size/alignment; large types first
  string             name                 = 1;
  int64              size                 = 3;
  int64              modified_s           = 5;   // mod time, whole seconds (Unix)
  uint64             modified_by          = 12;  // ShortID of the device that last changed it
  Vector             version              = 9;   // the version vector (causality)
  int64              sequence             = 10;  // per-device monotonic change counter (delta indexes)
  repeated BlockInfo blocks               = 16;  // content blocks (empty for dirs/deletes)
  bytes              symlink_target       = 17;
  bytes              blocks_hash          = 18;  // SHA-256 over the block-hash list (§7.4)
  bytes              previous_blocks_hash = 20;  // blocks_hash of the version this was based on
  bytes              encrypted            = 19;  // untrusted-peer encryption (feature; out of scope for us)
  FileInfoType       type                 = 2;   // FILE=0, DIRECTORY=1, SYMLINK=4 (2,3 deprecated)
  uint32             permissions          = 4;
  int32              modified_ns          = 11;  // mod time, nanosecond part
  int32              block_size           = 13;  // bytes per block for THIS file (adaptive, §7.3)
  PlatformData       platform             = 14;  // unix/windows owner + xattrs

  // ---- host-local fields, field numbers >= 1000, ZEROED on the wire ----
  uint32             local_flags             = 1000;  // §8: Global/Needed/Ignored/... (NOT sent)
  bytes              version_hash            = 1001;  // old db format only
  int32              encryption_trailer_size = 1003;  // host-local

  bool               deleted        = 6;   // tombstone marker (§4.5)
  bool               invalid        = 7;   // unreadable / ignored on the source
  bool               no_permissions = 8;   // ignore the permissions field

  reserved 1002; // previously inode change time
}
```

Key field semantics for two-way sync:

| field | role | maps to Merkle Sync |
|---|---|---|
| `name` | identity / tree position; a **raw byte string** — BEP does **not** normalize Unicode or case | we MUST canonicalize first: forward-slash relative + NFC (SR-13, XP-1, XP-2) |
| `version` (`Vector`) | causal ordering; the *only* truth for "who is newer / concurrent?" | adopt wholesale (SR-4, §4) |
| `modified_by` | `ShortID` (64-bit) of the authoring device; doubles as the conflict tie-break identity and as the counter ID in `version` | adopt (SR-7) |
| `deleted` | a delete is a *versioned event* (tombstone), not an absence | adopt (SR-9/SR-10) |
| `blocks` / `block_size` | content addressing + transfer plan | adopt block content-addressing; block-size is an open decision (§7.3) |
| `modified_s` / `modified_ns` | conflict **tiebreaker only**, never the ordering truth | adopt (SR-4: mtime is tiebreaker, VV is truth) |
| `permissions` / `no_permissions` | best-effort across OSes | adapt; mode is non-portable (XP-6) |
| `sequence` | per-device monotonic counter enabling delta indexes | adapt/likely-defer (§6, §9) |
| `local_flags` (>=1000) | host-local state, zeroed on wire | informs our local model (§8) |
| `platform` / `encrypted` / `version_hash` | ownership/xattr sync, at-rest encryption, db detail | reject/defer for a 2-device LAN tool (§9) |

The hand-written Go mirror is `type FileInfo struct {…}`
(`bep_fileinfo.go:115-150 @2775f424f228`); `ToWire(withInternalFields bool)`
(`:152-186`) shows the >=1000 fields are only serialized for the local database,
never to a peer (`Invalid: f.IsInvalid()` is computed from `local_flags`, but the
raw flags are sent only when `withInternalFields`).

### 3.1 Enums

```proto
enum FileInfoType {                  // bep.proto:145-151
  FILE_INFO_TYPE_FILE              = 0;
  FILE_INFO_TYPE_DIRECTORY         = 1;
  FILE_INFO_TYPE_SYMLINK_FILE      = 2 [deprecated = true];
  FILE_INFO_TYPE_SYMLINK_DIRECTORY = 3 [deprecated = true];
  FILE_INFO_TYPE_SYMLINK           = 4;
}
```

---

## 4. Version vectors — EXACT formulas (the causality engine)

This is the part Merkle Sync should **adopt almost verbatim**: it is small, pure,
integer-only, big-endian-safe (so identical on Mac/Windows), and directly
satisfies SR-4 and SR-7.

### 4.1 Types

```go
type Vector struct { Counters []Counter }           // vector.go:24-26
type Counter struct { ID ShortID; Value uint64 }    // vector.go:100-103
```

`Counters` are kept **sorted by `ID`** (an invariant relied on by `Compare`,
`Merge`, `Update`). `ID` is a `ShortID` = the 64-bit prefix of a device's
certificate-derived device ID — i.e. the counter ID in the vector is the device
identity, and it equals `modified_by`. On the wire `Counter{id uint64, value
uint64}` (`bep.proto:164-167`).

### 4.2 Ordering — the 5-valued result

```go
type Ordering int                                   // vector.go:246-254
const ( Equal Ordering = iota; Greater; Lesser; ConcurrentLesser; ConcurrentGreater )
```

Crucial design comment (`vector.go:256-260 @2775f424f228`):

> "There's really no such thing as 'concurrent lesser' and 'concurrent greater'
> in version vectors, just 'concurrent'. But it's useful to be able to get a
> strict ordering between versions for stable sorts and so on, so we return both
> variants."

That is the whole trick behind a **deterministic, symmetric** conflict winner
(SR-7): when two vectors are genuinely concurrent, `Compare` still returns one of
`ConcurrentGreater`/`ConcurrentLesser`, and because both peers run the identical
parallel walk over ID-sorted counters they independently pick the **same** winner.

### 4.3 Compare — happens-before vs concurrent (the core)

`func (v Vector) Compare(b Vector) Ordering` (`vector.go:262-329 @2775f424f228`)
walks both ID-sorted counter lists in parallel:

- start `result = Equal`;
- at each ID, take `av`/`bv` (a missing counter is treated as value 0);
- if `av.Value > bv.Value`: if we had already seen a `Lesser` → return
  `ConcurrentLesser`, else set `result = Greater`;
- if `av.Value < bv.Value`: if we had already seen a `Greater` → return
  `ConcurrentGreater`, else set `result = Lesser`;
- after the walk, return `result`.

So: `Greater`/`Lesser` ⇒ one strictly dominates (happens-before); `Equal` ⇒
identical history; `ConcurrentGreater`/`ConcurrentLesser` ⇒ neither dominates =
**a real conflict**. `Concurrent()` is just `comp == ConcurrentGreater || comp ==
ConcurrentLesser` (`vector.go:212-216`). This is exactly the
dominates / dominated-by / concurrent classification SR-4 mandates testing.

### 4.4 Update — and the wall-clock coupling (IMPORTANT for SR-4)

```go
func (v Vector) Update(id ShortID) Vector {         // vector.go:122-125
  now := uint64(time.Now().Unix())
  return v.updateWithNow(id, now)
}
func (v Vector) updateWithNow(id ShortID, now uint64) Vector {  // :127-149
  // existing counter:  Value = max(Value+1, now)
  // new counter:       Value = max(1, now)
}
```

**This is a sharp, easily-missed detail and a genuine adapt decision.** Syncthing's
version-vector counter is **not a pure logical counter** — it is seeded with the
authoring device's wall-clock Unix seconds, falling back to `prev+1` only when the
clock would move it backwards. The doc comment still reads "incremented by one"
([pkg.go.dev](https://pkg.go.dev/github.com/syncthing/syncthing/lib/protocol),
accessed 2026-06-28), but the implementation folds the wall clock in. This is done
so counters stay globally monotonic and survive a database loss without going
backwards.

Consequence for us: this *partially re-introduces wall-clock into ordering*, which
is exactly what SR-4 warns against. The causality math (`Compare`/`Merge`) is still
correct regardless of how counters are seeded, but a device with a badly-skewed
(far-future) clock will mint huge counter values that dominate honest `+1`
increments for a long time. **Recommendation (owner: protocol-researcher /
merkle-researcher):** Merkle Sync should prefer a **pure logical counter**
(`Value = prev + 1`) so mtime stays *strictly* a tiebreaker per SR-4; accept the
db-loss-resilience tradeoff (a 2-device LAN tool can re-seed its counter from the
peer's vector via `Merge` on reconnect). Log this in a Phase 2 decision before
implementing.

### 4.5 Merge and tombstones

`func (v Vector) Merge(b Vector) Vector` (`vector.go:151-184`) takes the
element-wise maximum of each counter (ID-sorted merge). Used to combine knowledge.

Deletion is a versioned event, not an absence — `SetDeleted`
(`bep_fileinfo.go:588-594 @2775f424f228`):

```go
func (f *FileInfo) SetDeleted(by ShortID) {
  f.ModifiedBy = by
  f.Deleted    = true
  f.Version    = f.Version.Update(by)   // bump the VV — a delete is an edit
  f.ModifiedS  = time.Now().Unix()
  f.setNoContent()                      // Blocks=nil, BlocksHash=nil, Size=0
}
```

This is precisely the tombstone model SR-9/SR-10 require: the `FileInfo` survives
with `deleted=true` and a *bumped* version vector, so it can dominate a stale
peer's pre-delete version (no resurrection) and can win/lose a conflict
deterministically.

### 4.6 WinsConflict — the deterministic winner (adopt verbatim)

`func (f *FileInfo) WinsConflict(other FileInfo) bool`
(`bep_fileinfo.go:208-227 @2775f424f228`):

```go
// 1. If only one side is invalid, the valid one wins.
if f.IsInvalid() != other.IsInvalid() { return !f.IsInvalid() }
// 2. Newer modification time wins.
if f.ModTime().After(other.ModTime())  { return true }
if f.ModTime().Before(other.ModTime()) { return false }
// 3. Equal mtimes: use the version vector as the deterministic tie-break.
return f.FileVersion().Compare(other.FileVersion()) == ConcurrentGreater
```

This is the exact ordering SR-7 already cites
([Understanding Synchronization](https://docs.syncthing.net/users/syncing.html),
accessed 2026-06-28): "The file with the older modification time will be marked as
the conflicting file … If the modification times are equal, the file originating
from the device which has the larger value of the first 63 bits for its device ID
will be marked as the conflicting file." The user-facing "larger 63 bits of device
ID" phrasing is the *intent*; the code ground-truth is the `ConcurrentGreater`
walk, which is deterministic and symmetric (both peers agree on the loser).

`InConflictWith` (`bep_fileinfo.go:188-206`) is the *detector*: a new file is **not**
a conflict if its version `GreaterEqual` the existing version; otherwise it checks
`previous_blocks_hash` against the existing `blocks_hash` — if the new file was
"based on" the content we hold, it is treated as a fast-forward, not a conflict.
That `previous_blocks_hash` chaining is a content-causality refinement *beyond*
pure version vectors (see §7.4; relevant to the merkle-researcher).

---

## 5. Conflict copies (no-data-loss policy)

From [Understanding Synchronization](https://docs.syncthing.net/users/syncing.html)
(accessed 2026-06-28), verbatim:

- "one of the files will be renamed to
  `<filename>.sync-conflict-<date>-<time>-<modifiedBy>.<ext>`";
- "The file with the older modification time will be marked as the conflicting
  file" (i.e. renamed; the newer stays at the canonical path);
- tie: "the device which has the larger value of the first 63 bits for its device
  ID" loses;
- modification-vs-deletion: "If the conflict is between a modification and a
  deletion of the file, and the deletion wins the conflict resolution, the file is
  renamed to a conflict copy" (so even a *losing modification* is preserved);
- propagation: "the `sync-conflict` files are treated as normal files after they
  are created, so they are propagated between devices."

This is SR-7/SR-9 verbatim — Merkle Sync adopts it (the rules already cite this
exact page). The loser is **renamed, never deleted**; the conflict copy then syncs
as an ordinary file so both devices end up holding both versions.

---

## 6. Index / IndexUpdate exchange + delta indexes

### 6.1 Messages (EXACT)

```proto
message Index {                                   // bep.proto:90-94
  string            folder        = 1;
  repeated FileInfo files         = 2;
  int64             last_sequence = 3;            // highest sequence in this batch
}
message IndexUpdate {                             // bep.proto:96-101
  string            folder        = 1;
  repeated FileInfo files         = 2;
  int64             last_sequence = 3;
  int64             prev_sequence = 4;            // highest sequence in the PREVIOUS batch (gap detection)
}
```

`Index` = full snapshot (sent first, or after a reset). `IndexUpdate` = the
`FileInfo`s changed since the last batch. `prev_sequence`/`last_sequence` let the
receiver detect a dropped/misordered batch.

### 6.2 Sequence numbers and Index IDs (delta indexes)

From [BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html) and the
ClusterConfig `Device` fields (`bep.proto:57-68`), accessed 2026-06-28:

- Every index item has a `sequence` "value of a counter at the time the index item
  was updated. The counter increments by one for each change" — per-device,
  strictly monotonic (1, 2, 3, …).
- Each folder has a 64-bit random `index_id` "set at index creation time".
- "the tuple {index ID, maximum sequence number} uniquely identifies a point in
  time of a given index."
- In `ClusterConfig`, each `Device` carries `index_id` (8) and `max_sequence` (6)
  (`bep.proto:65,63`). "Devices announcing a non-zero index ID in the Cluster
  Config message MUST send all index data ordered by increasing sequence number."
- The receiver, knowing the `{index_id, max_sequence}` it last saw, asks for only
  the delta — "This is called 'delta indexes'."
  ([Delta index implementation forum thread](https://forum.syncthing.net/t/delta-index-implementation/7772);
  [issue #438](https://github.com/syncthing/syncthing/issues/438), accessed 2026-06-28.)

### 6.3 ClusterConfig / Device / Folder (EXACT, for completeness)

```proto
message ClusterConfig { repeated Folder folders = 1; bool secondary = 2; } // bep.proto:42-45
message Folder {                                  // bep.proto:47-55
  string id = 1; string label = 2; FolderType type = 3;
  FolderStopReason stop_reason = 7; reserved 4 to 6;
  repeated Device devices = 16;
}
message Device {                                  // bep.proto:57-68
  bytes id = 1; string name = 2; repeated string addresses = 3;
  Compression compression = 4; string cert_name = 5;
  int64 max_sequence = 6; bool introducer = 7; uint64 index_id = 8;
  bool skip_introduction_removals = 9; bytes encryption_password_token = 10;
}
// FolderType: SEND_RECEIVE=0, SEND_ONLY=1, RECEIVE_ONLY=2, RECEIVE_ENCRYPTED=3 (bep.proto:76-81)
```

### 6.4 Merkle Sync mapping (adapt / likely-simplify)

Delta indexes are a clever way to avoid resending a whole index on every connect,
but they are a *substitute for the thing Merkle trees give us for free*. With a
Merkle tree, an O(log n) root/subtree-hash diff already tells two peers *exactly
which subtrees differ* without any per-change sequence bookkeeping or `index_id`
lifecycle. **Recommendation (owner: merkle-researcher + protocol-researcher):**
rely on the Merkle root/subtree diff as the primary "what changed" mechanism;
optionally keep a single "last-synced root hash per peer" so a reconnect where
roots match skips the index entirely. Treat full Syncthing sequence/`index_id`
delta machinery as **deferred / out of scope** — this is a concrete entry for the
synthesizer's "what we deliberately do NOT build vs Syncthing" list. Cross-ref
SR-5 (equal-root convergence oracle) and `merkle-tree` literature finding.

---

## 7. How blocks are Requested / Responded (the transfer)

### 7.1 BlockInfo (EXACT)

```proto
message BlockInfo {                               // bep.proto:153-158
  bytes hash   = 3;     // SHA-256 of the block bytes
  int64 offset = 1;
  int32 size   = 2;
  reserved 4;           // was weak_hash (rolling adler32) — REMOVED (see §10.2)
}
```

Go mirror: `type BlockInfo struct { Hash []byte; Offset int64; Size int }`
(`bep_fileinfo.go:607-611`). Note: **no weak hash in the modern struct** — the
rolling-hash field is `reserved`.

### 7.2 Request / Response (EXACT)

```proto
message Request {                                 // bep.proto:207-217
  int32  id             = 1;    // correlates Response to Request
  string folder         = 2;
  string name           = 3;
  int64  offset         = 4;
  int32  size           = 5;
  bytes  hash           = 6;    // expected SHA-256 of the requested block
  bool   from_temporary = 7;    // serve from the peer's .syncthing.*.tmp if present
  int32  block_no       = 9;
  reserved 8;                    // was weak_hash
}
message Response {                                // bep.proto:221-225
  int32     id   = 1;
  bytes     data = 2;
  ErrorCode code = 3;
}
enum ErrorCode {                                  // bep.proto:227-232
  ERROR_CODE_NO_ERROR     = 0;
  ERROR_CODE_GENERIC      = 1;
  ERROR_CODE_NO_SUCH_FILE = 2;
  ERROR_CODE_INVALID_FILE = 3;
}
```

The pull loop (from [Understanding Synchronization](https://docs.syncthing.net/users/syncing.html),
accessed 2026-06-28):

- "The block lists are compared to build a list of needed blocks, which are then
  requested from the network or copied locally."
- "It tries to find a source for each block that differs. This might be locally,
  if another file already has a block with the same hash, or it may be from
  another device" — i.e. **content-addressed local reuse** before any network
  request (this is BEP's whole "delta": dedup by block hash, not byte-level
  rolling delta).
- "When a block is copied or received from another device, its SHA256 hash is
  computed and compared with the expected value. If it matches the block is
  written to a temporary copy of the file."
- Temp file naming: "`.syncthing.original-filename.ext.tmp` or, on Windows,
  `~syncthing~original-filename.ext.tmp`"; "all changes are made to a temporary
  copy which is then moved in place." → exactly SR-1/SR-2 (temp + atomic rename).
- On error "the temporary file is kept around for up to a day. This is to avoid
  needlessly requesting data over the network."

### 7.3 Block-size selection (EXACT formula)

```go
const ( MinBlockSize = 128 << KiB; MaxBlockSize = 16 << MiB; DesiredPerFileBlocks = 2000 )
// BlockSizes = {128KiB, 256KiB, …, 16MiB}  (powers of two)  bep_fileinfo.go:92-103
func BlockSize(fileSize int64) int {              // bep_fileinfo.go:400-409
  var blockSize int
  for _, blockSize = range BlockSizes {
    if fileSize < DesiredPerFileBlocks*int64(blockSize) { break }
  }
  return blockSize
}
```

In words (confirmed by [BEP v1 spec](https://docs.syncthing.net/specs/bep-v1.html)):
"the smallest block size that results in fewer than 2000 blocks, or the maximum
block size for larger files." Block size is constant within a file (last block may
be smaller) but varies between files. This caps per-file block count near 2000,
bounding index size.

**Merkle Sync open decision (owner: merkle-researcher).** Plan/README leans toward
**fixed 32 KiB** chunks for simplicity; Syncthing uses **adaptive 128 KiB–16 MiB**
to keep block lists small for big files. Tradeoff: fixed 32 KiB is trivial to
implement and reason about but produces ~512× more `BlockInfo`s for a 16 MiB file
(huge indexes for large files); adaptive matches Syncthing's index-size discipline
at the cost of a size→blocksize function and a varying last block. This is a real
≥3-option decision (fixed-32KiB / adaptive-power-of-two / content-defined-chunking)
that `merkle-researcher` must enumerate, score, and **log** per the autonomy
contract. Cross-ref the `cdc-chunking` and `rsync-algorithm` literature findings.

### 7.4 BlocksHash — a one-level content digest of the block list

```go
func BlocksHash(bs []BlockInfo) []byte {          // bep_fileinfo.go:653-659
  h := sha256.New()
  for _, b := range bs { h.Write(b.Hash) }        // hash of concatenated block hashes
  return h.Sum(nil)
}
```

`blocks_hash` (FileInfo field 18) is a SHA-256 over the file's block-hash list — a
cheap "do two files have identical content?" check without comparing every block.
`BlocksEqual` short-circuits on it (`bep_fileinfo.go:564-574`). `previous_blocks_hash`
(field 20) records the `blocks_hash` of the version this edit was based on, enabling
the content-causality fast-forward in `InConflictWith` (§4.6). For Merkle Sync this
is essentially a *per-file mini-Merkle root*; our full Merkle tree subsumes it, but
the `previous_blocks_hash`-based "was this edit based on what I have?" check is a
useful conflict-precision idea for the merkle-researcher to consider.

### 7.5 DownloadProgress / Ping / Close (EXACT)

```proto
message DownloadProgress {                        // bep.proto:236-247
  string folder = 1;
  repeated FileDownloadProgressUpdate updates = 2; // APPEND/FORGET block_indexes + block_size
}
message Ping  {}                                  // bep.proto:256
message Close { string reason = 1; }              // bep.proto:260-262
```

`DownloadProgress` lets peers fetch blocks from each other's *in-progress temp
files* (`from_temporary` on `Request`). Useful for N-device swarms; **defer** for a
2-device LAN tool (§9).

---

## 8. Local vs global model

Definitions, verbatim from
[Understanding Synchronization](https://docs.syncthing.net/users/syncing.html)
(accessed 2026-06-28):

- "Syncthing keeps track of several versions of each file - the version that it
  currently has on disk, called the **local** version, the versions announced by
  all other connected devices, and the 'best' (usually the most recent) version of
  the file."
- "This version is called the **global** version and is the one that each device
  strives to be up to date with."
- "When new index data is received from other devices Syncthing recalculates which
  version for each file should be the global version, and compares this to the
  current local version."
- Storage: "kept in the **index database** … called `index-*`."

The model is implemented with the host-local `local_flags` bits
(`bep_fileinfo.go:26-44 @2775f424f228`):

```go
FlagLocalGlobal  = 1 << 4 // 16: "This is the global file version"
FlagLocalNeeded  = 1 << 5 // 32: "We need this file"
FlagLocalIgnored = 1 << 1 // 2 : matches local ignore patterns
FlagLocalMustRescan = 1 << 2 // 4: doesn't match content on disk, must re-check
FlagLocalReceiveOnly= 1 << 3 // 8: change detected on a receive-only folder
```

So "needed" = the global version differs from the local version. These flags are
**host-local** (zeroed on the wire — §3); only the derived `invalid` bit crosses
the wire.

**Merkle Sync mapping (adapt / simplify).** Syncthing's global-version DB is built
to reconcile *N* devices' announcements. For a **2-device LAN** tool the "global
version of a path" is simply `WinsConflict(local, remote)` (§4.6), and the
*set* of paths needing attention is exactly the leaves where the two Merkle trees
differ (SR-5). So Merkle Sync needs:
(a) its own tree = "local"; (b) the peer's tree (from the last INDEX) = the remote
candidate; (c) per differing leaf, a VV comparison to choose pull / push / conflict
(§4–5). We do **not** need a multi-device global-version index database, the
`index_id` lifecycle, or the `Needed`/`Global` flag bookkeeping; the Merkle diff +
VV comparison is the leaner equivalent. Cross-ref SR-5 (equal-root convergence),
SR-6 (broadcast only after a confirmed *local* change — never on apply), SR-8
(a received file is not a local change until the tree rebuilds).

---

## 9. Adopt / Adapt / Reject — the concrete map to Merkle Sync

| BEP element | Decision | Rationale (correctness / concurrency / testability / cross-platform) | Owner / cross-ref |
|---|---|---|---|
| `FileInfo` metadata model (name, type, size, mode, mtime, **version vector**, **deleted**, blocks) | **ADOPT** | already validated as the leaf shape; gives two-way sync the bare hash can't | logged: `merkle-leaf-shape.md`; SR-4/7/9 |
| Version-vector algorithm (`Compare`/`Merge`/`Concurrent`, 5-valued `Ordering`, deterministic concurrent tie-break) | **ADOPT ~verbatim** | small, pure-integer, big-endian-safe → identical Mac/Windows; table-driven testable (dominates/dominated/concurrent) | merkle-researcher; SR-4 |
| `WinsConflict` (invalid-loses → newer-mtime → VV `ConcurrentGreater`) | **ADOPT** | deterministic + symmetric: both peers pick the same loser | SR-7 (already cited) |
| Conflict copy `.sync-conflict-<date>-<time>-<modifiedBy>.<ext>`, loser renamed not deleted, copies sync as normal files | **ADOPT** | literal no-data-loss contract | SR-7/SR-9 (already cited) |
| Tombstone = `deleted=true` + bumped VV (`SetDeleted`) | **ADOPT** | resurrection-resistant deletes | SR-9/SR-10 |
| Block content-addressing (SHA-256 per block) + `BlocksHash` digest | **ADOPT** | dedup + cheap block-list equality; complements Merkle tree | merkle-researcher |
| Verify-hash → temp file → atomic rename; keep temp on error | **ADOPT** | exactly our atomic-write rule | SR-1/SR-2 |
| `Request{name,offset,size,hash}` / `Response{id,data,code}` shape | **ADOPT** (slim) | minimal, correlatable, typed errors | protocol-researcher; `message-type-codes.md` |
| Index vs IndexUpdate (full vs incremental) | **ADOPT** the split | maps to our INDEX/INDEX_UPDATE codes | `message-type-codes.md` |
| Length-prefix framing, big-endian, message-size guard | **ADOPT** (simpler 1-level header) | already logged; BEP's 2-level header (header-len+msg-len, LZ4) is more than a LAN needs | `framing-format.md`; SR-12 |
| TLS 1.3 + device-ID = SHA-256(cert) + TOFU allow-list | **ADOPT** | only serverless option giving conf+integ+per-device auth | `transport-security-tofu-vs-plaintext.md` |
| **Block size** (adaptive 128 KiB–16 MiB, <2000 blocks) | **ADAPT — open** | fixed 32 KiB (plan default) is simpler but bloats indexes for big files; adaptive controls index size | **merkle-researcher must log** (fixed/adaptive/CDC); §7.3 |
| **VV counter seeding** with wall-clock (`max(prev+1, unixNow)`) | **ADAPT — prefer pure logical `prev+1`** | clock-seeding re-imports wall-clock into ordering (against SR-4); pure counter is cleaner, accept db-loss tradeoff | **protocol-researcher must log**; §4.4, SR-4 |
| **Delta indexes** (`index_id`, `sequence`, `max_sequence`, `prev/last_sequence`) | **ADAPT — likely DEFER** | Merkle root/subtree diff already gives O(log n) "what changed" without sequence bookkeeping; keep only a per-peer last-synced root hash | merkle-researcher + protocol-researcher; §6.4, SR-5 |
| `previous_blocks_hash` content-causality fast-forward | **CONSIDER** | improves conflict precision beyond pure VV | merkle-researcher; §7.4 |
| Weak hash / rolling adler32 sub-block delta | **REJECT** | already `reserved` in BEP itself (perf); we content-address whole blocks | §10.2; `rsync-algorithm` finding |
| `PlatformData` (unix/windows owner + xattrs) ownership sync | **REJECT/minimal** | mode best-effort only; full ownership/xattr non-portable | XP-6 |
| `encrypted` / RECEIVE_ENCRYPTED / `encryption_password_token` | **REJECT** | untrusted-peer at-rest encryption; we have TLS in transit | — |
| Introducer / N-device cluster / `secondary` / multiple connections (`num_connections`) | **REJECT** | 2-device LAN, single conn, no introducer | — |
| LZ4 `MessageCompression` negotiation | **DEFER** | fast LAN; framing stays forward-compatible | `framing-format.md` |
| `DownloadProgress` (fetch from peers' temp files) | **DEFER** | swarm optimization; not needed for 2-peer convergence | §7.5 |
| FolderType send-only/receive-only | **REJECT** | we do send-receive only | — |

**Net:** Merkle Sync = "BEP's `FileInfo`/version-vector/conflict/tombstone/atomic-
transfer core" + "a Merkle tree as the change-detection layer in place of BEP's
sequence/index_id delta machinery" + "ruthless removal of the N-device/cluster/
encryption/ownership/compression surface."

---

## 10. Failure modes (BEP-specific, cited)

1. **Wall-clock-coupled version vectors (§4.4).** Because `Update` seeds counters
   with Unix seconds, a device with a far-future clock mints counter values that
   dominate honest `+1` increments, skewing `Compare` toward that device for a long
   time. Mitigation in Syncthing: `WinsConflict` still consults mtime first and the
   concurrency detection stays correct; but skew is real. For us → use a pure
   logical counter (§4.4). Source: `vector.go:122-149 @2775f424f228`; clock-skew is
   a recognized sync hazard (SR-4 and the antipatterns track).
2. **Weak hash removed → no byte-level rolling delta.** The `weak_hash` field is
   `reserved` in both `BlockInfo` (bep.proto:157) and `Request` (bep.proto:216);
   rolling-adler32 sub-block reuse was dropped largely for performance
   ([Weak hashing performance vs network speed](https://forum.syncthing.net/t/weak-hashing-performance-vs-network-speed/20067),
   accessed 2026-06-28). Implication: an inserted byte that shifts every fixed
   block boundary causes a *whole-file* re-transfer in a fixed-block scheme — the
   "insert one byte shifts every boundary" problem the `cdc-chunking` finding owns.
3. **Delta-index desync on changing ignore patterns.** Because delta indexes assume
   a monotonic append keyed by `{index_id, sequence}`, changing ignore patterns can
   desynchronize them
   ([issue #3457](https://github.com/syncthing/syncthing/issues/3457), accessed
   2026-06-28). A reason to prefer the Merkle-diff approach (§6.4), which has no
   sequence-append assumption.
4. **Global vs local state divergence.** Multiple reports of computed global state
   not matching local state, especially around invalid/ignored files
   ([issue #7649](https://github.com/syncthing/syncthing/issues/7649);
   [PR #4460](https://github.com/syncthing/syncthing/pull/4460), accessed
   2026-06-28). Lesson: the "what is the global/newest version?" computation is a
   real correctness surface; our 2-device `WinsConflict` reduction (§8) shrinks it.
5. **Tombstone resurrection if GC'd too early.** A stale offline peer reconnecting
   with a pre-delete version will *resurrect* the file unless the tombstone's
   version vector still dominates it. BEP keeps deleted `FileInfo`s with bumped
   versions (§4.5); premature tombstone GC breaks this. Exactly SR-10.
6. **Filenames are raw bytes — BEP does NOT normalize.** `FileInfo.name` is a proto
   `string` sent as-is; no Unicode/case normalization in the protocol. A macOS NFD
   name and a Windows/Linux NFC name for the "same" file are two different keys →
   two leaves → non-convergence. Normalization is the application's job. For us this
   is mandatory pre-hash canonicalization (SR-13, XP-1, XP-2). The "bag of bytes"
   problem is documented in the crossplatform-rules citations.
7. **Index memory/transfer growth.** Every index carries a `BlockInfo` per block
   (≈40 bytes: 32-byte SHA-256 + offset + size). A folder with many large files or
   a too-small block size produces large indexes
   ([128 KiB block size choice](https://forum.syncthing.net/t/128-kib-block-size-choice/3128),
   accessed 2026-06-28). Mitigated by adaptive block size (§7.3), `BlocksHash`
   dedup, and delta indexes. Reinforces the fixed-32KiB-vs-adaptive decision (§7.3).
8. **First-connection TOFU MitM.** Device-ID pinning is only as strong as the
   out-of-band ID exchange; first contact is the weak point. Already analysed and
   mitigated (out-of-band allow-list) in `transport-security-tofu-vs-plaintext.md`.

---

## 11. Complexity

- **Version-vector `Compare`/`Update`/`Merge`:** single parallel walk over
  ID-sorted counters → **O(d)** where `d` = number of distinct devices that ever
  authored the file. For a 2-device LAN, `d ≤ 2`, i.e. effectively **O(1)**
  (`vector.go:262-329`).
- **Per-file block list:** `ceil(size / block_size)` `BlockInfo`s, capped near
  `DesiredPerFileBlocks = 2000` by adaptive sizing (§7.3). Each ≈ 40 bytes.
- **Index size / on-connect transfer:** full index ≈ **O(num_files +
  total_bytes / block_size)**. Delta indexes amortize a reconnect to
  **O(changes since peer's `max_sequence`)**. The Merkle-tree alternative (§6.4)
  finds the changed set in **O(log n)** subtree comparisons when most of the tree
  is unchanged.
- **Needed-block computation per file:** compare two block lists →
  **O(blocks in file)** (or **O(1)** via `blocks_hash` when lists are identical,
  `bep_fileinfo.go:564-574`).
- **Transfer:** **O(differing blocks)** Request/Response round-trips; unchanged
  blocks already present locally are copied, never re-fetched (content-addressed
  reuse, §7.2).

---

## 12. Sources

Specification / docs (accessed 2026-06-28):
- Block Exchange Protocol v1 — https://docs.syncthing.net/specs/bep-v1.html
- Understanding Synchronization — https://docs.syncthing.net/users/syncing.html
- `lib/protocol` API docs — https://pkg.go.dev/github.com/syncthing/syncthing/lib/protocol
- BEP v1 manpage (mirror) — https://manpages.ubuntu.com/manpages/bionic/man7/syncthing-bep.7.html
- Version vector (background) — https://en.wikipedia.org/wiki/Version_vector

Real source — `syncthing/syncthing` @ commit `2775f424f228` (accessed 2026-06-28):
- `proto/bep/bep.proto` — message/field layouts (§2,§3,§6,§7)
- `lib/protocol/vector.go` — `Vector`, `Counter`, `Compare`, `Update`, `Merge`,
  `Concurrent`, `Ordering` (§4)
- `lib/protocol/bep_fileinfo.go` — `FileInfo`, `WinsConflict`, `InConflictWith`,
  `SetDeleted`, `BlockSize`, `BlocksHash`, `LocalFlags` (§3,§4,§7,§8)
- `lib/protocol/protocol.go` — `MaxMessageLen`, `MinBlockSize`, `MaxBlockSize`,
  `DesiredPerFileBlocks` (§2.4,§7.3)
- `lib/protocol/doc.go` — package overview (§1)

Issues / forum (delta indexes, weak hash, block size, global/local state;
accessed 2026-06-28):
- https://github.com/syncthing/syncthing/issues/438
- https://github.com/syncthing/syncthing/issues/3457
- https://github.com/syncthing/syncthing/issues/7649
- https://github.com/syncthing/syncthing/pull/4460
- https://forum.syncthing.net/t/delta-index-implementation/7772
- https://forum.syncthing.net/t/128-kib-block-size-choice/3128
- https://forum.syncthing.net/t/weak-hashing-performance-vs-network-speed/20067

Cross-references inside this repo:
- Rules: `docs/audit/rules/sync-rules.md` (SR-1..SR-13),
  `docs/audit/rules/go-rules.md` (GR-7/GR-8/GR-12),
  `docs/audit/rules/crossplatform-rules.md` (XP-1/XP-2/XP-6).
- Decisions: `docs/audit/decisions/phase0/merkle-leaf-shape.md`,
  `framing-format.md`, `message-type-codes.md`,
  `transport-security-tofu-vs-plaintext.md`.
- Sibling literature (to be written / read together): `rsync-algorithm`,
  `merkle-tree`, `version-vectors`, `cdc-chunking`.
