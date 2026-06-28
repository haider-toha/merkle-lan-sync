# Literature finding: The rsync algorithm (rolling checksum + strong hash delta transfer)

- Phase: 1 (problem-space map) · Agent: literature-mapper · Slug: `rsync-algorithm`
- Status: informational (no implementation decided here — per the literature-mapper
  contract, open questions are flagged for the synthesizer / Phase 2)
- Date: 2026-06-28 (all URLs accessed 2026-06-28)
- Reads-first honoured: `docs/audit/rules/{sync,go,crossplatform}-rules.md`

> **One-line takeaway.** rsync answers a narrower question than Merkle Sync's
> tree diff: *"given that two machines each hold a **similar** version of **one
> file**, and only one of them can read both, transfer the minimum bytes to make
> them equal."* Its rolling-weak + strong-hash block search is the canonical
> technique for **sub-file** delta. For Merkle Sync it is an **optional WS-4
> optimisation below the leaf**, not the diff engine — and on a fast LAN
> (Merkle Sync's target) rsync's *own authors default to whole-file copy*, which
> is the single most important lesson here (see §11).

---

## 1. Provenance — papers and real source

| # | Source | What it authorities | URL (accessed 2026-06-28) |
|---|---|---|---|
| P1 | Tridgell & Mackerras, *The rsync algorithm*, Tech. Report **TR-CS-96-05**, ANU, June 1996 | the canonical algorithm + formulas | https://rsync.samba.org/tech_report/tech_report.html (sections `node1`–`node7`) |
| P2 | A. Tridgell, *Efficient Algorithms for Sorting and Synchronization* (PhD thesis), ANU, Feb 1999 | the fully-worked treatment (ch. 3–5) | https://www.samba.org/~tridge/phd_thesis.pdf |
| S1 | **librsync** `doc/formats.md` / rendered "File formats" | exact on-the-wire signature + delta byte layout | https://librsync.github.io/page_formats.html · https://github.com/librsync/librsync/blob/master/doc/formats.md |
| S2 | **librsync** `src/librsync.h` (real source) | magic-number constants (hex) | https://github.com/librsync/librsync/blob/master/src/librsync.h |
| S3 | **librsync** `src/prototab.h` (real source) | the full `RS_OP_*` delta-opcode table (hex) | https://github.com/librsync/librsync/blob/master/src/prototab.h |
| S4 | D. Baarda, *librsync and rsync vulnerability to maliciously crafted data* (rsync mailing list, Apr 2004) | the `checksum_seed` anti-collision rationale | https://lists.samba.org/archive/rsync/2004-April/009092.html |
| S5 | **rsync** man page (`rsync.1`) | `--whole-file` default, whole-file post-transfer verification | https://download.samba.org/pub/rsync/rsync.1 |
| S6 | **rsync** `NEWS.md` + `checksum.c` (real source) | checksum evolution MD4→MD5→xxHash, negotiation | https://download.samba.org/pub/rsync/NEWS.md · https://github.com/RsyncProject/rsync/blob/master/checksum.c |

---

## 2. The problem and the sender/receiver assumption (the "why")

Two files `A` and `B` sit on two machines "connected by a slow communications
link, for example a dial up IP link"; you want to "update `B` to be the same as
`A`" and `A`/`B` are "quite similar, perhaps both derived from the same original
file" (P1, `node1`). Whole-file copy ignores the similarity; generic compression
"usually only gain[s] a factor of 2 to 4" (P1, `node1`).

The crux — and the assumption that shapes everything — is stated by P1 (`node1`):
classic diff methods "rely on being able to read both files," i.e. "both files
are available beforehand at one end of the link." rsync's contribution is to
compute "which parts of a source file match some part of an existing destination
file" **without either end ever holding both files**.

**Roles (use these names; they are routinely stated backwards).** From P1
(`node2`):

- **`α` (alpha)** holds **`A`** — the *up-to-date* file. `α` is the **sender** of
  the delta.
- **`β` (beta)** holds **`B`** — the *old* file to be updated. `β` is the
  **receiver** (gets updated to equal `A`).
- The party that **computes block signatures is the receiver `β`** — it advertises
  *what it already has* (signatures of its old `B`). The **sender `α`** computes
  the delta of its new `A` against those signatures.

In rsync-the-program the same split is named **generator → sender → receiver**:
the *generator* runs on the receiving host and emits checksums of the receiver's
old files; the *sender* (new files) consumes them and emits the delta; the
*receiver* reconstructs (P1 `node5`; P2 ch. 3). **The signature flows from the
side that lacks the data; the delta flows from the side that has it.** This
asymmetry is the whole trick.

---

## 3. Core algorithm — five steps (verbatim, P1 `node2`)

1. "β splits the file B into a series of non-overlapping fixed-sized blocks of
   size S bytes. The last block may be shorter than S bytes."
2. "For each of these blocks β calculates two checksums: a weak ''rolling''
   32-bit checksum (described below) and a strong 128-bit MD4 checksum."
3. "β sends these checksums to α."
4. "α searches through A to find all blocks of length S bytes (**at any offset,
   not just multiples of S**) that have the same weak and strong checksum as one
   of the blocks of B."
