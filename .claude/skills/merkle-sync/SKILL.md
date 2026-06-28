---
name: merkle-sync
description: >-
  The distilled implementable spec for the Merkle Sync engine — the Merkle diff
  algorithm, the version-vector scheme and concurrent-vs-causal rule, the binary
  framing spec with the message-type table, and the canonical path-normalisation
  rules. Use this when implementing or reviewing anything in internal/merkle,
  internal/protocol, internal/pathnorm, internal/transport, or internal/reconcile.
---

# Merkle Sync — distilled spec

Module `github.com/haider-toha/merkle-sync` (Go 1.23). Decentralised LAN file
sync, **Mac ↔ Windows**, no central server, raw TCP + UDP multicast. The Merkle
tree is the source of truth for *what differs*; version vectors decide *who is
newer*; tombstones make deletions first-class.

This is the spec, not the rationale. Rationale and evidence live in the rule
files (`docs/audit/rules/{sync,go,crossplatform}-rules.md`) and the Phase 0
decisions (`docs/audit/decisions/phase0/`). Hard-rule IDs (`SR-n`, `GR-n`,
`XP-n`) are cited inline; obey them.

---

## 1. The state model — what a leaf carries

The tree's leaf is a per-path **`FileInfo`** (decision: `merkle-leaf-shape.md`).
A bare content hash is enough for a one-way *mirror*; it is **not** enough for
two-way *sync*, which must answer who-is-newer, concurrent-vs-causal, and
deleted-vs-never-created. So:

```
FileInfo {
  path          canonical forward-slash relative string   // identity / tree position (XP-1, SR-13)
  content_hash  [32]byte  SHA-256 of the file's bytes      // transfer key + "do the bytes differ"
  size          uint64                                     // cheap mismatch reject + transfer planning
  mode          uint32                                     // perm bits / type / exec bit (lossy on Windows — XP-6)
  mtime         int64 (ns)                                 // CONFLICT TIEBREAKER ONLY — never orders edits (SR-4)
  version       VersionVector  map[DeviceID]uint64         // causal order: concurrent vs happens-before (SR-4)
  deleted       bool  (+ the version vector at delete time) // a tombstone is a versioned event, not an absence (SR-9)
}
```

`content_hash` is pure file bytes, independent of metadata, so it doubles as the
transfer/dedup key. Directory nodes derive their hash from their children (next
section).

### What the structural (tree) hash commits to

This split is the load-bearing subtlety that makes "converged ⇔ equal root hash"
true (SR-5):

- **Included** in a node's structural hash: child `name` (canonical), the child's
  `content_hash`, `mode`, `deleted` flag, and `version` vector.
- **Excluded**: raw `mtime` and `size`. `mtime` differs across machines without a
  real change (hashing it manufactures spurious whole-tree diffs); `size` is
  redundant with `content_hash`. (SR-5, XP-6)

Consequence: two fully-converged peers hold identical `FileInfo` sets (identical
version vectors and tombstones) ⇒ **bit-identical root hashes**. Including the
version vector in the structural hash is what makes equal-root hold even for files
whose bytes match but whose history differed, and for tombstones (whose bytes are
absent).

---

## 2. The Merkle tree and the diff algorithm

### Tree construction

- Files are **leaves**; directories are **internal nodes**.
- Leaf structural hash = `SHA-256(canonical(name, content_hash, mode, deleted, version))`.
- Directory node hash = `SHA-256` over the **canonically serialised, name-sorted**
  list of `(childName, childStructuralHash)` pairs. Sort by the canonical
  forward-slash name so the hash is order-independent of directory-read order.
- Root hash = the top directory node's hash. Any leaf change flips that leaf's
  hash and every ancestor hash up to the root (the git-tree / Merkle property).

Serialisation for hashing **must be byte-deterministic and identical on Mac and
Windows**: forward-slash paths (XP-1, GR-12), fixed integer widths, big-endian,
length-prefixed names, version-vector entries sorted by `DeviceID`. (Exact
on-disk/wire byte layout is hardened by merkle-researcher in Phase 2; these
constraints are fixed now.)

### The diff (reconciliation) algorithm — prune equal, recurse mismatching

