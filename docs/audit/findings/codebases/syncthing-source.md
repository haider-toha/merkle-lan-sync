# Codebase map: Syncthing source (Go)

- Source: `github.com/syncthing/syncthing`, **tag `v2.1.1`** (latest stable,
  published 2026-06-02; annotated-tag object
  `e105e5b6aa9f4e625e379b4a449c02db9c78dde3`).
- Method: downloaded the v2.1.1 source tarball
  (`https://codeload.github.com/syncthing/syncthing/tar.gz/refs/tags/v2.1.1`) and
  read files locally, so every `file:line` below is verified, not summarized.
  Accessed 2026-06-28. Version-selection rationale:
  `docs/audit/decisions/phase1/syncthing-version-to-map.md`.
- Blob URL form (pinned to tag):
  `https://github.com/syncthing/syncthing/blob/v2.1.1/<path>#Lx-Ly`.
- Read-first input honored: `docs/audit/rules/go-rules.md` — patterns below are
  judged against GR-2/3/4/5/7/8/12 and the project's stdlib-first stance.
- Target module placements reference `docs/audit/plan/structure.md`.

> **v1 → v2 note.** Everything Merkle Sync borrows (DeviceID derivation, version
> vectors, conflict-copy policy, atomic write, block hashing, case/Unicode
> handling) is semantically unchanged from the classic v1 line the Phase 0
> decisions cite. The one big v2 change is the **database**: the old LevelDB
> key/value store was rewritten to **SQLite** (`internal/db/sqlite/*`), with the
> legacy store kept only for migration at
> `internal/db/olddb/backend/leveldb_backend.go`. `lib/db` no longer exists.

---

## 1. Package structure (relevant subset)

| Syncthing package | role | key files (v2.1.1) |
|---|---|---|
| `lib/protocol` | BEP wire protocol, `FileInfo`, `DeviceID`, version `Vector`, framing, per-OS native-path conversion | `protocol.go`, `bep_fileinfo.go`, `deviceid.go`, `vector.go`, `wireformat.go`, `nativemodel_{darwin,unix,windows}.go`, `bep_*.go` |
| `internal/db` | the "last-known-state" database — a persistent **per-device** index per folder | `interface.go`, `sqlite/folderdb_{update,global,local}.go`, `olddb/` (legacy LevelDB, migration only) |
| `lib/model` | folder pullers; turns index diffs into disk operations; `.sync-conflict` creation; atomic finish | `folder.go`, `folder_sendrecv.go` |
| `lib/scanner` | walk the tree, block-hash files, build `FileInfo`s | `walk.go`, `blocks.go` |
| `lib/fs` | filesystem abstraction; case-conflict detection; temp naming | `casefs.go`, `tempname.go`, `basicfs*.go` |

### 1a. `lib/protocol` — identity, version vector, FileInfo, framing

- **`DeviceID`** is `[32]byte` (`deviceid.go:27`). It is derived as the SHA-256
  of the raw certificate bytes: `NewDeviceID(rawCert) = sha256.Sum256(rawCert)`
  (`deviceid.go:44-48`). Human form is base32 + Luhn check digits + dash
  chunking, with typo de-confusion (`0→O,1→I,8→B`) (`deviceid.go:70-83`,
  `123-153`, `178-239`). `Short()` returns **bits 0-63 as a `uint64`**
  (`deviceid.go:106-108`) — this `ShortID` is reused as the version-vector key.
- **Version `Vector`** = `{Counters []Counter}`, `Counter{ID ShortID, Value
  uint64}` (`vector.go:24-26`, `100-103`). Counters are kept **sorted by ID**;
  `Update` inserts in order (`vector.go:122-149`), `Merge` is a sorted
  element-wise max (`vector.go:154-184`), and `Compare` is a sorted merge-walk
  (`vector.go:263-329`). `Update` is a **hybrid logical clock**:
  `value = max(counter+1, unixSeconds)` (`vector.go:127-149`, esp. `131`), so a
  counter never goes backwards and roughly tracks wall time. `Compare` returns
  `Ordering ∈ {Equal, Greater, Lesser, ConcurrentLesser, ConcurrentGreater}`
  (`vector.go:245-329`); `Concurrent()` is either concurrent variant
  (`vector.go:212-216`). `VectorHash` gives a deterministic hash of a vector
  (`bep_fileinfo.go:671-682`).