5. "α sends β a sequence of instructions for constructing a copy of A. Each
   instruction is either a reference to a block of B, or literal data. Literal
   data is sent only for those sections of A which did not match any of the
   blocks of B."

The **"at any offset"** in step 4 is what makes rsync robust to **insertions and
deletions**: a one-byte insert near the start of `A` shifts every later byte, yet
`α` re-finds `B`'s blocks at their new, non-`S`-aligned offsets. This is precisely
the property fixed-size chunking *cannot* provide and is the direct bridge to the
`cdc-chunking` finding (the "insert one byte shifts every boundary" problem).

---

## 4. The rolling weak checksum — exact formulas (verbatim, P1 `node3`)

For bytes `X_k … X_l` of the file, with **`M = 2^16`**:

```
a(k,l) = ( Σ_{i=k..l}  X_i )           mod M
b(k,l) = ( Σ_{i=k..l} (l - i + 1) X_i ) mod M
s(k,l) = a(k,l) + 2^16 · b(k,l)
```

`s(k,l)` is the packed 32-bit weak checksum (`a` in the low 16 bits, `b` in the
high 16 bits). It is **Adler-32-inspired** but *not* Adler-32: Adler-32 uses the
prime modulus 65521; rsync uses `M = 2^16 = 65536`, trading a hair of collision
resistance for a free modulo (mask with `0xFFFF`) (P1 `node3`; cross-check P2
ch. 3).

**Why "rolling" — the recurrences (verbatim, P1 `node3`):**

```
a(k+1, l+1) = ( a(k,l) - X_k + X_{l+1} )                 mod M
b(k+1, l+1) = ( b(k,l) - (l - k + 1) X_k + a(k+1,l+1) )  mod M
```

Sliding the `S`-byte window forward by one byte costs **O(1)** (subtract the
leaving byte, add the entering byte), so `α` can compute the weak checksum at
**every** byte offset of `A` in `O(|A|)` total — the linear-time scan that makes
step 4 affordable.

---

## 5. The strong checksum and the three-level search

A weak-checksum hit is only a *candidate*. rsync confirms with a **strong 128-bit
MD4** checksum per block (P1 `node2`). To keep the per-offset scan cheap, P1
(`node4`) uses a **three-level search**:

1. **16-bit hash + `2^16`-entry hash table.** "The first level uses a 16-bit hash
   of the 32-bit rolling checksum and a `2^16` entry hash table." Each table slot
   points into a list of `β`'s blocks sorted by that 16-bit hash (or is null).
2. **32-bit weak compare.** "scanning the sorted checksum list starting with the
   entry pointed to by the hash table entry, looking for an entry whose 32-bit
   rolling checksum matches the current value"; the scan stops at a different hash.
3. **Strong (MD4) compare.** "calculating the strong checksum for the current
   offset … and comparing it with the strong checksum value in the current list
   entry." Only now is it a confirmed match.

Crucially, the expensive MD4 is computed **only** when levels 1+2 pass, so for
the overwhelmingly common "no match here" offset the cost is one O(1) rolling
update + one table probe. On a match, "the search restarts at the matched block's
conclusion" (P1 `node4`) — skipping `S` bytes at a stroke when files are nearly
identical.

---

## 6. EXACT data structures / field layouts

### 6.1 The signature pair (the wire cost of advertising a block)

Each block `β` advertises costs **20 bytes**: "4 bytes for the rolling checksum
plus 16 bytes for the 128-bit MD4 checksum" (P1 `node6`). For a file of `n`
bytes split into blocks of `S`, that is `⌈n/S⌉ · 20` bytes of signature traffic —
the term you minimise by enlarging `S` (§7).

