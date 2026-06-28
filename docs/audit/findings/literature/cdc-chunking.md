# Literature finding: Content-Defined Chunking (Rabin / FastCDC) vs fixed-size blocks

- Source slug: `cdc-chunking`
- Phase: 1 — Problem-space map (literature-mapper)
- Status: finding (input to the **Phase 2 chunking decision** owned by the
  merkle-researcher — roster: "Decide & log: fixed 32 KB chunks vs content-defined
  chunking", `docs/audit/decisions/phase2/`)
- Date / access date for all URLs: **2026-06-28**
- Reads-first honoured: `docs/audit/rules/{go-rules,sync-rules,crossplatform-rules}.md`;
  cross-references `docs/audit/decisions/phase0/{merkle-leaf-shape,message-type-codes,framing-format}.md`

> **Scope note / role boundary.** This is a *literature map*, not the binding
> engineering decision. The roster assigns the fixed-vs-CDC decision to the Phase 2
> merkle-researcher (`decisions/phase2/`), and the leaf-shape decision already states
> chunking "is *not* part of the leaf shape; it lives under `content_hash`'s transfer
> story and is deferred to the merkle/reconcile workstream"
> (`docs/audit/decisions/phase0/merkle-leaf-shape.md`). To avoid colliding with that
> decision file (and with parallel Phase-1/2 agents), this finding presents the ≥3
> scored options as **input** in §9 and explicitly hands the binding choice to that
> Phase 2 decision. No decision file is written by this agent.

---

## 1. TL;DR

- **Fixed-size blocking** (split a file every N bytes) is trivial and deterministic
  but suffers the **boundary-shift problem**: insert or delete one byte near the
  start and *every* subsequent block boundary moves, so every block hash changes and
  deduplication / delta-transfer collapses to ~0 for the shifted region
  (restic blog; gopheracademy; Wikipedia, accessed 2026-06-28).
- **Content-Defined Chunking (CDC)** picks boundaries from a **rolling hash of a
  sliding window of the content**, not from byte offsets. Because a boundary depends
  only on the last *w* bytes, an edit only disturbs chunks within ~one window of the
  edit; unmodified data more than a window away re-chunks **identically** → edit-local
  damage, high dedup across insert/delete shifts (Wikipedia *Rolling hash*; Chonkers,
  arXiv:2509.11121, accessed 2026-06-28).
- **Rabin-fingerprint CDC** (LBFS, SOSP'01; restic in Go) is the classic: a true
  sliding window + GF(2) polynomial fingerprint; cut when the low *k* bits are zero.
- **FastCDC** (USENIX ATC'16) replaces Rabin with the cheaper **Gear** rolling hash
  (`fp = (fp<<1) + GearTable[byte]`), adds **sub-minimum cut-point skipping** and
  **normalized chunking** (two masks around the target size), and reports being
  **~10× faster than Rabin-based CDC and ~3× faster than Gear/AE-based CDC while
  achieving nearly the same deduplication ratio** (USENIX ATC'16, accessed 2026-06-28).
- **Core formula:** for a boundary mask with *k* one-bits, P(cut at a byte) ≈ 2⁻ᵏ, so
  the **expected chunk size ≈ 2ᵏ bytes**. LBFS k=13 → 8 KiB; restic default k=20 →
  1 MiB; FastCDC normal k=13 → 8 KiB (verified by popcount, see §5).
- **Headline tradeoff:** smaller average chunk ⇒ finer dedup/delta granularity **but**
  more chunks ⇒ larger hash index / per-chunk metadata. Syncthing — our primary
  reference — deliberately ships **fixed** blocks (128 KiB–16 MiB) precisely as
  "a reasonable trade off between data to shuffle and metadata to track" (calmh,
  Syncthing forum, accessed 2026-06-28).
- **Recommendation for Merkle Sync v1 (see §9):** **adopt fixed-size blocks**
  (Syncthing-style, the roster's "lock chunking to fixed 32 KB, defer CDC" pilot
  posture), and **adapt-later** to FastCDC *only if* cross-file/insert-shift dedup
  becomes a real need — and then with a **fixed, shared Gear table** across all peers
  (never restic's per-repo *randomized* polynomial, which would make Mac and Windows
  disagree on boundaries).

---

## 2. The problem CDC solves: fixed blocks and boundary shift

Fixed-size blocking cuts at offsets `0, N, 2N, 3N, …`. The block hashes let two peers
exchange only the blocks that differ (Syncthing's model). It fails on **insertion or
deletion**:

> "if a user adds a byte to the beginning of the file? The chunk boundaries (where a
> chunk ends and the next begins) would shift by one byte, changing every chunk in
> the file." — restic, *Foundation – Introducing CDC* (accessed 2026-06-28)

Worked example (gopheracademy, accessed 2026-06-28): prepending 3 bytes (`foo`) to a
file shifts the first fixed chunk from 2,908,784 → 2,908,787 bytes, "a shift that
cascades through the file with fixed boundaries", so every downstream block hash
changes even though the bytes are identical, just relocated.

CDC fixes this because boundaries are **content-anchored**:

> "unmodified data (more than a window size away from the changes) will have the same
> boundaries." — Wikipedia, *Rolling hash* (accessed 2026-06-28)

So an insert/delete only re-chunks the chunk(s) overlapping the edit plus possibly the
next one (until the rolling hash re-synchronises within one window); the rest of the
file produces **bit-identical chunks and hashes**.

---

## 3. Core algorithm (general CDC)

```
for each byte position i in the stream:
    h = rolling_hash(window ending at byte i)      # O(1) update per byte
    if (h & MASK) == 0  and  current_chunk_len >= MinSize:   # boundary condition
        emit chunk [chunk_start .. i]
        chunk_start = i + 1
    else if current_chunk_len >= MaxSize:           # forced cut (content-independent)
        emit chunk [chunk_start .. i]
        chunk_start = i + 1
emit final chunk (may be < MinSize)
```

Two design axes distinguish the variants:

1. **Which rolling hash** — Rabin polynomial fingerprint (true sliding window, removes
   the byte leaving the window) vs Gear (`fp=(fp<<1)+G[b]`, no explicit window; old
   bytes "age out" via the left shift).
2. **Which boundary mask + size clamps** — number of one-bits in the mask sets the
   expected size; `MinSize`/`MaxSize` clamp the tail of the geometric distribution.

**Boundary probability / expected-size formula.** If hash bits are ~uniform, the cut
test `(h & MASK)==0` succeeds with probability `2^(-popcount(MASK))` per byte, so the
inter-cut gap is geometric with **mean ≈ 2^popcount(MASK) bytes**. This is the single
most important sizing lever (verified numerically in §5).

---

## 4. Variant A — Rabin / LBFS, and the restic Go implementation

### 4.1 LBFS (the seminal CDC system), exact parameters

LBFS (*A Low-bandwidth Network File System*, Muthitacharoen, Chen, Mazières, SOSP'01,
https://www.sosp.org/2001/papers/mazieres.pdf, accessed 2026-06-28):

- **Window:** Rabin fingerprint over every overlapping **48 bytes**.
- **Boundary condition:** when the **low-order 13 bits** of the fingerprint equal a
  chosen constant value → that position is a **breakpoint** (chunk end).
- **Expected chunk size:** 2¹³ = **8192 bytes (8 KiB)**.
- **Clamps:** **minimum 2 KiB, maximum 64 KiB** chunk size (to bound pathological runs).

LBFS is the direct ancestor of every CDC backup/sync system; it introduced "transfer
only the chunks the peer lacks, keyed by chunk hash."

### 4.2 restic/chunker — directly adoptable Go, Rabin-based (MIT)

`github.com/restic/chunker` (`chunker.go`, `doc.go`, `polynomials.go`, accessed
2026-06-28) is the most relevant real source because **Merkle Sync is Go**. Exact
layout:

**Constants** (`chunker.go`):

```go
const (
    kiB            = 1024
    miB            = 1024 * kiB
    windowSize     = 64               // sliding-window length in bytes
    MinSize        = 512 * kiB        // 524288
    MaxSize        = 8 * miB          // 8388608
    chunkerBufSize = 512 * kiB
)
```

**Default boundary mask:** `splitmask = (1 << 20) - 1` → averageBits = 20 →
expected ≈ 2²⁰ = **1 MiB** average chunk (pkg.go.dev/github.com/restic/chunker:
"The default value is 20 bits, so chunks will be of 1 MiB size on average."). The 2015
restic blog illustratively used the **low 21 bits** (≈2 MiB).

**Emitted chunk struct:**

```go
type Chunk struct {
    Start  uint
    Length uint
    Cut    uint64     // the rolling-hash value at the cut point
    Data   []byte
}
```

**Rolling state:**

```go
type chunkerState struct {
    window [windowSize]byte   // [64]byte
    wpos   uint               // ring position
    digest uint64            // current Rabin fingerprint
    pre    uint              // bytes to pre-skip up to MinSize
    count  uint
}
type chunkerConfig struct {
    MinSize, MaxSize  uint
    pol               Pol     // the irreducible polynomial
    polShift          uint
    tables            tables  // precomputed out[] and mod[] (256 entries each)
    tablesInitialized bool
    splitmask         uint64
}
```

**Rolling update** — true sliding window (subtract the leaving byte via `out[]`, add the
entering byte via `mod[]` reduction):

```go
func (c *BaseChunker) slide(digest uint64, b byte) uint64 {
    out := c.window[c.wpos]
    c.window[c.wpos] = b
    digest ^= uint64(c.tables.out[out])          // remove byte leaving the 64-byte window
    c.wpos = (c.wpos + 1) % windowSize
    digest = updateDigest(digest, c.polShift, &c.tables, b)  // add entering byte
    return digest
}
func updateDigest(digest uint64, polShift uint, tab *tables, b byte) uint64 {
    index := digest >> polShift
    digest <<= 8
    digest |= uint64(b)
    digest ^= uint64(tab.mod[index])              // GF(2) polynomial reduction
    return digest
}
```

**Boundary + clamp logic** (`Next()`):

```go
if add < minSize { continue }                          // skip until MinSize reached
if (digest & c.splitmask) == 0 || add >= maxSize {     // content cut OR forced max cut
    // emit chunk
}
```

**The polynomial** (`doc.go`, `polynomials.go`):

> "Package chunker implements Content Defined Chunking (CDC) based on a rolling Rabin
> Checksum." … "The degree 53 is chosen because it is the largest prime below
> 64-8 = 56, so that the top 8 bits of an uint64 can be used for optimising
> calculations in the chunker." … a random degree-53 **irreducible** polynomial is
> drawn (bit 53 set, bit 0 set, 51 random bits; "probability for selecting an
> irreducible polynomial at random is about 7.5%").

> **Critical cross-peer caveat (load-bearing for Merkle Sync, see §7/§9):** restic
> draws a **random** polynomial **per repository** (a security/anti-fingerprinting
> measure). Two peers with *different* polynomials compute *different* boundaries on
> identical bytes → **zero chunk-level reuse between them**. Any CDC adopted by Merkle
> Sync MUST use a **fixed, shared** polynomial/table baked into the protocol.

restic's rolling hash optimisation history is documented in
`github.com/restic/chunker/pull/24` (accessed 2026-06-28).

---

## 5. Variant B — Gear / FastCDC, exact masks and the normalized-chunking algorithm

FastCDC (Xia, Zhou, Jiang, Feng, Hua, Hu, Zhang, Liu — *FastCDC: a Fast and Efficient
Content-Defined Chunking Approach for Data Deduplication*, USENIX ATC'16, pp. 101–114;
PDF https://www.usenix.org/system/files/conference/atc16/atc16-paper-xia.pdf and mirror
https://csyhua.github.io/csyhua/hua-atc2016.pdf; extended journal: IEEE TPDS 2020,
https://ranger.uta.edu/~jiang/publication/Journals/2020/2020-IEEE-TPDS(Wen%20Xia).pdf;
all accessed 2026-06-28).

**The Gear rolling hash** (much cheaper than Rabin — one shift, one table add):

```
fp = (fp << 1) + GearTable[byte]      // GearTable has 256 random 64-bit entries
```

There is **no explicit window** and **no subtraction**: each `<<1` ages out older
bytes' influence. Gear alone is fast but has a *small effective window* → wider
chunk-size distribution → ~1 % worse dedup than Rabin (Wikipedia *Rolling hash*:
Gear "generates results comparable with the Rabin fingerprint in one-third of the time"
but "the distribution of split-sizes were wider than Rabin, making deduplication
results about 1% poorer", accessed 2026-06-28).

**FastCDC's three techniques** (USENIX abstract, accessed 2026-06-28):
1. **Enhance/optimise the hash judgment** — use a mask whose one-bits sit in *higher*
   bit positions (zero-padding the low bits), which enlarges Gear's effective window so
   more preceding bytes influence the cut decision (recovering Rabin-like distribution).
2. **Sub-minimum cut-point skipping** — do not even evaluate the boundary test until
   `MinSize` bytes have accumulated; this skips work *and* avoids tiny chunks.
3. **Normalized chunking (NC)** — switch between two masks around the target size so the
   chunk-size distribution concentrates near the normal size (this *recovers* the dedup
   ratio lost by skipping cut-points in (2)).

**Exact constants** — from the reference implementation `wxiacode/FastCDC-c`
(`fastcdc.c`, accessed 2026-06-28), 8 KiB-target configuration:

```c
MinSize = 8192 / 4;   // 2048  bytes (2 KiB)
Mid     = 8 * 1024;   // 8192  bytes (8 KiB)  — normal/expected size
MaxSize = 8192 * 4;   // 32768 bytes (32 KiB)

// 32-bit fp variants
Mask_15 = 0xf9070353;          // popcount 15  -> expected 2^15 = 32 KiB  (the "hard" mask)
Mask_11 = 0xd9000353;          // popcount 11  -> expected 2^11 =  2 KiB  (the "easy" mask)
// 64-bit fp variants (low 16 bits zeroed so 1-bits sit high => larger effective window)
Mask_15_64 = 0x0000f90703530000;   // popcount 15
Mask_11_64 = 0x0000d90003530000;   // popcount 11
```

Bit-counts and implied sizes **verified numerically** (this finding, `python3` popcount):

| mask | popcount | expected chunk = 2^popcount | low-16 zero? | role |
|---|---|---|---|---|
| `Mask_15`/`Mask_15_64` | 15 | 32768 B (32 KiB) | 64-bit: yes | "small" region: harder to cut → grows small chunks toward normal |
| `Mask_11`/`Mask_11_64` | 11 | 2048 B (2 KiB) | 64-bit: yes | "large" region: easier to cut → trims large chunks toward normal |
| (basic 13-bit, plain Gear-CDC) | 13 | 8192 B (8 KiB) | — | non-normalized expected size |

**Normalized-chunking control flow** (`normalized_chunking_64`, FastCDC-c, accessed
2026-06-28) — the four-stage shape (also `antgroup/hugescm/docs/cdc.md`, accessed
2026-06-28):

```
Stage 1  [0 .. MinSize)        : skip — never cut (sub-minimum cut-point skipping)
Stage 2  [MinSize .. Normal)   : cut if !(fp & Mask_15)   // 15 one-bits, P(cut)=2^-15, harder
Stage 3  [Normal .. MaxSize)   : cut if !(fp & Mask_11)   // 11 one-bits, P(cut)=2^-11, easier
Stage 4  at MaxSize            : force cut (content-independent)
```

Intuition: below the target size use the **hard** mask so the algorithm rarely cuts
(few undersized chunks); past the target size use the **easy** mask so it cuts soon
(few oversized chunks). The result is a distribution tightly peaked at `Normal`, which
is what restores the dedup ratio (paper §normalized chunking). FastCDC-c also ships a
"2-byte rolling" variant `fp = (fp<<2) + LEAR[...]` processing two bytes per step for
extra speed.

**Reported performance** (USENIX abstract / researchgate, accessed 2026-06-28):

> "FastCDC is about 10x faster than the best of open-source Rabin-based CDC, and about
> 3x faster than the state-of-the-art Gear- and AE-based CDC, while achieving nearly
> the same deduplication ratio as the classic Rabin-based approach."

`wxiacode/FastCDC-c` reports up to **4.448 GB/s** (gcc -O3) on some datasets
(README, accessed 2026-06-28).

**Go ports exist** (for the "adapt-later" path): `mark-herrmann/restic-FastCDC` (restic
chunker API with FastCDC), plus several `*/chunker` forks (pkg.go.dev, accessed
2026-06-28). A from-scratch FastCDC core is ~100 lines + a 256-entry table.

---

## 6. The contrast: Syncthing's fixed blocks (our primary reference *does not* use CDC)

Syncthing's Block Exchange Protocol v1 (https://docs.syncthing.net/specs/bep-v1.html,
accessed 2026-06-28) uses **fixed-size blocks**, not CDC:

> "File data is described and transferred in units of *blocks*, each being from
> 128 KiB (131072 bytes) to 16 MiB in size, in steps of powers of two. The block size
> may vary between files but is constant in any given file, except for the last block
> which may be smaller."

Block size is chosen by **file size** (BEP spec table):

| file size | block size |
|---|---|
| 0–250 MiB | 128 KiB |
| 250–500 MiB | 256 KiB |
| 500 MiB–1 GiB | 512 KiB |
| 1–2 GiB | 1 MiB |
| 2–4 GiB | 2 MiB |
| 4–8 GiB | 4 MiB |
| 8–16 GiB | 8 MiB |
| 16 GiB+ | 16 MiB |

Wire field layouts (BEP, accessed 2026-06-28) — directly analogous to our REQUEST/INDEX:

```protobuf
message BlockInfo { int64 offset = 1; int32 size = 2; bytes hash = 3; reserved 4; }
message Request   { int32 id = 1; string folder = 2; string name = 3;
                    int64 offset = 4; int32 size = 5; bytes hash = 6;
                    bool from_temporary = 7; int32 block_no = 9; reserved 8; }
```

**Why fixed, per the maintainer** (Jakob Borg / calmh, Syncthing forum
"128 KiB block size choice", accessed 2026-06-28): the size "felt like a reasonable
trade off between data to shuffle and metadata to track", and the thread notes "block
size directly impacts the index size by the number of blocks needed to define a
file (larger the block size the lower the index size and vice versa)." CDC was **not**
adopted; a future "variable, larger, block size" was only floated. This is strong
prior-art evidence that a serious LAN sync engine ships fixed blocks for the
**simplicity / index-size** tradeoff — exactly Merkle Sync's situation.

---

## 7. Failure modes

1. **Boundary shift (the fixed-block failure CDC exists to fix).** One inserted/deleted
   byte re-hashes every downstream fixed block → ~0 delta/dedup for that file (restic
   blog; gopheracademy, accessed 2026-06-28). *Forced max-size cuts in CDC reintroduce
   this locally* — a `MaxSize` cut is content-independent, so a run that always hits
   MaxSize chunks like fixed blocks and shifts the same way (Chonkers, arXiv:2509.11121:
   CDC "chunk sizes are only expected to meet target bounds; adversarial inputs can
   produce extremely small or large chunks", accessed 2026-06-28).
2. **Pathological size distribution without clamps.** Low-entropy/repetitive data can
   make `(h & MASK)==0` true at almost every byte (runaway tiny chunks → metadata
   blow-up) or never true (one giant chunk → memory/latency). `MinSize`/`MaxSize` are
   mandatory, but **both clamps themselves cost dedup**: MinSize-skipping can step over
   a natural boundary; MaxSize-forcing creates non-content boundaries (failure mode 1).
3. **Dedup loss from cut-point skipping.** Skipping the first `MinSize` bytes removes
   real cut-points and lowers dedup; FastCDC's **normalized chunking** is the documented
   counter-measure (USENIX ATC'16, accessed 2026-06-28).
4. **Gear's narrow effective window.** Plain Gear masks the *low* bits → only the last
   few bytes matter → wider size variance → ~1 % worse dedup than Rabin (Wikipedia
   *Rolling hash*, accessed 2026-06-28). FastCDC mitigates via the high-bit mask
   (`Mask_*_64` zero low 16 bits — verified §5).
5. **Cross-peer non-determinism (the killer for sync).** Different algorithm, different
   parameters, or a **randomized polynomial/Gear table** between two peers → different
   boundaries on identical content → no chunk reuse between them, and broken
   convergence reasoning. restic *randomizes* its polynomial per repo (anti-fingerprint)
   — adopting that verbatim would silently defeat Mac↔Windows chunk sharing
   (restic `doc.go`/`polynomials.go`, accessed 2026-06-28). Merkle Sync must pin a
   single shared table. (Maps to SR-5 convergence, SR-13 canonical identity.)
6. **Security / privacy side channels.** Chunk boundaries and sizes leak file structure;
   the dedup "do you already have this chunk?" signal is a known-file/known-chunk oracle.
   Backup-service CDC parameters meant to hide content **can be extracted by attackers**
   ("These chunking algorithms often depend on per-user parameters in an attempt to
   avoid leaking information about the data being stored" and the paper "present[s]
   attacks to extract these chunking parameters" — *Chunking Attacks on File Backup
   Services using CDC*, arXiv:2504.02095, accessed 2026-06-28). For Merkle Sync (TLS,
   trusted LAN peers — see `transport-security-tofu-vs-plaintext.md`) this is low risk,
   but it is the reason restic randomizes — and the reason we *can* safely use a fixed
   table (our trust model differs).
7. **Implementation correctness pitfalls.** Off-by-one between "cut after byte i" vs
   "chunk includes byte i"; ring-buffer `wpos` wrap; failing to flush the final
   (sub-MinSize) chunk; `<<` overflow assumptions on 32- vs 64-bit fp. These are pure
   data-integrity bugs (corrupt reassembly) and need table-driven tests (GR-13, SR-5).

---

## 8. Complexity

| dimension | Rabin (restic) | Gear / FastCDC |
|---|---|---|
| Time per byte | O(1): table lookup `out[]` + `mod[]` reduction + shift/xor | O(1): one `<<1` + one add (cheaper constant) |
| Time per file | O(n) | O(n), smaller constant; **skips MinSize bytes/chunk** so touches fewer bytes |
| Throughput (reported) | baseline | **~10× Rabin, ~3× Gear/AE**; up to 4.448 GB/s (FastCDC-c) |
| State / memory | O(1): 64-byte window + two 256-entry tables (`out`,`mod`) | O(1): one 256-entry Gear table, **no window buffer** |
| Output (chunk count) | ≈ n / avg_size; avg ≈ 2^popcount(mask), clamped [Min,Max] | same, tighter distribution under NC |
| **Index/metadata overhead** | one (hash,offset,len) per chunk → smaller avg ⇒ more entries | identical structure; **this is the real cost knob** (see Syncthing §6) |

Dedup-ratio ordering (paper + Wikipedia, accessed 2026-06-28): Rabin ≈ FastCDC-NC >
plain Gear (~1 % lower) ≫ fixed blocks on shifted data (fixed can be near-0 on insert).
Speed ordering: FastCDC ≫ Gear > Rabin ≫ (fixed blocks are O(n) trivial, fastest of all
to *cut* but worst dedup on shifts).

---

## 9. How it maps to Merkle Sync (adopt vs adapt)

### 9.1 Where chunking actually lives in our design

- The Merkle leaf's `content_hash` is **SHA-256 of the whole file** and the tree diff
  already gives **O(log n) file-level** "which files differ"
  (`merkle-leaf-shape.md`). Chunking is a *second, sub-file* layer: once a file is known
  to differ, which **bytes** do we move?
- The wire already supports byte-range transfer: `REQUEST (0x04) = path + content_hash +
  offset + length`, `RESPONSE (0x05) = chunk data` (`message-type-codes.md`), framed
  `[4-byte len][1-byte type][payload]` with a 16 MiB `MaxFrameLen` (`framing-format.md`,
  SR-12). So either fixed or content-defined blocks fit the existing protocol.
- **Disambiguate two different "chunks"** (this confusion is latent in plan/README's
  "32 KB chunk streaming" + the "fixed 32 KB vs CDC" decision):
  - **Transfer/streaming chunk** = how many bytes per `RESPONSE` frame. Pure I/O /
    flow-control; pick e.g. 32 KiB–1 MiB; independent of dedup. Keep it ≤ `MaxFrameLen`.
  - **Dedup/delta block** = the unit whose hash we compare to decide "send or skip."
    *This* is what "fixed vs CDC" is about. They need not be equal.
- Materialisation is unchanged: chunks are streamed into a **temp file**, then atomic
  `rename` (SR-1/SR-2). Chunking only changes *which* RESPONSE bytes we ask for.

### 9.2 Options (scored 1–5; criteria: correctness / concurrency-safety / testability / cross-platform) — **input to the Phase 2 chunking decision**

**Option 1 — Whole-file transfer (no sub-file blocks).**
- Correctness 5 (trivially convergent) / concurrency 5 / testability 5 / cross-platform 5.
- Cost: resends the entire file for a 1-byte change; no intra/inter-file dedup. Fine for
  small files; wasteful for large mutable ones on a busy LAN.

**Option 2 — Fixed-size blocks (Syncthing-style), e.g. 32 KiB–128 KiB, hash each block, send only differing blocks. (RECOMMENDED v1)**
- Correctness **5** — deterministic boundaries, no shared-secret coordination; converges
  by construction (SR-5).
- Concurrency-safety **5** — stateless cut at offsets; no rolling state shared.
- Testability **5** — trivial table-driven tests ("one byte changed ⇒ exactly the
  blocks overlapping it differ"); matches the WS-1 acceptance phrasing.
- Cross-platform **5** — offsets are content-position only; nothing OS-specific
  (SR-13/GR-12 untouched).
- Cost: boundary-shift on insert/delete (failure mode 1) — a 1-byte insert near the
  start re-sends ~all blocks. Acceptable for a 2-device personal LAN sync where the
  common edits are in-place rewrites/appends and bandwidth is cheap. This is **exactly
  Syncthing's shipped choice and rationale** (§6).

**Option 3 — Content-defined chunking, FastCDC with a fixed shared Gear table (ADAPT-LATER).**
- Correctness **4** — converges *iff* every peer uses identical algorithm + params +
  table; the randomized-polynomial trap (failure mode 5) must be consciously avoided.
- Concurrency-safety **5** — chunker is per-file local state.
- Testability **3** — needs golden-vector tests (fixed input ⇒ fixed boundary set) and
  distribution tests; more surface than fixed blocks.
- Cross-platform **5** — content-anchored boundaries are inherently OS-independent
  (content is content); no `filepath`/Unicode interaction.
- Benefit: insert/delete-shift resilience + best cross-file dedup; speed (10×/3×).
  Cost: a new algorithm + 256-entry table, **a new dependency or ~100 LOC** (GR-11
  requires a logged decision for any dep beyond fsnotify); variable-size block metadata.

**Option 4 — rsync rolling-checksum delta (weak rolling + strong hash, sender-driven).**
- Out of scope for *this* finding (it is the `rsync-algorithm` literature source). Noted
  for completeness: it solves the same shift problem differently (receiver advertises
  block checksums; sender rolls over its copy). Heavier protocol; deferred to that
  finding + the merkle-researcher decision.

### 9.3 Recommendation (to be ratified in `decisions/phase2/`)

- **v1: adopt Option 2 (fixed blocks).** It is the simplest thing that satisfies the
  no-data-loss/convergence contract, mirrors our primary reference (Syncthing), needs
  zero new dependencies (GR-11), is trivially deterministic across Mac/Windows
  (SR-5/SR-13), and is the cheapest to test against the WS-1/WS-4 acceptance criteria.
  This matches the roster's suggested pilot posture: "lock chunking to fixed 32 KB,
  defer CDC."
  - Suggested params: dedup block = **fixed 32 KiB–128 KiB** (start 64 KiB; or scale by
    file size like Syncthing's table if large files dominate); streaming chunk per
    RESPONSE ≤ `MaxFrameLen` (e.g. 32 KiB).
- **Adapt-later trigger:** revisit Option 3 (FastCDC) only if measurements show
  insert/delete-heavy or cross-file-duplicate workloads where fixed blocks waste real
  LAN bandwidth. If adopted: (a) **FastCDC, not Rabin** (speed; simpler 256-entry table;
  no GF(2) machinery); (b) a **single fixed Gear table compiled into the protocol** —
  *never* restic's per-repo randomized polynomial (failure mode 5); (c) normalized
  chunking with MinSize 2 KiB / Normal 8 KiB (k=13) / MaxSize 64 KiB (LBFS-class) or a
  larger scale if metadata index size allows (the Syncthing index-size tradeoff, §6);
  (d) golden-vector cross-platform tests proving Mac and Windows cut identically;
  (e) log the new-dependency-or-vendored-code decision per GR-11.

### 9.4 Concrete adopt vs adapt table

| Item | Verdict | Note |
|---|---|---|
| Fixed-block delta (hash blocks, send only differing) | **ADOPT** | Syncthing model; v1 |
| `REQUEST offset+length` / `RESPONSE` byte-range transfer | **ADOPT (already present)** | `message-type-codes.md` supports both fixed & CDC |
| Whole-file SHA-256 as `content_hash` / tree identity | **KEEP** | chunk hashes are a *separate* sub-file layer, not the leaf |
| Temp-file + atomic rename reassembly | **KEEP** | SR-1/SR-2 unchanged by chunking choice |
| FastCDC (Gear, normalized chunking, skip<MinSize) | **ADAPT-LATER** | only on measured need; fixed shared table |
| Rabin polynomial fingerprint (restic-style) | **ADAPT (not adopt)** | if any CDC, prefer FastCDC; if Rabin, fix the polynomial |
| restic per-repo **randomized** polynomial | **REJECT** | breaks cross-peer determinism (SR-5/SR-13) |
| MaxSize forced cut | **NOTE** | reintroduces local boundary-shift; size it generously |
| Per-user/secret chunking params (anti-fingerprint) | **REJECT/UNNEEDED** | our trust = TLS LAN peers; fixed table is fine |

---

## 10. Sources (all accessed 2026-06-28)

Papers / specs:
- FastCDC, USENIX ATC'16 — abstract: https://www.usenix.org/conference/atc16/technical-sessions/presentation/xia ; PDF: https://www.usenix.org/system/files/conference/atc16/atc16-paper-xia.pdf ; mirror: https://csyhua.github.io/csyhua/hua-atc2016.pdf
- FastCDC extended, IEEE TPDS 2020: https://ranger.uta.edu/~jiang/publication/Journals/2020/2020-IEEE-TPDS(Wen%20Xia).pdf
- LBFS — *A Low-bandwidth Network File System*, SOSP'01: https://www.sosp.org/2001/papers/mazieres.pdf
- Chonkers — CDC with provable size/locality guarantees, arXiv:2509.11121: https://arxiv.org/abs/2509.11121
- Chunking Attacks on File Backup Services using CDC, arXiv:2504.02095: https://arxiv.org/abs/2504.02095
- Syncthing Block Exchange Protocol v1: https://docs.syncthing.net/specs/bep-v1.html
- Syncthing forum, "128 KiB block size choice": https://forum.syncthing.net/t/128-kib-block-size-choice/3128
- Wikipedia, *Rolling hash*: https://en.wikipedia.org/wiki/Rolling_hash
- Wikipedia, *Rabin fingerprint*: https://en.wikipedia.org/wiki/Rabin_fingerprint

Real source code:
- restic/chunker (Go, Rabin): https://github.com/restic/chunker/blob/master/chunker.go , /doc.go , /polynomials.go ; docs https://pkg.go.dev/github.com/restic/chunker ; perf PR https://github.com/restic/chunker/pull/24
- wxiacode/FastCDC-c (reference C): https://github.com/wxiacode/FastCDC-c ; header https://github.com/wxiacode/FastCDC-c/blob/master/fastcdc.h ; impl https://raw.githubusercontent.com/wxiacode/FastCDC-c/master/fastcdc.c
- mark-herrmann/restic-FastCDC (Go FastCDC port): https://pkg.go.dev/github.com/mark-herrmann/restic-FastCDC
- antgroup/hugescm CDC notes: https://github.com/antgroup/hugescm/blob/master/docs/cdc.md

Explainers / blogs:
- restic, *Foundation – Introducing CDC*: https://restic.net/blog/2015-09-12/restic-foundation1-cdc/
- Gopher Academy, *Splitting Data with Content-Defined Chunking*: https://blog.gopheracademy.com/advent-2018/split-data-with-cdc/

Internal cross-references:
- `docs/audit/rules/{sync-rules,go-rules,crossplatform-rules}.md` (SR-1/2/5/13, GR-7/11/13)
- `docs/audit/decisions/phase0/{merkle-leaf-shape,message-type-codes,framing-format}.md`