- **`FileInfo`** (the leaf) — `bep_fileinfo.go:115-150`. Fields:
  `Name` (canonical), `Size`, `ModifiedS int64`+`ModifiedNs int32` (mtime),
  `ModifiedBy ShortID` (last author — the conflict tiebreaker), `Version Vector`,
  `Sequence int64` (per-device monotonic index cursor), `Blocks []BlockInfo`,
  `BlocksHash []byte` (content identity), `PreviousBlocksHash []byte` (the
  content this edit was based on), `Type`, `Permissions uint32`, `RawBlockSize`,
  `Deleted bool` (tombstone), and `LocalFlags` (host-local, **explicitly not
  sent over the wire** — comment at `bep_fileinfo.go:134-140`). This is almost
  exactly the `hash+size+mode+mtime+VV+tombstone` leaf already chosen in
  `docs/audit/decisions/phase0/merkle-leaf-shape.md`.
- **Block hashing identity.** `BlockInfo{Hash, Offset, Size}`
  (`bep_fileinfo.go:617-621`); `BlocksHash(bs)` = SHA-256 over the concatenated
  block hashes (`bep_fileinfo.go:663-668`). This is a **flat, one-level** hash
  over a file's blocks — Syncthing does *not* build a recursive folder Merkle
  tree (see §3).
- **Block sizing is variable, powers of two.** `BlockSizes` is built at init from
  `MinBlockSize=128 KiB` to `MaxBlockSize=16 MiB`, doubling each step
  (`bep_fileinfo.go:93-98`; consts `protocol.go:46-50`). `BlockSize(fileSize)`
  picks the smallest size giving fewer than `DesiredPerFileBlocks=2000` blocks
  (`bep_fileinfo.go:410-419`; `protocol.go:56-57`).
- **Framing** (`protocol.go`). Each message on the wire is
  `[2-byte BE header-len][protobuf Header{type,compression}][4-byte BE
  message-len][protobuf message, optionally LZ4]` — write path
  `writeMessage` `protocol.go:771-823`, read path `readMessage`/`readHeader`
  `protocol.go:506-598`. The length guard is the GR-8 pattern: read the 4-byte
  length, **reject `<0` or `> MaxMessageLen` (500 MB) *before* allocating**, then
  `io.ReadFull` (`protocol.go:518-535`; const `protocol.go:43-44`). There are
  **8 message types** — ClusterConfig, Index, IndexUpdate, Request, Response,
  DownloadProgress, Ping, Close (`typeOf`/`newMessage` `protocol.go:873-917`;
  `Model` interface `protocol.go:82-95`). Unknown message types are **skipped**
  for forward-compatibility (`protocol.go:412-415`).
- **Trust-boundary validation.** `checkFilename` rejects non-canonical names
  (`path.Clean(name) != name`), empty/`.`/`..`, absolute (`/…`) and traversal
  (`../…`) names, and "a filename failing this test is grounds for disconnecting
  the device" (`protocol.go:646-670`). `checkFileInfoConsistency` enforces
  invariants on received indexes: deleted files and directories must have no
  blocks; non-deleted regular files must have ≥1 block (`protocol.go:622-641`).