### 6.2 librsync signature file — header + per-block (S1, S2)

librsync (the reusable library extraction of the algorithm) fixes a concrete,
**big-endian / network-order** byte layout. Header:

```
u32 magic          // one of the RS_*_SIG_MAGIC values below
u32 block_len      // S, bytes per block
u32 strong_sum_len // truncated strong-sum length, bytes (≤ full digest)
```

followed by one entry per block:

```
u32              weak_sum
u8[strong_sum_len] strong_sum   // truncated MD4 or BLAKE2 digest
```

Note `strong_sum_len` is **tunable and usually truncated** — librsync picks a
strong-sum length "based on the block size and file size to give a compact
signature without risking blocksum collisions" (S1; block-size issue #129,
https://github.com/librsync/librsync/issues/129).

### 6.3 Magic numbers (real source — S2, `src/librsync.h`)

"A uint32 magic number, emitted in bigendian/network order at the start of" each
stream. Note `0x7273 == "rs"`:

```
RS_DELTA_MAGIC        = 0x72730236   // delta stream
RS_MD4_SIG_MAGIC      = 0x72730136   // signature, weak=rollsum(adler-like), strong=MD4
RS_BLAKE2_SIG_MAGIC   = 0x72730137   // signature, strong=BLAKE2 (librsync ≥ 1.0 default)
RS_RK_MD4_SIG_MAGIC   = 0x72730146   // weak=RabinKarp rollsum, strong=MD4
RS_RK_BLAKE2_SIG_MAGIC= 0x72730147   // weak=RabinKarp rollsum, strong=BLAKE2
```

The versioned magic is the lesson: the wire format is **self-identifying** so old
readers fail loudly ("bad magic number") rather than mis-parsing a newer format.

### 6.4 librsync delta command stream — full opcode table (real source — S3, `src/prototab.h`; ranges confirmed in S1)

A delta is `RS_DELTA_MAGIC` followed by a sequence of commands; **three kinds**:
literal (emit bytes inline), copy (copy a range from the basis file `B`), and end.
The opcode byte selects the kind *and* the width of its length/offset arguments:

| opcode (hex) | symbol | meaning |
|---|---|---|
| `0x00` | `RS_OP_END` | end of delta (single null byte, no args) |
| `0x01`–`0x40` | `RS_OP_LITERAL_1` … `RS_OP_LITERAL_64` | **inline short literal**: the opcode *is* the byte count (1–64); that many literal bytes follow immediately — no separate length field |
| `0x41`–`0x44` | `RS_OP_LITERAL_N1/N2/N4/N8` | literal whose length is given by the next **1 / 2 / 4 / 8** bytes, then the data |
| `0x45`–`0x54` | `RS_OP_COPY_Nx_Ny` (16 combos) | copy from basis: `x` = byte-width of the **start offset** arg ∈ {1,2,4,8}; `y` = byte-width of the **length** arg ∈ {1,2,4,8}. (`0x45`=`COPY_N1_N1` … `0x54`=`COPY_N8_N8`) |
| `0x55`–`0xFF` | `RS_OP_RESERVED_85` … `_255` | reserved |

Two design ideas worth stealing conceptually: (a) **variable-width integer
arguments** chosen per command keep small deltas tiny (a 30-byte literal is 1
opcode + 30 bytes, no 8-byte length); (b) **inline short literals** (`0x01`–`0x40`)
avoid any length field for the common tiny-run case. These are exactly the kind of
compactness the Merkle Sync `REQUEST`/`RESPONSE` payload design (decision
`message-type-codes.md`) can borrow — *as ideas*, not as a format to copy.

---

## 7. Block-size selection (the central tuning knob)

There is a tension P1 (`node6`) makes explicit:

- **Small `S`** → more, finer matches (less literal data) **but** more signature
  overhead (`⌈n/S⌉ · 20` bytes) and a higher weak-checksum false-alarm rate.
- **Large `S`** → cheap signatures **but** a single changed byte invalidates a
  whole `S`-byte block, forcing it to be sent as literal.

P1's experiment: "For block sizes above 300 bytes, only a small fraction (around
5%) of the file was transferred." Modern rsync/librsync make `S` adaptive: the
**dynamic heuristic is `S ≈ √(file size)`**, clamped to sane bounds, with the
strong-sum length scaled so collisions stay negligible (librsync issue #129;
S1). The thesis (P2 ch. 3) derives the optimal-`S` tradeoff formally.

---