Given local tree `L` and a peer's tree `R`, compute the set of differing paths
in ~`O(d + k)` (d = depth, k = differing leaves), **not** `O(n)`:

```
diff(L_node, R_node):
    if L_node.hash == R_node.hash:
        return            # subtree identical on both sides — PRUNE, do not recurse
    if both are files (leaves):
        emit (path, L_leaf, R_leaf)        # bytes/metadata differ — resolve via §3
        return
    # both are directories whose hashes differ → recurse only where children differ
    for name in union(L_node.children, R_node.children):
        lc = L_node.children[name]   # may be nil
        rc = R_node.children[name]   # may be nil
        if lc != nil and rc != nil:
            diff(lc, rc)             # recurses; the hash check at the top prunes equal subtrees
        elif lc != nil:             # only local has it
            emit (name, lc, nil)     # candidate to SEND, OR a remote deletion → tombstone check (§4)
        else:                        # only remote has it
            emit (nil, rc)           # candidate to FETCH, OR a local deletion → tombstone check (§4)
```

Key properties to preserve and test (SR-5):

- **Equal subtrees are pruned by the top-of-call hash compare** — never walked.
  This is the whole point; a one-byte change must touch exactly one leaf's branch
  and the root, nothing else.
- A child present on only one side is **not** automatically a create/delete: cross
  it against the version vectors and tombstones (§3, §4) before acting. Absence is
  ambiguous (SR-9).
- The diff is read-only over the tree: take `RLock`, snapshot the subtree you
  need, release, then act. **Zero I/O under the lock** (GR-5).

---

## 3. Version vectors — the chosen scheme and the concurrent-vs-causal rule

**Scheme:** a per-file **version vector** — `map[DeviceID]uint64`, one counter per
device that has authored a change to that path. Chosen over (a) bare hash — no
ordering; (b) hash+mtime — wall clocks skew across laptops, a classic data-loss
source; (c) full per-file CRDT op-log — correct but overkill for LAN sync
(decision: `merkle-leaf-shape.md`, Option C). Version vectors "allow the
participants to determine if one update preceded another (happened-before),
followed it, or if the two updates happened concurrently" without a shared clock.

### Operations

- **`Bump(self DeviceID)`** — `vv[self]++`. Done **only** on a confirmed *local*
  authorship event (a settled local edit, or a rescan delta whose new
  `content_hash` differs from the recorded one). **Never** on applying a received
  file. This is the load-bearing half of the no-sync-loop invariant (SR-6, SR-8).
- **`Merge(a, b)`** — per-device max: `out[d] = max(a[d], b[d])` over all devices.
  Used when accepting an update so the local vector reflects the received history.
- **`Compare(a, b) → {Equal, Dominates, DominatedBy, Concurrent}`** — the
  decision procedure. Treat a missing entry as `0`:

```
aGreater = exists d : a[d] > b[d]
bGreater = exists d : b[d] > a[d]

!aGreater && !bGreater  → Equal         # same history
 aGreater && !bGreater  → Dominates     # a is causally newer (happened-after b)
!aGreater &&  bGreater  → DominatedBy   # b is causally newer
 aGreater &&  bGreater  → Concurrent    # neither dominates → CONFLICT
```

### Causal vs concurrent → what the engine does

- **Causal** (`Dominates` / `DominatedBy`): the dominating side wins outright. The
  dominated side applies the newer file (or tombstone). **No conflict copy.** A
  stale peer reconnecting with an old version is `DominatedBy` the tombstone, so
  it deletes locally and does **not** resurrect the file (SR-10).
- **Equal**: same history — if `content_hash` also matches, the apply is a no-op
  (idempotent, content-addressed — SR-3). Nothing to do.
- **Concurrent** (neither dominates) **and contents differ → CONFLICT** (SR-7):
  keep **both**, lose nothing.
  - Winner stays at `path`. Loser is renamed to
    `<name>.sync-conflict-<UTC-date>-<UTC-time>-<DeviceID>.<ext>` and then syncs
    as a normal file.
  - **Tiebreaker (deterministic so both peers pick the same loser):** the file
    with the **older `mtime` loses**; if mtimes are equal, the file from the
    device with the **larger DeviceID value loses**. (This is the *only* use of
    `mtime` — SR-4.)
  - A modification-vs-deletion that is concurrent is the same rule: if the delete
    wins, the modified file is preserved as a conflict copy (still no data loss —
    SR-7, SR-9).