- **Canonical wire form + per-OS conversion (decorator chain).**
  `NewConnection` stacks decorators: `wireFormatConnection` (outermost) →
  encryption → `rawConnection`, plus a `makeNative` model wrapper
  (`protocol.go:220-240`). Outgoing names are forced to **NFC + forward-slash**
  in one place (`wireformat.go:20-39`). Incoming names are converted to the OS
  native form at the edge: macOS → **NFD** (`nativemodel_darwin.go:22-39`);
  Windows → backslashes via `filepath.FromSlash`, **dropping** any index entry or
  request whose name contains a literal `\` (`nativemodel_windows.go:38-84`).
  Linux/unix is a no-op passthrough (`nativemodel_unix.go`).

### 1b. `lib/protocol` — connection lifecycle (concurrency)

`rawConnection.Start()` spawns exactly **five goroutines** — `readerLoop`,
`dispatcherLoop`, `writerLoop`, `pingSender`, `pingReceiver` — and tracks them
with a `loopWG sync.WaitGroup` whose comment is literally "Need to ensure no
leftover routines in testing" (`protocol.go:193`, `272-311`). The shape is the
GR-4 fan-in: `readerLoop` reads frames and pushes to an `inbox` channel
(`protocol.go:407-425`); `dispatcherLoop` is the single consumer that mutates
state (`protocol.go:427-504`); `writerLoop` drains `outbox`/`closeBox`/
`clusterConfigBox` (`protocol.go:716-769`). Request/response correlation uses an
`awaiting map[int]chan asyncResult` under `awaitingMut` (`protocol.go:176-178`,
`357-384`, `693-701`); on close, **every waiting channel is drained and closed**
so no caller leaks (`internalClose` `protocol.go:965-996`, esp. `975-982`).
Keepalive: `pingSender` (90 s) + `pingReceiver` that closes the conn after a
300 s `ReceiveTimeout` (`protocol.go:209-212`, `1003-1047`).

### 1c. `internal/db` — the last-known-state database

The `DB` interface (`internal/db/interface.go:46-109`) is keyed by
**`(folder, device)`**: Syncthing persists a separate `FileInfo` index for the
local device *and for every remote device*. From those it derives the **global**
(winning) file and the **need** set on demand:
- `Update(folder, device, []FileInfo, …)` applies an index batch
  (`interface.go:52`).
- `GetGlobalFile(folder, file)` returns the winner across devices
  (`interface.go:58`); `GetDeviceFile` returns one device's last-known copy
  (`interface.go:56`).
- `AllNeededGlobalFiles(folder, device, …)` *is* the diff — the globals a device
  is missing (`interface.go:73`).
- Index exchange is **delta-based via per-device monotonic `Sequence` numbers**:
  `GetDeviceSequence`, `AllLocalFilesBySequence`, `RemoteSequences`
  (`interface.go:70`, `87`, `90`) — INDEX_UPDATE carries only files newer than
  the last sequence the peer acknowledged.
- A lightweight `FileMetadata` projection (no blocks) is used for iteration
  (`interface.go:132-160`).

### 1d. `lib/scanner` — hashing and FileInfo production

`Blocks()` block-hashes a reader with SHA-256, reusing a **32 KiB copy buffer**
and the hasher from `sync.Pool`s, and emitting `BlockInfo{Size,Offset,Hash}` per
block (`blocks.go:26-121`); an empty file yields a single zero-length block with
the precomputed `SHA256OfNothing` (`blocks.go:20`, `111-118`). `Validate` re-hashes
to verify a received block (`blocks.go:124-131`). The walker computes the file's
block size (`protocol.BlockSize(size)`) with **hysteresis** — it keeps the
existing block size if the new one is within 2× — to avoid re-hashing when a file
just crosses a size threshold (`walk.go:433-458`). `CreateFileInfo` fills
`Permissions = Mode & ModePerm` and the type (`walk.go:733-754`). Crucially, the
**version vector is bumped only when a local change is detected**, in
`updateFileInfo`: `dst.Version = src.Version.Update(w.ShortID); dst.ModifiedBy =
w.ShortID` (`walk.go:649-657`). A scan-detected deletion stamps a tombstone the
same way via `SetDeleted(shortID)` (`lib/model/folder.go:834`, `963`).

### 1e. `lib/model` + `lib/fs` — atomic write, conflict copies, case conflicts

- **Atomic write.** Pulled content is written to a temp file
  `fs.TempName(name)` (`folder_sendrecv.go:1130`), `fd.Sync()`'d
  (`folder_sendrecv.go:1785`), then **atomically renamed** onto the target via
  `osutil.RenameOrCopy(tempName, file.Name)` — "If it didn't work, leave the temp
  file in place for reuse" (`performFinish` `folder_sendrecv.go:1658-1710`, rename
  at `1700`). The DB index is updated **only after** the rename succeeds
  (`folder_sendrecv.go:1708`). Temp names are prefixed/hidden (`~syncthing~` on
  Windows, `.syncthing.` on unix) with a max-filename guard
  (`lib/fs/tempname.go:19-20`, `34`, `39-42`).
- **Conflict copies.** On finishing a pull where the incoming file
  `InConflictWith` the current on-disk file (`folder_sendrecv.go:1679`),
  `moveForConflict` **renames the loser** to a conflict copy — it never deletes
  it (`folder_sendrecv.go:1863-1906`, `Rename` at `1881`) — then feeds the new
  name back into the scan channel so the conflict copy itself gets indexed and
  synced (`1902-1904`). The name format is
  `name.sync-conflict-YYYYMMDD-HHMMSS-<lastModBy>.ext`
  (`conflictName` `folder_sendrecv.go:2220-2223`), where `lastModBy =
  file.ModifiedBy.String()` (`folder_sendrecv.go:1686`). Old copies beyond
  `MaxConflicts` are pruned (`folder_sendrecv.go:1889-1901`).
- **Conflict winner** (`bep_fileinfo.go:210-227`): (1) if exactly one side is
  invalid, the valid one wins; (2) else newer `ModTime()` wins; (3) on an mtime
  **tie**, the version vector breaks it deterministically —
  `f.FileVersion().Compare(other.FileVersion()) == ConcurrentGreater`. (The
  `ConcurrentGreater/Lesser` split exists precisely to give a stable total order
  for ties — `vector.go:256-261`.) `InConflictWith` additionally treats an edit
  as *not* a conflict if its `PreviousBlocksHash` matches the current content,
  i.e. it descends from what we have (`bep_fileinfo.go:188-206`).
- **Case-conflict detection** (`lib/fs/casefs.go`). A `caseFilesystem` decorator
  (enabled by `OptionDetectCaseConflicts`, `casefs.go:133-154`) runs `checkCase`
  before every mutating/looking-up operation (`casefs.go:156-356`). `realCase`
  resolves the on-disk casing component-by-component via a cached
  `lowerToReal[UnicodeLowercaseNormalized(comp)]` map (`casefs.go:419-444`,
  esp. `434`); if the real name differs from the requested one (after
  normalizing **both** to NFC so a Unicode difference is not mistaken for a case
  difference), it returns `*CaseConflictError` (`casefs.go:378-395`, esp. `391`;
  type at `27-38`). The policy is **refuse + surface an error**, never clobber.

---

## 2. Patterns to ADOPT

### A2-1 — DeviceID = SHA-256(cert); reuse its high 64 bits as the version-vector key
- **Where:** `deviceid.go:44-48` (`NewDeviceID`), `deviceid.go:106-108`
  (`Short()`), `vector.go:100-103` (`Counter{ID ShortID}`).
- **What it buys us:** per-device cryptographic identity for free from the TLS
  cert (no separate ID scheme), and a **compact** version vector that keys on a
  `uint64` instead of a 32-byte device ID — smaller leaves, smaller wire index.
- **Lands in:** `internal/protocol/deviceid.go` (`DeviceIDFromCert`, `Short`) and
  `internal/protocol/versionvector.go` (key the counters on the short ID).
  Confirms and refines
  `docs/audit/decisions/phase0/transport-security-tofu-vs-plaintext.md` and the
  VV encoding handed to Phase 2 by `merkle-leaf-shape.md`. For a 2-device LAN
  tool the 64-bit truncation has no meaningful collision risk; we can document it
  and skip the Luhn/base32 human-encoding flourish (`deviceid.go:178-239`) — a
  hex/base32 string is enough with no GUI.

### A2-2 — Length-prefixed frame with a max-length guard *before* allocation
- **Where:** `protocol.go:518-535` (read length → check `<0`/`>MaxMessageLen` →
  `io.ReadFull`), `protocol.go:43-44` (`MaxMessageLen`), and `BufferPool`
  reuse around the read (`protocol.go:530-531`).
- **What it buys us:** exactly GR-8 — no stream desync on partial reads, and DoS
  resistance (a hostile peer can't make us allocate 4 GiB by lying about a
  length). The `sync.Pool` buffer reuse is a bonus for the chunk-streaming path.
- **Lands in:** `internal/protocol/framing.go` (`ReadFrame` with the
  `MaxFrameLen` guard) per
  `docs/audit/decisions/phase0/framing-format.md`. We adopt the *guard
  discipline*, not the two-level protobuf header (see D3-2).

### A2-3 — Conflict policy: invalid-loses → newer-mtime → version-vector tiebreak; loser **renamed**, never deleted
- **Where:** `bep_fileinfo.go:210-227` (`WinsConflict`),
  `folder_sendrecv.go:1863-1906` (`moveForConflict`, `Rename` at `1881`),
  `folder_sendrecv.go:2220-2223` (`conflictName`), `bep_fileinfo.go:188-206`
  (`InConflictWith`).
- **What it buys us:** deterministic convergence — both peers independently pick
  the *same* winner (the VV tiebreak is symmetric) — with **zero data loss**
  because the losing side becomes a `.sync-conflict-…` copy that is itself synced.
  This is SR-7 made concrete, and the naming matches what `structure.md` already
  planned for `internal/reconcile/conflict.go`.
- **Lands in:** `internal/reconcile/conflict.go`. Adopt the three-tier winner
  rule and the `name.sync-conflict-YYYYMMDD-HHMMSS-<deviceID>.ext` format
  verbatim. (We can skip `PreviousBlocksHash`-based "based-on" detection
  initially — see D3-1 — and treat any concurrent VV as a conflict, which is
  safe-but-slightly-more-eager.)

### A2-4 — Version vector as a hybrid logical clock with a stable total order
- **Where:** `vector.go:122-149` (`Update` = `max(counter+1, now)`),
  `vector.go:154-184` (`Merge`), `vector.go:245-329` (`Compare` →
  `{Equal,Greater,Lesser,ConcurrentLesser,ConcurrentGreater}`),
  `vector.go:212-216` (`Concurrent`).
- **What it buys us:** causal-vs-concurrent detection (the core of two-way sync)
  *and* a deterministic tiebreak for free — the `ConcurrentGreater/Lesser` split
  is what makes A2-3's mtime-tie rule converge without a separate code path.
  Keeping counters sorted makes `Merge`/`Compare` linear and the serialization
  deterministic (needed for the structural hash in `merkle-leaf-shape.md`).
- **Lands in:** `internal/protocol/versionvector.go` (`Bump`/`Update`, `Compare`,
  `Merge`). Note for the implementer: bump the counter **only on local
  authorship** (`walk.go:649-657`), never when applying a received file — this is
  SR-6/SR-8 and the anti-sync-loop invariant.

### A2-5 — Atomic write: temp → fsync → atomic rename → index-update-after-rename
- **Where:** `folder_sendrecv.go:1130` (temp name), `:1785` (`fd.Sync()`),
  `:1700` (`RenameOrCopy` temp→final), `:1708` (DB update only after rename);
  temp naming `lib/fs/tempname.go:19-20`.
- **What it buys us:** SR-1/SR-2 — a transfer killed mid-stream leaves a discarded
  temp file and never a half-written live file; the in-memory/DB state is updated
  only once the bytes are durably in place, so a crash can't desync state from
  disk. The "leave temp for reuse" behavior also enables resumable transfers.
- **Lands in:** `internal/reconcile/transfer.go`. Use a prefixed, hidden temp
  name and `os.Rename` on the same directory; add a parent-dir fsync after rename
  for full durability (structure.md already specifies "temp-write → fsync →
  atomic rename → dir fsync").

### A2-6 — Bump VV / set ModifiedBy / write tombstone only in the scanner, and isolate path normalization in edge decorators
- **Where:** `walk.go:649-657` (VV+ModifiedBy on local change),
  `bep_fileinfo.go:598-604` (`SetDeleted` bumps the VV → tombstone),
  `wireformat.go:20-39` (NFC+slash on send), `nativemodel_darwin.go:22-39` (NFD
  on receive), `nativemodel_windows.go:38-84` (slash↔backslash, drop invalid).
- **What it buys us:** the canonical form (NFC + forward-slash) is enforced in
  exactly one choke point and OS quirks live only at the edge — directly serving
  GR-12 and the crossplatform rules, and keeping the tree/wire bytes identical on
  Mac and Windows. Treating deletion as a VV-bumping event (not an absence) is
  the tombstone model (SR-9).
- **Lands in:** `internal/pathnorm` (canonicalize/native conversion) invoked at
  the `internal/transport`↔`internal/reconcile` boundary and in
  `internal/merkle/scanner.go`.

---

## 3. What Merkle Sync deliberately does DIFFERENTLY (simpler)

### D3-1 — In-memory recursive **Merkle tree + tree diff**, not a persistent multi-device index DB
- **Syncthing:** persists a full `FileInfo` index **per device** per folder in a
  database (`internal/db/interface.go:46-109`; v2 SQLite at
  `internal/db/sqlite/*`, v1 LevelDB now at `internal/db/olddb`), computes the
  global/winner and the "need" set as **queries** (`GetGlobalFile`
  `interface.go:58`, `AllNeededGlobalFiles` `interface.go:73`), and ships index
  **deltas keyed by per-device monotonic `Sequence`** (`interface.go:70,87,90`).
  Its per-file content identity is a **flat** `BlocksHash`
  (`bep_fileinfo.go:663-668`), not a folder-recursive hash.
- **Merkle Sync:** holds **one in-memory Merkle tree** guarded by a single
  `RWMutex` (GR-5; `internal/merkle`, `internal/reconcile/engine.go`), where a
  directory's hash derives from its children so a change flips exactly that leaf's
  branch and the root. Diffing is "walk both trees, recurse only into mismatching
  branches" (O(log n)) — `merkle-leaf-shape.md`.
- **Why this is safe for a 2-device LAN tool:** with two devices and one folder
  there is no need for a scalable persistent multi-device index, SQL, or
  sequence-based incremental index transfer; a full rescan on settle plus a tree
  compare is cheap, and the tree is the single source of truth.
- **What we lose / must watch:** (a) **persistence of last-synced state across
  restarts** — Syncthing reads it from the DB; we re-scan, but we still need a
  persisted snapshot of the last-synced tree to detect *deletions* after a restart
  (absence vs never-existed). This is a real gap the tombstone/rescan design must
  close (flag for `tree-critic`/Phase 2). (b) **cross-file block dedup** (the DB
  block index, `interface.go:74`). (c) incremental sequence-based index sync — we
  resend/compare the whole (small) tree instead.

### D3-2 — One length-prefixed binary frame with a **1-byte type code**, not a two-level protobuf-header frame
- **Syncthing:** `[2-byte BE hdr-len][protobuf Header{type,compression}][4-byte BE
  msg-len][protobuf message, optionally LZ4]` (`protocol.go:771-823`, `506-598`)
  over 8 protobuf message types (`protocol.go:873-917`).
- **Merkle Sync:** `[4-byte BE len][1-byte type][payload]` with `encoding/binary`
  and a hand-rolled codec — no protobuf, no self-describing header, no compression
  (`docs/audit/decisions/phase0/framing-format.md`; GR-7 forbids `gob`/untrusted
  self-describing decoders at the network boundary).
- **Why safe:** a fixed, small message catalogue on a fast LAN needs neither
  protobuf's schema-evolution machinery nor LZ4; a 1-byte enum + explicit binary
  codec is smaller, easier to fuzz, and keeps the trust boundary minimal.
- **What we lose:** wire compression; protobuf's automatic unknown-field
  tolerance (we get extensibility only by allocating new type codes — but we keep
  Syncthing's good habit of skipping unknown message types, `protocol.go:412-415`);
  and cross-language tooling.

### D3-3 — Fixed-size chunks (baseline 32 KiB) rather than variable 128 KiB–16 MiB blocks
- **Syncthing:** variable, power-of-two block sizes from 128 KiB to 16 MiB chosen
  to target ~2000 blocks/file, with re-hash hysteresis (`bep_fileinfo.go:93-98`,
  `410-419`; `walk.go:433-458`).
- **Merkle Sync:** the workstream baseline is **fixed 32 KiB** chunks
  (plan/README, `structure.md` `internal/reconcile/transfer.go`), with
  content-defined chunking explicitly deferred to the merkle-researcher decision
  in Phase 2.
- **Why safe:** fixed chunks are trivially correct and testable; at LAN speed the
  bandwidth penalty for small files is irrelevant. (Note: Syncthing's *scanner*
  already uses a 32 KiB copy buffer internally — `blocks.go:26` — so 32 KiB is a
  familiar unit.)
- **What we lose:** for very large files, far more chunk metadata than
  Syncthing's adaptive scheme, and no insertion-resilient boundaries (the
  one-byte-insert-shifts-every-boundary problem CDC solves) — both acceptable for
  the baseline and revisitable in Phase 2.

### D3-4 — LAN-only UDP multicast discovery, no global discovery server / relays / GUI
- **Syncthing:** ships global discovery servers, relay infrastructure, and a web
  GUI + REST API (the latter even referenced in `deviceid.go:22`'s comment about
  `gui/default/syncthing/app.js`).
- **Merkle Sync:** multicast-only peer discovery (`internal/discovery`, GR-4),
  authentication strictly at the TLS layer so discovery is only a *hint*
  (`transport-security-tofu-vs-plaintext.md`), and a **headless daemon**
  (`cmd/msync`) with no GUI/REST.
- **Why safe:** both devices are on the same LAN by assumption; pairing happens
  out-of-band via device IDs.
- **What we lose:** cross-subnet/internet sync, NAT traversal, and any GUI/observability
  — all explicitly on the Phase 4 deferral list (plan/agent_roster.md).

---

## 4. Cross-references

- Confirms/refines Phase 0 decisions: `merkle-leaf-shape.md` (FileInfo fields,
  VV-in-structural-hash, mtime→deviceID tiebreak), `framing-format.md` (length
  guard adopted, protobuf header rejected), `transport-security-tofu-vs-plaintext.md`
  (DeviceID = SHA-256(cert)).
- Honors `go-rules.md`: GR-3/GR-4 (the 5-goroutine + `loopWG` connection model is
  a template for `internal/transport/conn.go`), GR-8 (length guard), GR-12
  (canonical forward-slash + edge-only native conversion), GR-7 (we diverge from
  protobuf-on-the-wire by design).
- Hands to Phase 2: `merkle-researcher` (fixed-32 KiB vs CDC — D3-3; flat
  `BlocksHash` vs recursive folder hash — D3-1); `protocol-researcher` (VV
  encoding/pruning, the 8→~7 message catalogue, `PreviousBlocksHash`-style
  "based-on" conflict refinement — A2-3/D3-1); `tree-critic` (persisted
  last-synced state for deletion detection across restart — D3-1 gap).