## 8. Pipelining and the round-trip count

rsync is **one logical round trip per file**: signatures one way, delta back. P1
(`node5`) pipelines the two directions — "One of the processes generates and
sends the checksums to α while the other receives the difference information from
α and reconstructs the files" — so "If the communications link is buffered then
these two processes can proceed independently and the link should be kept fully
utilised in both directions." That maps cleanly onto a **full-duplex connection
with separate reader/writer goroutines**, which is exactly Merkle Sync's
transport shape (GR-3, GR-4; `internal/transport/conn.go` "per-conn reader +
writer goroutines").

---

## 9. Failure modes

1. **Weak-checksum false alarms (benign, costs CPU).** Level-1/2 hits that fail
   the strong check just waste an MD4 computation. Measured rate: "less than
   1/1000 of the number of true matches" (P1 `node6`). A weaker/smaller `S`
   raises it.

2. **Strong-checksum collision → silent wrong reconstruction (the dangerous
   one).** If two *different* blocks share both the weak checksum and the
   (truncated, non-cryptographic) MD4, `α` emits a *reference* where it should
   have emitted *literal* bytes, and `β` reconstructs the **wrong file with no
   error**. Probability is tiny for random data but **not** for an adversary:
   Baarda (S4) reports it is "disturbingly easy" to craft colliding blocks, in
   which case librsync would "silently corrupt files." Two defences:
   - **Per-session random `checksum_seed`** mixed into the strong checksum so an
     attacker cannot precompute collisions: "librsync by default uses a
     pse[u]do-random checksum_seed, that makes such an attack nearly impossible"
     (S4). rsync does the same; the seed is sent at negotiation.
   - **Whole-file verification after reconstruction.** "rsync always verifies
     that each transferred file was correctly reconstructed on the receiving side
     by checking a whole-file checksum that is generated as the file is
     transferred" (S5). On mismatch rsync re-sends the file in a later pass — so
     a block collision is caught and corrected rather than committed. (Secondary
     reports describe a reseed-and-retry then whole-file fallback; the
     authoritative, citable fact is the mandatory whole-file verify in S5.)

3. **Weak strong-hash by modern standards.** The original strong hash is **MD4**,
   long broken for collision resistance. rsync moved to **MD5** at protocol 30
   (rsync 3.0) and added **xxHash** and SHA variants with run-time negotiation in
   **rsync 3.2** (2020); librsync defaults to **256-bit BLAKE2** (S6, `NEWS.md`,
   `checksum.c`; S1). **Lesson for us: do not inherit MD4.**

4. **No match found → graceful degradation to whole-file.** If `A` and `B` are
   unrelated, *nothing* matches, every byte is literal, and rsync has merely paid
   the signature overhead on top of a full copy — strictly worse than `cp`. This
   is why rsync makes **whole-file copy the default on fast links** (§11).

5. **Renames are invisible.** rsync diffs a *named pair* `A↔B`; a file renamed
   on `α` is, to `α`, a brand-new path with no `B` to diff against → full
   re-send. (Merkle Sync inherits this exact limitation unless rename detection
   is added — flagged by `merkle-researcher`/roster as a separate decision.)

---

## 10. Complexity

| resource | cost | source / reasoning |
|---|---|---|
| `β` signature compute | **O(\|B\|)** — one weak + one MD4 per `⌈\|B\|/S⌉` block | P1 §2 |
| Signature bytes on wire | **`⌈\|B\|/S\| · 20` bytes** (4 weak + 16 MD4) | P1 `node6` |
| `α` search | **O(\|A\|)** time: O(1) rolling update + O(1) hash probe per byte; MD4 only on a level-1/2 hit (≈ true-matches + <0.1%) | P1 `node3`,`node4` |
| `α` memory | **O(\|B\|/S)** — the in-RAM block index (hash table + sorted list) | P1 `node4` |
| Delta bytes on wire | **literal bytes (the actual differences) + a few bytes per copy reference** | P1 `node2` |
| Round trips | **1** per file (pipelined both directions) | P1 `node5` |
| CPU vs. `diff` | "the CPU time taken was less than the time it takes to run `diff`" | P1 `node6` |

Net: rsync turns an `O(\|A\|+\|B\|)` *bandwidth* problem into an `O(differences)`
one, at the price of `O(\|A\|+\|B\|)` *CPU* on both ends plus `O(\|B\|/S)` signature
bandwidth. **The trade only pays when bandwidth is the bottleneck** — the hinge
for §11.

---

## 11. The fast-LAN caveat (most important point for Merkle Sync)

rsync's own man page: **whole-file copy is the *default*** "when both the source
and destination are specified as local paths," and the delta algorithm "may be
[slower than] … this option … when the bandwidth between the source and
destination machines is higher than the bandwidth to disk" (S5, `--whole-file`).

Merkle Sync is a **LAN** tool (raw TCP, often 1 GbE+), i.e. exactly the
"bandwidth ≥ disk" regime where rsync's authors switch the delta *off*. So the
delta algorithm's headline win (saving WAN bytes) is **weakest** in Merkle Sync's
common case, and the CPU cost (hash both files at every byte) is real. This is
strong evidence that Merkle Sync should **default to whole-file (chunked)
transfer** and treat sub-file rsync-style delta as a **targeted optimisation**
(large file, small edit) — *if it is built at all*.

---

## 12. How it maps to Merkle Sync (adopt vs adapt)

Merkle Sync already settled the *file-level* identity/diff problem with Merkle
trees + `FileInfo` leaves (`decisions/phase0/merkle-leaf-shape.md`) and version
vectors. The Merkle tree diff answers **"which files differ"** in `O(log n)`
(SKILL.md §2). **rsync answers a strictly lower-level question: once you know a
single file differs, how do you transfer the new content cheaply.** It lives
**below the leaf**, inside `internal/reconcile/transfer.go`, never in the differ.

### ADOPT (concepts / corroboration)

- **A1 — Verify-after-reconstruct (strong corroboration of SR-3).** rsync's
  mandatory whole-file checksum (S5) is prior art for: after assembling a file
  from chunks, **recompute SHA-256 and assert it equals the expected
  `FileInfo.content_hash` *before* the atomic rename** (SR-1/SR-2). Merkle Sync is
  already content-addressed (`content_hash` is SHA-256 of file bytes), so this is
  free and closes the same silent-corruption hole rsync closes. → `transfer.go`,
  cites SR-1, SR-2, SR-3.
- **A2 — Per-session anti-collision seed (defence in depth).** Even with a strong
  hash, mixing a per-connection random seed into any *block*-level matching hash
  defeats crafted-collision attacks (S4). If Merkle Sync ever does block-level
  matching, seed the block hash; the final whole-file SHA-256 check (A1) is the
  backstop regardless.
- **A3 — Pipelined full-duplex transfer.** rsync's bidirectional pipelining
  (P1 `node5`) is already Merkle Sync's transport shape (separate reader/writer
  goroutines, GR-3/GR-4). Adopt the *pattern*; it confirms the WS-2 design.