Version-vector pruning/compaction as device counts grow is bounded for a 2-device
LAN tool; the growth story is hardened by protocol-researcher in Phase 2.

---

## 4. Deletions — tombstones, never silent absence

(SR-9, SR-10.) A local delete produces a **tombstone**: the `FileInfo` is kept
with `deleted=true` and the deleting device's version-vector counter bumped (a
delete is a *versioned event*). It propagates like any other update; a peer
applying it removes its local copy and keeps the tombstone.

- Never represent a delete as "the path is just gone from my index" — absence is
  ambiguous between *deleted here*, *not yet created here*, and *must be
  resurrected here*.
- **Resurrection resistance (SR-10):** a reconnecting stale peer holding the
  pre-delete file is `DominatedBy` the tombstone, so the file is deleted on the
  stale peer and **not** re-created on the deleter. Retain tombstones until both
  peers have acknowledged, then GC (retention is a logged Phase 2 sub-decision).

---

## 5. Wire framing — `[4-byte len][1-byte type][payload]`

(Decision: `framing-format.md`; rules SR-12, GR-7, GR-8.) Framing runs **inside**
the TLS 1.3 session (§7), so these bytes are encrypted on the wire.

```
+----------------------+--------------+------------------------+
| length: uint32 BE    | type: 1 byte | payload: (length-1) B  |
| = 1 + len(payload)   |              |                        |
+----------------------+--------------+------------------------+
```

- `length` counts `type byte + payload`. An empty-payload message has `length == 1`.
- **All multi-byte integers are big-endian** (`encoding/binary.BigEndian`), so
  Mac and Windows agree byte-for-byte (cross-platform; matches Syncthing BEP).
- **`MaxFrameLen = 16 MiB`.** The reader validates `0 < length <= MaxFrameLen`
  **before allocating**. Violation → `ErrFrameTooLarge` (a typed sentinel) → drop
  that peer connection. Never desync, never wedge, never allocate on a bad length.
  (SR-12)
- Read with `io.ReadFull` for **both** the 4-byte header and the body — a single
  `conn.Read` may return a partial message; treating its return as a whole
  message is a stream-desync bug. **Never** decode `gob` from the network (GR-7).
- Bulk file content is streamed as many small chunk messages (per-chunk size is a
  reconcile-workstream decision, well under the ceiling), not one giant frame.

`WriteFrame(w, type, payload)` and `ReadFrame(r) (type, payload, error)` live in
`internal/protocol/framing.go`.

### Message-type table

(Decision: `message-type-codes.md`. `type MsgType byte`. `0x00` is reserved/invalid;
`0x08`+ are unassigned — Phase 2 protocol-researcher may extend, never renumber.)

| code | type | payload (sketch — Phase 2 finalises) |
|---|---|---|
| `0x00` | *(reserved — invalid)* | never sent; receipt is a protocol error |
| `0x01` | `HELLO` | protocol version, `DeviceID`, folder id — sent once at connect |
| `0x02` | `INDEX` | full index snapshot: a set of `FileInfo` |
| `0x03` | `INDEX_UPDATE` | incremental `FileInfo` deltas since the last index |
| `0x04` | `REQUEST` | want bytes of a file: `path`, `content_hash`, `offset`, `length` |
| `0x05` | `RESPONSE` | chunk data answering a prior `REQUEST` |
| `0x06` | `PING` | keepalive / liveness — empty payload (`length == 1`) |
| `0x07` | `CLOSE` | graceful shutdown — optional reason payload |

Test obligations (SR-12, GR-8): split a valid frame across reads
(`iotest.OneByteReader`) → correct reassembly; oversized length → `ErrFrameTooLarge`
with no large allocation; type `0x00`/`0x08` → typed `ErrUnknownMsgType`;
per-type encode/decode round-trip.

---

## 6. Canonical path normalisation (Mac ↔ Windows)

(Rules XP-1..XP-6, SR-13, GR-12.) The same logical file **must** map to the same
canonical key and the same hash on both OSes, or convergence (SR-5) breaks.

