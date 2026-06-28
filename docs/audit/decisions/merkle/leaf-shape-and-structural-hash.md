# Decision: Merkle leaf shape (two-way-sync metadata) + the exact structural-hash recipe

- Area: merkle (Phase 2 — merkle-researcher hardening of the Phase 0 baseline)
- Status: decided
- Date: 2026-06-28
- Decider: merkle-researcher (Phase 2)
- Ratifies / supersedes-in-detail: `docs/audit/decisions/phase0/merkle-leaf-shape.md`
  (Option C field set), whose "Consequences" explicitly deferred **the exact
  canonical serialization** and the **leaf-vs-node hashing recipe** to this agent.
- Closes open question: **OQ-4** (synthesis problem-space map §4 — "exact canonical
  FileInfo/VV serialization for hashing + RFC-6962 domain separation"). Other
  in-repo docs route OQ-1/OQ-4 to a generic `decisions/phase2/`; the area-specific
  home is **`decisions/merkle/`** (this file) per the task's "DECIDE & LOG
  (decisions/merkle/)" instruction. Same decision, area-named location.

## Context

The Merkle tree is "the source of truth for *what differs*." A **bare content
hash** leaf is sufficient for a one-way **mirror**: where two leaf hashes differ,
the bytes differ, send them. Merkle Sync is **two-way sync**, which a bare hash
cannot serve — it cannot answer the three questions every reconciliation step
needs (`docs/audit/decisions/phase0/merkle-leaf-shape.md:11-31`;
`docs/audit/findings/merkle/MK-3-leaf-metadata-two-way-sync.md`):

1. **Who is causally newer** (which edit happened-before which), without trusting a
   wall clock that skews across two laptops.
2. **Did the two edits truly conflict (concurrent) or is one side simply behind
   (causal)** — the difference between "apply silently" and "make a conflict copy."
3. **Was the file deleted** — absence is ambiguous between *deleted here*, *not yet
   created here*, and *deleted elsewhere and must be removed here*.

Phase 0 already validated the **field set** (Option C: `hash + size + mode + mtime
+ version-vector + tombstone`). What Phase 0 deferred — and what is consequential
because it is the wire/disk ABI for the convergence oracle (SR-5: "converged ⇔
identical root hash") — is **the byte-exact recipe** for (a) which fields the
*structural* (tree) hash commits to, (b) leaf-vs-node domain separation, (c) child
ordering, and (d) the serialization grammar that must be **identical on Mac and
Windows** (SR-13). Getting (d) wrong is, per `merkle-tree` §4.3, "the
highest-probability convergence bug" for a project that is *literally* Mac↔Windows.

This decision therefore (1) re-affirms the field set with a per-field
two-way-sync justification, and (2) pins the hardened structural-hash recipe.

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — bare `content_hash` leaf (one-way mirror)

- Correctness **1** (two-way): no causality, no deletion, no concurrent-vs-causal.
- Concurrency **5** · Testability **5** · Cross-platform **4**.
- **Rejected:** cannot express the two-way model at all.

### Option B — `content_hash + size + mtime` (rsync / basic-mirror identity)

- Correctness **2**: `size`+`mtime` is a fast "probably changed" pre-filter, but
  `mtime` is not a reliable cross-machine ordering (clock skew → silent data loss,
  the classic trap — `version-vectors` FM-5, aphyr "trouble with timestamps"); no
  deletion model; no concurrency detection.
- Concurrency **5** · Testability **4** · Cross-platform **2** (mtime ns/precision,
  TZ).
- **Rejected** as identity; `size`/`mtime` survive only as a scanner pre-filter and
  a conflict tiebreaker respectively.

### Option C — Option-C field set + the **current SKILL serialization** (baseline)

The Phase 0 / `SKILL.md:67-83` recipe: `leaf = SHA-256(canonical(name,
content_hash, mode, deleted, version))`, `dir = SHA-256(name-sorted (childName,
childHash))`, **no leaf-vs-node domain separation**, and an under-specified byte
grammar ("fixed widths, big-endian, length-prefixed names" — not pinned to bytes).

- Correctness **4**: field set is right, but two real gaps remain — (i) **no
  domain separation** between a file-leaf and a directory-node hash, leaving the
  second-preimage / layout-collision class open (`merkle-tree` §4.1); (ii) the
  exact bytes are not fixed, so two independent implementations (or a future
  refactor) could disagree on the root for identical content → never converge.
- Concurrency **5** · Testability **4** · Cross-platform **3** (the grammar gap is
  exactly the NFD/NFC, separator, integer-width hazard of `merkle-tree` §4.3).
- **Rejected** as *final* — it is the baseline this decision hardens.

### Option D — Option-C field set + **hardened** structural-hash recipe (CHOSEN)

Same fields as Option C, plus the four hardening refinements below: RFC-6962
domain separation, git-style "name committed by the parent only," a byte-exact
fixed-width/big-endian/length-prefixed grammar, and a single explicit child-order
rule.

- Correctness **5**: closes the second-preimage class (domain separation) and the
  cross-impl ambiguity (byte-exact grammar); convergence becomes a provable
  property of a fixed function, not an emergent accident.
- Concurrency **5**: the hashed value is computed over an **immutable `FileInfo`
  snapshot** copied out under `RLock` (GR-5); the VV encoding is copy-on-write
  (`version-vectors` §8 A4), so no shared-slice aliasing.
- Testability **5**: a golden-vector test pins the exact bytes; a Mac→wire→
  Windows→wire→Mac round-trip test (SR-13) asserts identical subtree hashes; the
  "one byte changed ⇒ exactly that leaf's branch + root" property test (SR-5).
- Cross-platform **5**: forward-slash NFC names, big-endian fixed widths, length
  prefixes — nothing OS-specific enters the hash.

## Decision

Adopt **Option D**. The leaf is the Phase 0
`FileInfo{path, content_hash, size, mode, mtime, version_vector, deleted}`; the
**structural hash** is computed by the following byte-exact recipe.

### D.1 Field set and per-field justification (why each exists for two-way sync)

| field | type | why it must be in the leaf (two-way) | in structural hash? |
|---|---|---|---|
| `path` | canonical forward-slash relative NFC string | identity / tree position; the map key (XP-1, SR-13) | **via parent** (the child *name* component, see D.3) |
| `content_hash` | `[32]byte` SHA-256 of file bytes (tombstone: 32 zero bytes) | "do the bytes differ?" + the transfer/dedup key; independent of metadata | **yes** |
| `size` | `uint64` | cheap mismatch reject + transfer planning; **redundant with `content_hash`** for difference detection | **no** (would not change the answer; excluded to keep the hash content-pure) |
| `mode` | `uint32` (perm/type/exec bit) | an exec-bit/permission change carries no content change but is a real, syncable edit; best-effort on Windows (XP-6) | **yes** |
| `mtime` | `int64` ns | **conflict tiebreaker ONLY** (older mtime loses, SR-7); **never** orders edits (SR-4) | **no** (hashing volatile per-machine mtime manufactures spurious whole-tree diffs — `merkle-tree` §4.4) |
| `version_vector` | sorted `[]Counter{ID uint64, Value uint64}` | the *only* truth for who-is-newer / concurrent-vs-causal (SR-4); a delete bumps it | **yes** (so "converged ⇔ equal root" holds even for same-bytes-different-history files and for tombstones) |
| `deleted` (tombstone) | `bool` + the bumped VV | a delete is a **versioned event**, not an absence; lets a tombstone *dominate* a stale peer's pre-delete VV (no resurrection, SR-9/SR-10) | **yes** |

**Excluded from the structural hash: `mtime` and `size`.** This is load-bearing,
not an optimisation: `mtime` differs across machines for byte-identical files
(hashing it ⇒ the tree never converges); `size` is fully determined by
`content_hash`. Both stay in `FileInfo` (mtime as the SR-7 tiebreaker, size as a
scanner pre-filter and transfer hint).

### D.2 Leaf-vs-node domain separation (RFC-6962) — the one spec change vs Phase 0

Prefix a fixed **type byte** before hashing, per RFC 9162 §2.1.1:

```
leafStructuralHash = SHA-256( 0x00 || leafEncoding )      # a file (or tombstone) leaf
dirNodeHash        = SHA-256( 0x01 || nodeEncoding )      # a directory node
rootHash           = dirNodeHash(of the root directory)
```

RFC 9162 §2.1.1 (accessed 2026-06-28,
https://datatracker.ietf.org/doc/html/rfc9162): `MTH({d0}) = HASH(0x00 || d0)`,
`MTH(D_n) = HASH(0x01 || MTH(D[0:k]) || MTH(D[k:n]))`, and *"the hash calculations
for leaves and nodes differ; this domain separation is required to give second
preimage resistance."* Without it, a crafted leaf encoding that happens to equal a
node's child-list bytes would collide a file with a directory (`merkle-tree` §4.1,
AL-20). Git gets a coarser version of this "for free" via its `"blob "`/`"tree "`
type prefix; we make it explicit and one byte cheap.

### D.3 Byte-exact serialization grammar (the convergence ABI)

All integers **big-endian** (`encoding/binary.BigEndian`). Names are the **canonical
NFC, forward-slash** form (XP-1, XP-2); only the **leaf component** (not the full
path) appears in a parent's child entry — the full path is reconstructed by walking.

```
leafEncoding  =  content_hash[32]                 # raw bytes; tombstone ⇒ 32×0x00
              || mode        : uint32             # 4 bytes
              || deleted     : uint8              # 0x00 | 0x01
              || vvCount     : uint16             # number of VV counters
              || vvCount × ( id:uint64 || value:uint64 )   # counters SORTED ASCENDING by id

nodeEncoding  =  childCount  : uint32
              || childCount × ( nameLen:uint16 || nameBytes || childHash[32] )
                                                   # children SORTED ASCENDING by
                                                   #   bytewise compare of nameBytes
```

Notes that make it unambiguous and deterministic:

- **`content_hash` excludes the name** (git model: a blob id is content-only). The
  child name is committed **exactly once**, by the parent's child entry — this
  preserves the "identical content ⇒ identical leaf hash ⇒ shared subtree" dedup
  property (`merkle-tree` §2.2) and avoids double-committing the name. (This refines
  the Phase 0 sketch's loose `canonical(name, content_hash, …)`; flagged for the
  Phase 3 tree-critic.)
- **Child order = plain bytewise ascending compare of the canonical NFC name
  bytes.** We deliberately do **not** copy git's directory-as-`name/` trailing-slash
  rule (`merkle-tree` §3.2): files and directories are separated structurally and
  the only hazard is *inconsistency*, not the specific rule, so the simplest fixed
  rule wins (`merkle-tree` A4). The rule is fixed here forever.
- **VV counters sorted ascending by `id`**, fixed 16 bytes each — byte-deterministic
  and identical Mac/Windows (`version-vectors` §8 A3). `Bump`/`Merge` are
  copy-on-write (return a fresh backing array) to honour GR-5 immutable snapshots
  and avoid the Syncthing value-receiver aliasing footgun (`version-vectors` §4.7,
  §8 A4).
- **Tombstone leaf:** `content_hash = 32×0x00`, `deleted = 0x01`, VV bumped. The
  flipped `deleted` byte + bumped VV make the tombstone's leaf hash distinct from
  the pre-delete leaf, so the deletion propagates and changes the root.
- **Empty directory:** `nodeEncoding = childCount(0)` ⇒ `SHA-256(0x01 ||
  00 00 00 00)` — well-defined.
- Self-delimiting: every variable-length region is length-prefixed, so no
  `name="a",hash=Hb` vs `name="a"+Hb,hash=""` ambiguity (`merkle-tree` §4.3).

### D.4 How a folder hash recomputes on a single leaf change (incremental rebuild)

A directory node's hash is `SHA-256(0x01 || nodeEncoding)` over its **direct
children's** `(name, childHash)` pairs. Therefore a change to one leaf propagates
**only up its own root→leaf path**:

1. The changed file's `leafEncoding` changes (new `content_hash`, or flipped
   `deleted`, or bumped VV, or new `mode`) ⇒ its leaf structural hash changes.
2. Its parent directory's child entry for that name now carries the new childHash ⇒
   the parent's `nodeEncoding` changes ⇒ the parent hash changes. **Sibling child
   entries are untouched and their hashes are reused verbatim.**
3. Repeat up each ancestor to the root. Re-hash exactly the **O(depth-of-this-path)**
   directory nodes on the path; every off-path subtree hash is reused.

So one byte changed in one file flips **exactly that leaf's branch and the root,
and nothing else** — this is both the SR-5 acceptance test and the rebuild
algorithm. Honest cost of a rebuild after `d` changed leaves is `O(Σ depths of the
changed paths)` node re-hashes (shared ancestors counted once), **not** `O(n)` and
**not** a strict `O(log n)` (the tree is an unbalanced directory hierarchy —
`merkle-tree` §4.5, §5). Diff has the mirror property (D in
`MK-2-diff-reconciliation`).

## Rationale

- **Directly satisfies the two-way requirements a bare hash cannot** (causal order
  via VV, deletion via tombstone, permission edits via `mode`, deterministic
  tiebreak via `mtime`-then-deviceID) — the whole point of the leaf carrying more
  than a hash.
- **Domain separation is a one-byte, standards-blessed fix** (RFC 9162 §2.1.1) for
  a real collision class the Phase 0 recipe left open; cost is negligible, the
  property (a leaf hash can never equal a node hash) is exactly what a recursive
  hash tree needs.
- **A byte-exact grammar turns convergence from "hopefully" into a property of a
  fixed pure function** — testable by golden vector and by the Mac↔Windows
  round-trip, which is the only defence against `merkle-tree` §4.3, the
  highest-probability bug for this project.
- **Keeping content-hash and structural-hash separate concerns** lets the transfer
  layer dedup by `content_hash` while the differ reasons over full `FileInfo`,
  preventing the two classic failures: mtime jitter → endless false diffs, and
  invisible deletions/permission changes.

## Consequences

- Drives `internal/merkle/{fileinfo.go, node.go, tree.go, codec.go}` and
  `internal/protocol/versionvector.go` (`structure.md:54,75-82`). `codec.go` owns
  the D.3 grammar; `node.go` owns the D.2 domain bytes and D.4 rebuild.
- **Test obligations:** golden-vector for `leafEncoding`/`nodeEncoding`; SR-5 "one
  byte ⇒ exactly that branch + root"; SR-13 Mac→wire→Windows→wire→Mac identical
  subtree hashes; VV `Compare` table (dominates/dominated/concurrent); `-race` on
  the COW VV ops.
- **Forward-compat:** the structural-hash function is itself versioned by the
  protocol `algo_version` field introduced in the chunking decision
  (`decisions/merkle/chunking-fixed-32kib-vs-cdc.md`); changing this recipe in a
  future version is a fail-closed, negotiated change, never a silent one.
- **Handed to Phase 3 tree-critic:** review the "name committed by parent only"
  refinement vs the Phase 0 sketch, and the persisted last-synced snapshot needed
  to detect deletions across a daemon restart (R-5 / OQ-5 — see
  `findings/merkle/MK-6-persisted-snapshot-restart-deletion.md`).
- Cross-references: SR-4/5/6/7/9/10/13, XP-1/2/6, GR-5/12; literature
  `merkle-tree`, `version-vectors`, `syncthing-bep`; decision
  `phase0/merkle-leaf-shape.md`.