- **A4 — Compact, self-identifying, variable-width binary encoding.** librsync's
  versioned magic (S2) and variable-width command args (S3) corroborate Merkle
  Sync's Phase-0 framing choices (binary, big-endian, typed, length-guarded —
  SR-12, GR-7, GR-8) and inform the `REQUEST`/`RESPONSE` payload sketch
  (`message-type-codes.md`). Adopt the *ideas* (per-command arg widths, inline
  short runs); **do not** adopt librsync's wire format — Merkle Sync has its own
  `[4-byte len][1-byte type][payload]` frame.

### ADAPT (use the technique, change the substance)

- **D1 — Strong hash: SHA-256/BLAKE-family, never MD4.** rsync's block strong sum
  is truncated MD4 (broken). Merkle Sync already standardises on SHA-256 for
  `content_hash`; any block-level strong sum must be SHA-256 (or BLAKE2/BLAKE3)
  truncated, sized like librsync's `strong_sum_len` to keep collisions negligible
  (§6.2, §9.3). (GR-11: prefer `crypto/sha256` stdlib.)
- **D2 — The sender/receiver assumption becomes symmetric + VV-directed.** rsync
  fixes direction per file (β=old computes sigs, α=new sends delta). Merkle Sync
  is **two-way**, so direction is **decided per file by the version-vector
  comparison** (SKILL.md §3): the peer that is `DominatedBy` (behind) plays
  rsync's **β/receiver** (computes signatures of its stale copy and requests the
  delta); the `Dominates` peer plays **α/sender**. A `Concurrent` result is a
  *conflict* (SR-7), not a transfer — resolve **before** any delta runs. So
  rsync's clean role split is *kept*, but **gated by the VV decision**, not by a
  static client/server role.