1. **Canonical form = forward-slash, relative, NFC-normalised** (XP-1, XP-2).
   - Relative to the sync root; the root is the only absolute path.
   - Separator is `/`. Use the `path` package for keys; convert with
     `filepath.ToSlash` on read and `filepath.FromSlash` on write — **only** at
     the filesystem call. Never store `\` (GR-12).
   - Unicode: normalise to **NFC** (Composed) at the boundary — at scan time and
     when receiving from a peer (`golang.org/x/text/unicode/norm`). macOS stores
     NFD; without one canonical form, `résumé.pdf` is two different leaves and
     never converges. Keep the on-disk byte form separately if needed to re-open
     the file on macOS.
2. **Windows-unsafe names: escape (reversibly) or refuse + flag — never write
   verbatim** (XP-3). A name is unsafe if it contains a reserved char
   (`< > : " / \ | ? *`), a control char (1–31 or NUL), is a reserved device name
   (`CON PRN AUX NUL COM1..9 COM¹²³ LPT1..9 LPT¹²³`, including with any extension —
   `NUL.txt` ≡ `NUL`), or ends in a space or a period. The **canonical tree key
   keeps the original name**; only the on-disk Windows form is escaped. Mind
   `MAX_PATH` (260) and the `\\?\` long-path prefix.
3. **Case-insensitivity collisions: refuse + flag, never clobber** (XP-4). Two
   keys differing **only** by case (`File.txt` vs `file.txt`) cannot coexist on a
   case-insensitive target (Windows; macOS default). On collision while applying,
   do **not** overwrite — refuse the second write, flag it, keep both in the tree
   (Syncthing's posture). Detect via a case-folded collision index.
4. **`mode` / `mtime` are not portable** (XP-6). Keep them in `FileInfo` but
   exclude raw `mtime` and `size` from the structural hash (§1). Exec bit and
   symlink mapping on Windows is documented and lossy.

Test obligation (SR-13): round-trip a **Windows-hostile name set**
Mac→wire→Windows→wire→Mac and assert identical canonical keys and identical
subtree hashes. Several of these (real NTFS case collisions, reserved names,
trailing dot/space) cannot be verified on the Mac — they are closed by the CI
`windows-latest` job and `docs/audit/CROSS_PLATFORM_CHECKLIST.md`.

---

## 7. Transport identity (one-paragraph reference)

(Decision: `transport-security-tofu-vs-plaintext.md`.) TLS 1.3, self-signed
per-device certs, **`DeviceID = SHA-256(cert DER)`**, trust-on-first-use against
an explicit allow-list. `tls.Config{MinVersion: VersionTLS13}` with
`InsecureSkipVerify` *plus* a custom `VerifyConnection` that pins the peer's
DeviceID against the allow-list and drops the connection on an unknown ID
(skipping the CA chain is intended — there is no CA — but the fingerprint check is
mandatory; skipping both would be plaintext in disguise). DeviceIDs compared as
raw 32-byte values; the human encoding is presentation-only. Discovery (UDP
multicast) is unauthenticated — a hint, never authorisation; authentication
happens at the TLS layer. Stdlib only (`crypto/tls`, `crypto/x509`).

---

## 8. Invariant quick-reference (cite these IDs)

| # | Invariant | Rule |
|---|---|---|
| 1 | Atomic write: temp → fsync → rename → fsync dir | SR-1, SR-2 |
| 2 | Idempotent, content-addressed apply | SR-3 |
| 3 | Version vectors order edits; mtime only breaks ties | SR-4, SR-7 |
| 4 | Convergence ⇔ identical root hash | SR-5 |
| 5 | Bump VV + broadcast only on a *local* change | SR-6, SR-8 |
| 6 | Conflict loser renamed, never deleted | SR-7 |
| 7 | Deletions are tombstones; no resurrection | SR-9, SR-10 |
| 8 | Watcher events are hints; rescan is truth; debounce ~150 ms | SR-11, GR-9, GR-10 |
| 9 | Framing max-length guard; `io.ReadFull`; no `gob` from net | SR-12, GR-7, GR-8 |
| 10 | Canonical forward-slash NFC identity | SR-13, XP-1, XP-2, GR-12 |
| 11 | One `RWMutex`, zero I/O under the lock; no goroutine leaks | GR-3, GR-5 |
