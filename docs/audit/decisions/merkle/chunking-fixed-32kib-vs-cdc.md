# Decision: sub-file transfer chunking — fixed 32 KiB blocks for v1; defer CDC

- Area: merkle / reconcile (Phase 2 — merkle-researcher)
- Status: decided
- Date: 2026-06-28
- Decider: merkle-researcher (Phase 2)
- Closes open question: **OQ-1** (synthesis problem-space map §4; roster "Decide &
  log: fixed 32 KB chunks vs content-defined chunking"). Other in-repo docs
  (`cdc-chunking` §1, synthesis OQ-1) route this to a generic `decisions/phase2/`;
  the area-specific home is **`decisions/merkle/`** per the task's "DECIDE & LOG"
  instruction. Same decision, area-named location.

## Context

Once the Merkle diff (`differ.go`) has identified that a file differs between two
peers, the reconcile layer must move the differing **bytes**. The Merkle leaf's
`content_hash` is the whole-file SHA-256 (`decisions/merkle/leaf-shape-and-
structural-hash.md`); chunking is a *second, sub-file* layer that decides which
byte ranges to actually transfer. The wire already supports byte-range transfer:
`REQUEST(0x04) = path + content_hash + offset + length`, `RESPONSE(0x05) = chunk
data`, framed `[4-byte len][1-byte type][payload]` with `MaxFrameLen = 16 MiB`
(`decisions/phase0/{message-type-codes,framing-format}.md`, SR-12).

**Two distinct "chunks" must not be conflated** (`cdc-chunking` §9.1):

- **Streaming/transfer chunk** = how many bytes ride in one `RESPONSE` frame. Pure
  flow-control; must stay well under `MaxFrameLen`. This is what plan/README means
  by "32 KB chunk streaming."
- **Dedup/delta block** = the unit whose **hash** is compared to decide
  send-or-skip. *This* is what "fixed vs CDC" is about.

This decision settles the **dedup/delta block** strategy (and notes the streaming
chunk falls out of it). The hard requirement that constrains every option:
whatever boundaries we choose **must be computed identically on Mac and Windows**,
or two peers disagree on blocks and lose chunk-level reuse / break convergence
reasoning (SR-5, SR-13; `cdc-chunking` §7 failure mode 5).

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option 1 — Whole-file transfer (no sub-file blocks)

- Correctness **5** (trivially convergent) · Concurrency **5** · Testability **5** ·
  Cross-platform **5**.
- **Cost:** re-sends the *entire* file for a one-byte change; zero intra/inter-file
  reuse. Fine for small files, wasteful for large mutable ones even on a LAN.
- **Not chosen** as the only mechanism, but it is the **degenerate case** of
  Option 2 (a file ≤ one block) and the correctness backstop (verify-after-
  reconstruct, below).

### Option 2 — Fixed-size content-addressed blocks (Syncthing-style), 32 KiB (CHOSEN, v1)

Split each file at fixed offsets `0, S, 2S, …` (`S = 32 KiB`); hash each block
(SHA-256); the receiver requests only the blocks whose hash it lacks, reusing any
block it already holds locally by hash.

- Correctness **5**: deterministic boundaries at byte offsets; no shared secret or
  per-peer parameter to coordinate ⇒ converges by construction (SR-5). The
  whole-file `content_hash` remains the final integrity check.
- Concurrency-safety **5**: cutting at offsets is stateless; no rolling hash state
  shared between goroutines.
- Testability **5**: trivial table-driven tests ("one byte changed ⇒ exactly the
  block(s) overlapping it differ; identical bytes ⇒ identical block set"); matches
  the WS-1/WS-4 acceptance phrasing.
- Cross-platform **5**: offsets are content positions only — nothing OS-specific,
  no `filepath`/Unicode interaction (SR-13/GR-12 untouched).
- **Cost (knowingly accepted):** the **boundary-shift** weakness — inserting/
  deleting a byte near the start shifts every later boundary, so a mid-file insert
  re-sends the tail (`cdc-chunking` §2, `rsync-or-librsync` DIFF-1). Acceptable for
  a 2-device personal LAN where common edits are in-place rewrites/appends and
  bandwidth is cheap — **this is exactly Syncthing's shipped choice and rationale**
  (BEP v1, accessed 2026-06-28: blocks "from 128 KiB … to 16 MiB … in steps of
  powers of two … constant in any given file, except for the last block";
  https://docs.syncthing.net/specs/bep-v1.html).

### Option 3 — rsync rolling-checksum delta (weak rollsum + strong hash, all-offset search)

- Correctness **4** but **wrong tool for a LAN**: rsync's *own* man page makes
  **whole-file copy the default** when bandwidth ≥ disk bandwidth, i.e. exactly the
  LAN regime (`rsync-algorithm` §11, https://download.samba.org/pub/rsync/rsync.1,
  accessed 2026-06-28). Its win is WAN bytes; its cost is a per-byte scan of both
  files + a stateful per-file signature→delta round-trip.
- Concurrency **3** (stateful per-file codec) · Testability **2** (rolling math,
  hashtable, short-final-block edge cases) · Cross-platform **5** (content-only).
- **Rejected:** its benefit is weakest in our common case, it does not map onto
  "block hash = tree leaf," and it adds a stateful codec against the project's
  simplicity posture (`rsync-or-librsync` DIFF-1/DIFF-2, N1/N2).

### Option 4 — Content-defined chunking (FastCDC, fixed shared Gear table) (ADAPT-LATER)

- Correctness **4**: converges *iff* every peer uses an identical algorithm + params
  + table. The **killer trap** is restic's *per-repo randomized polynomial* — two
  peers with different polynomials cut identical bytes differently and share **zero**
  chunks (`cdc-chunking` §4.2, §7 failure mode 5). Avoidable only by a *fixed shared*
  table baked into the protocol.
- Concurrency **5** (per-file local state) · Testability **3** (needs golden-vector
  boundary tests + distribution tests) · Cross-platform **5** (content-anchored).
- **Benefit:** insert/delete-shift resilience + best cross-file dedup + speed (~10×
  Rabin / ~3× Gear per FastCDC ATC'16). **Cost:** a new algorithm (~100 LOC + a
  256-entry table or a dependency → a logged GR-11 decision) and variable-size block
  metadata. **Deferred**, not rejected — it is the correct *upgrade path* if measured
  workloads need it, because it preserves the content-addressed-leaf model that
  rsync's delta breaks.

## Decision

Adopt **Option 2 — fixed-size content-addressed blocks at `S = 32 KiB`** as the v1
dedup/delta block. Concretely:

1. **Dedup block size = fixed 32 KiB** (`BlockSize = 32 * 1024`), a single tunable
   constant in `internal/reconcile` (or `internal/merkle` block helper). The last
   block of a file may be shorter. Each block is identified by its SHA-256.
2. **Streaming chunk = the same 32 KiB** carried in one `RESPONSE` frame (≪ the
   16 MiB `MaxFrameLen`), so a block maps 1:1 to a `REQUEST`/`RESPONSE` round and
   plan/README's "32 KB chunk streaming" is satisfied with no second size to tune.
3. **Local content-addressed reuse first:** before requesting a block over the
   network, look for a block with the same hash already on disk (another file, or
   the previous version of this file) and copy it locally (`syncthing-bep` §7.2).
4. **Reassemble into a temp file, then verify-before-commit:** stream blocks into a
   temp file on the destination filesystem; after the last block, **recompute the
   whole-file SHA-256 and assert it equals the leaf `content_hash` *before* the
   atomic rename** (AL-12; `rsync-algorithm` A1; `rsync-or-librsync` ADOPT-4; SR-3).
   Then `fsync` → `os.Rename` → parent-dir `fsync` (SR-1/SR-2). Discard the temp on
   any error; never touch `dst` until the verify passes.
5. **Forward-compat (mandatory): an `algo_version` / `chunking_scheme` field** in the
   INDEX / chunk-map message (`internal/protocol`), set to the v1 value (fixed-32KiB
   + SHA-256). A peer that receives an **unknown** value **fails closed** with a
   typed sentinel error (GR-6) rather than mis-chunking — the librsync versioned-magic
   discipline (`rsync-or-librsync` ADOPT-3). This is the mechanism that makes "lock
   32 KiB now, add CDC later" safe with no flag day.

## Rationale

- **Simplicity is a correctness property** at the data-integrity boundary: fixed
  offsets are deterministic, need no cross-peer coordination, and are trivially
  table-testable against the SR-5 acceptance criteria — the cheapest path to a
  provably-convergent v1 (`rsync-or-librsync` §4 input recommendation).
- **It mirrors our primary reference** (Syncthing ships fixed blocks for the
  simplicity / index-size tradeoff — BEP v1, accessed 2026-06-28), and needs **zero
  new dependencies** (GR-11), keeping the `GOOS=windows` cross-compile and the CI
  matrix simple.
- **It is content-position-only**, so it cannot reintroduce the NFD/NFC or separator
  hazards that threaten convergence (SR-13).
- **The LAN regime defangs the one weakness:** boundary-shift re-sends a tail, but
  on a 1 GbE+ link with content-addressed local reuse the cost is small and the
  simplicity is worth more (`rsync-algorithm` §11).

## Consequences

- Implements in `internal/reconcile/transfer.go` (request/stream/verify/atomic
  write) with block hashing helped by `internal/merkle/hash.go`
  (`structure.md:80,119`). The `algo_version` field lands in
  `internal/protocol/messages.go` (`message-type-codes.md` reserves codes `0x08+`
  and never renumbers; this is a payload field, not a new type).
- **Known caveat documented for the planner:** fixed 32 KiB produces ~512× more
  block records than adaptive for a 16 MiB file (`syncthing-bep` §7.3) — index/metadata
  bloat for **large** files. v1 accepts this (the README pilot posture). The
  **first** forward-compat step, if large files dominate, is **adaptive power-of-two
  block size** (Syncthing's "smallest size giving < 2000 blocks," BEP v1 accessed
  2026-06-28) — a strictly smaller change than CDC, still deterministic, still
  content-addressed, and reachable via the same `algo_version` field.
- **Adapt-later trigger for CDC (Option 4):** measured insert/delete-heavy or
  cross-file-duplicate workloads where fixed blocks waste real LAN bandwidth. If
  taken: FastCDC (not Rabin); a **single fixed Gear table compiled into the
  protocol** (never a randomized polynomial); normalized chunking; golden-vector
  cross-platform cut tests; a logged GR-11 dependency/vendoring decision
  (`cdc-chunking` §9.3).
- **Test obligations:** "same bytes ⇒ same block set / same `content_hash`";
  "one byte changed ⇒ only the overlapping block(s) differ"; killed-transfer mid-
  stream ⇒ no corrupt `dst`, temp discarded, re-run completes (SR-1); unknown
  `algo_version` ⇒ typed error, connection dropped, no mis-chunk.
- Cross-references: SR-1/2/3/5/12/13, GR-6/11, AL-8/12/13; literature
  `cdc-chunking`, `rsync-algorithm`, `rsync-or-librsync`, `syncthing-bep`; decisions
  `phase0/framing-format.md`, `phase0/message-type-codes.md`.