- **D3 — Rolling search vs. fixed offsets: this is THE chunking fork.** Merkle
  Sync's Phase-0 `REQUEST` sketch asks for bytes by `(path, content_hash, offset,
  length)` (`message-type-codes.md`) — a **fixed-offset** model. That re-fetches a
  whole file when a 1-byte insert near the start shifts every later offset
  (exactly rsync step-4's motivation and the `cdc-chunking` finding's problem).
  Three real options for WS-4, **to be decided by `merkle-researcher` /
  `cdc-chunking` in Phase 2, not here**:
  - *(a)* **Fixed 32 KiB chunks + offset requests** (current sketch): simplest,
    great for append/random-access edits, worst for mid-file inserts.
  - *(b)* **rsync-style rolling delta**: optimal for inserts/deletes; costs a
    per-byte scan of both files (the §11 LAN-CPU caveat) and a stateful per-file
    exchange.
  - *(c)* **Content-defined chunking (FastCDC/rolling-hash boundaries) +
    content-addressed chunks**: boundaries move with content so an insert only
    re-chunks locally; chunk hashes then dedup. This is the modern middle ground
    and the likely fit for a Merkle tree of chunk hashes.

### DO NOT ADOPT

- **N1 — rsync as the *diff engine*.** The Merkle tree already does file-level
  diff in `O(log n)`; running rsync across the whole folder would be a redundant,
  slower `O(total bytes)` scan. Keep rsync strictly sub-leaf.
- **N2 — A new heavyweight dependency.** A rolling checksum is ~30 lines of Go and
  reuses `crypto/sha256`; do **not** pull `librsync`/cgo or a delta library
  (GR-11). librsync-go exists (`github.com/balena-os/librsync-go`) as a *reference
  to read*, not a dependency to add.
- **N3 — MD4, the prime-modulus Adler-32, or the librsync wire format verbatim.**

### Package landing zones (`docs/audit/plan/structure.md`)

- `internal/reconcile/transfer.go` — **if** sub-file delta is adopted, the rolling
  checksum + block search + verify-after-reconstruct live here (A1, A3, D1, D2).
- `internal/protocol/messages.go` — `REQUEST`/`RESPONSE` payload shape borrows the
  compact-encoding *ideas* (A4); decision pending D3.
- `internal/merkle/` — **unaffected as the diff engine** (N1); only relevant if
  D3(c) makes chunk hashes tree leaves.

---

## 13. Open questions flagged for the synthesizer / Phase 2

1. **Sub-file delta at all?** Given §11 (LAN ⇒ rsync's authors default to
   whole-file), should Merkle Sync ship **whole-file/chunk transfer only** in v1
   and defer any rsync-style delta? (Owner: `merkle-researcher` + planner;
   relates to roster "Chunking: fixed 32KB vs content-defined".)
2. **The chunking fork D3 (a/b/c).** The hard decision the `cdc-chunking` finding
   and `merkle-researcher` must settle; the Phase-0 fixed-offset `REQUEST` sketch
   presupposes (a) and should not be treated as final.
3. **Block strong-sum length** if any block matching is used — port librsync's
   "scale `strong_sum_len` to file/block size" rule (§6.2) to SHA-256.
4. **Rename detection** (§9.5): rsync can't; Merkle Sync's leaf carries enough
   (`content_hash`) to *optionally* detect a rename as delete+create with matching
   content — a separate roster decision, noted so it isn't lost.

---

## 14. Rule cross-references

- **SR-1 / SR-2** (temp-write → fsync → atomic rename → dir fsync): rsync also
  reconstructs into a temp file then renames — corroborates the atomic-write rule;
  delta reconstruction MUST land in the temp, never in `dst`.
- **SR-3** (idempotent, content-addressed apply): A1 — verify reassembled bytes
  hash to `content_hash` before commit; rsync's whole-file verify is the prior art.
- **SR-4 / SR-7** (VV orders edits, not mtime): D2 — VV decides transfer
  direction; `Concurrent` ⇒ conflict, resolve before transfer.
- **SR-12 / GR-7 / GR-8** (length-guarded binary framing, no `gob`): A4 — rsync's
  binary, magic-versioned, length-typed stream is concordant prior art.
- **GR-11** (stdlib-first, minimal deps): N2 — roll the checksum by hand; reuse
  `crypto/sha256`; no librsync/cgo.
- **GR-3 / GR-4** (owned goroutines, reader+writer per conn): A3 — rsync's
  pipelining is this pattern.
