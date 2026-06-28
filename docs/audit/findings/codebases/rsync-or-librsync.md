# Codebase map: rsync / librsync delta-encoding (signature · delta · patch)

- Phase: 1 (codebase-mapper)
- Reads-first: `docs/audit/rules/go-rules.md`
- Decision behind source choice:
  `docs/audit/decisions/codebases/rsync-vs-librsync-which-to-map.md`
- Purpose: study the real signature/delta/patch + rolling-checksum implementation
  to **inform** (not decide) the fixed-32 KiB-vs-content-defined-chunking choice
  owned by the Phase 2 merkle-researcher
  (`plan/agent_roster.md`; `docs/audit/findings/merkle/`).
- Primary source: **librsync** `librsync/librsync@master` (structure).
  Provenance: **rsync** `WayneD/rsync@master` (`tech_report.tex`, `match.c`).
  All upstream refs accessed **2026-06-28** at
  `github.com/librsync/librsync/blob/master/<path>#L<n>` and
  `github.com/WayneD/rsync/blob/master/<path>#L<n>`. Line numbers below are from
  those `master` files as fetched today.

---

## 0. TL;DR for the chunking decision

rsync/librsync do **not** use content-defined chunking. They split the *basis*
(old) file into **fixed-size blocks**, then find matches by sliding a **rolling
weak checksum one byte at a time** over the *new* file, testing **every byte
offset** against a hashtable of the basis blocks; a weak hit is confirmed by a
strong hash (rsync `tech_report.tex:75-96`, `:142-175`; librsync
`src/delta.c:137-148`). That byte-by-byte *search* — not content-defined
*boundaries* — is how rsync tolerates an inserted byte shifting all later data.

This gives Merkle Sync a **three-way** framing for §4, not a binary one:
(1) fixed-size content-addressed blocks (what Merkle Sync proposes at 32 KiB),
(2) rsync's fixed-block-signature + rolling-search delta, and
(3) content-defined chunking (FastCDC). See §4 for the scored comparison and the
input recommendation.

---

## 1. How librsync structures the relevant package

librsync is a transport-agnostic C library; its whole API surface is the codec,
which makes it a clean template for our `internal/reconcile` (and the
chunk/transfer parts of `internal/merkle`).

### 1.1 Three operations, three constructors, one driver

The public API exposes the codec as **three independent jobs** plus a generic
driver (`src/librsync.h`):

- `rs_sig_begin(block_len, strong_len, sig_magic)` — make a **signature** of a
  file (`librsync.h:466`).
- `rs_loadsig_begin(&sig)` then `rs_build_hash_table(sig)` — load a signature and
  index it for matching (`librsync.h:482`, `:487`).
- `rs_delta_begin(sig)` — make a **delta** of a new file against a loaded
  signature (`librsync.h:473`).
- `rs_patch_begin(copy_cb, copy_arg)` — **apply** a delta to a basis to produce
  the new file (`librsync.h:521`).
- All four return an `rs_job_t *` (`librsync.h:384`) cranked by
  `rs_job_iter(job, buffers)` (`librsync.h:400`) or `rs_job_drive(...)`
  (`librsync.h:410`).

Each job is a **state machine**: `struct rs_job` holds a current state-function
pointer `statefn` (`src/job.h:47`, `:56`) plus all codec state — the active
`signature` (`job.h:72`), the rolling `weak_sum` accumulator (`job.h:84`), the
delta scan window `scan_buf`/`scan_len`/`scan_pos` (`job.h:104-106`), the current
match `basis_pos`/`basis_len` (`job.h:117`), and the patch basis-fetch callback
`copy_cb` (`job.h:120`). Steps return `rs_result`:
`RS_DONE` / `RS_BLOCKED` / `RS_RUNNING` (`librsync.h:180-184`) so a step that
can't make progress (no input, or output full) yields cleanly — i.e. the codec
is decoupled from I/O and never blocks on a socket itself.

### 1.2 Signature generation = fixed-size blocks (`src/mksum.c`)

- Header first: emit `magic`, `block_len`, `strong_sum_len`
  (`mksum.c:54-56`; wire format in `doc/formats.md:27-31`).
- Then loop: read **exactly `block_len` bytes** and sum them
  (`mksum.c:95-96`), and when EOF leaves a short tail, take whatever remains via
  `rs_scoop_read_rest` (`mksum.c:99`). So blocks are fixed-size with a
  possibly-short **final** block → `ceil(input_len/block_len)` blocks
  (`doc/formats.md:36-38`).
- Per block, compute **both** sums and emit them (`rs_sig_do_block`,
  `mksum.c:67-85`): `weak_sum = calc_weak_sum(...)` then
  `calc_strong_sum(...)` then write `weak`(u32) + truncated `strong`
  (`mksum.c:73-76`). Each on-wire block sig is `u32 weak_sum` +
  `u8[strong_sum_len] strong_sum` (`doc/formats.md:49-52`).

### 1.3 Signature data structure + match (`src/sumset.h`, `src/sumset.c`)

- A block sig is just `{ weak_sum, strong_sum }` (`sumset.h:35-38`).
- A whole-file `rs_signature` carries `magic`, `block_len`, `strong_sum_len`,
  `count`, packed `block_sigs`, and a `hashtable *` for fast matching
  (`sumset.h:44-56`).
- The algorithm choice is encoded in `magic` and decoded lazily:
  `rs_signature_weaksum_kind()` → ROLLSUM vs RABINKARP, and
  `rs_signature_strongsum_kind()` → MD4 vs BLAKE2 (`sumset.h:114-125`).
- **Lazy strong-sum** is the key cost optimization: `rs_block_match_cmp`
  computes the expensive strong sum of the candidate **only when a weak hit
  forces it** (`if (match->buf) { ... calc_strong_sum ... }`, `sumset.c:67-73`)
  and counts how often that happens (`calc_strong_count++`, `sumset.c:69`), then
  `memcmp`s the strong sums (`sumset.c:75-76`). The generic open-addressing
  hashtable is instantiated for these types from a header template
  (`sumset.c:84-85`, `src/hashtable.h`).

### 1.4 Delta generation = rolling-search over the new file (`src/delta.c`)

This is the file that answers the chunking question. The block length used is
**dictated by the incoming signature**, not chosen by the delta side
(`delta.c:124`; file-header comment `delta.c:33-34`).

`rs_delta_s_scan` (`delta.c:122-162`) loops while a full block of input is
available (`delta.c:137`) and, at each position:

```c
if (rs_findmatch(job, &match_pos, &match_len)) {        // delta.c:139
    result = rs_appendmatch(job, match_pos, match_len); // emit COPY, jump fwd
    weaksum_reset(&job->weak_sum);                       // delta.c:142
} else {
    weaksum_rotate(&job->weak_sum,                       // delta.c:145-146
                   job->scan_buf[job->scan_pos],          //   byte leaving window
                   job->scan_buf[job->scan_pos+block_len]);//   byte entering window
    result = rs_appendmiss(job, 1);                       // advance ONE byte
}
```

- `rs_findmatch` (`delta.c:229-254`) computes the weak sum for the window if it
  doesn't have one (`delta.c:235-243`) and probes the signature hashtable via
  `rs_signature_find_match(sig, weaksum_digest, buf, len)` (`delta.c:250-252`).
- On a **match**: emit/extend a `COPY` and advance `scan_pos` by the whole
  `match_len` (`rs_appendmatch`, `delta.c:258-281`, esp. `:264-272`, `:274`),
  then **reset** the rolling sum (`delta.c:142`).
- On a **miss**: roll the weak sum forward by exactly **one byte**
  (`weaksum_rotate`, `delta.c:145-146`) and accumulate one literal byte
  (`rs_appendmiss`, `delta.c:288-299`). Misses are flushed in ~32 KiB segments to
  bound memory (`delta.c:287-288`, `MAX_MISS_LEN` `delta.c:102-103`).
- The **final short block** is special: short signature blocks don't store their
  length, so a tail can match any block with the same checksum and the emitted
  COPY uses the matched block's length (`delta.c:43-49`; flush path
  `rs_delta_s_flush` `delta.c:164-202`, `weaksum_rollout` `delta.c:187`).
- `rs_delta_begin` asserts the caller already built the hashtable
  (`delta.c:402-408`).

### 1.5 The rolling checksums (`src/rollsum.h`, `src/rabinkarp.h`)

- **rollsum** (classic rsync, adler-32-style): state is `{count, s1, s2}`
  (`rollsum.h:36-40`); `RollsumRotate(out,in)` updates in O(1) as the window
  slides (`rollsum.h:51-56`); `RollsumDigest = (s2<<16)|(s1&0xffff)`
  (`rollsum.h:72-75`); uses `ROLLSUM_CHAR_OFFSET 31` (`rollsum.h:33`). This is
  exactly the recurrence in rsync `tech_report.tex:118-133`.
- **rabinkarp** (added 2019, "better alternative" per `doc/formats.md:42`):
  polynomial rolling hash with `rotate`/`rollin`/`rollout` (`rabinkarp.h:71-90`);
  the multiplier rationale is documented inline (`rabinkarp.h:36-41`). It exists
  because the adler-style rollsum has weak bit distribution.
- Both sit behind one abstraction `weaksum_t` (a tagged union dispatched by an
  inlined switch, chosen over vtable pointers for speed — `src/checksum.h:46-62`,
  `:119-126`). Strong sums (MD4/BLAKE2) sit behind the parallel
  `strongsum_kind_t` (`checksum.h:40-44`).

### 1.6 Patch = apply a COPY/LITERAL/END command stream (`src/patch.c`)

- A delta is `RS_DELTA_MAGIC` + a stream of commands (`doc/formats.md:54-79`).
- The patch job is a state machine over the byte stream
  (`patch.c:42-47`): read a command byte (`rs_patch_s_cmdbyte`,
  `patch.c:51-67`), read its args (`rs_patch_s_params`, `patch.c:71-91`),
  dispatch (`rs_patch_s_run`, `patch.c:94-111`).
- `LITERAL(len)` copies `len` inline bytes through to output
  (`patch.c:114-130`).
- `COPY(pos,len)` fetches `len` bytes from the basis at `pos` via the
  application-supplied `copy_cb` callback (`patch.c:132-201`, callback invoked at
  `patch.c:175`). librsync never opens the basis itself — the caller owns that
  I/O (callback type `rs_copy_cb`, `librsync.h:502`).
- `END` (single null byte) terminates (`patch.c:101-103`,
  `doc/formats.md:78-79`).
- The patch job seeds an MD4 of its own output at construction
  (`rs_mdfour_begin(&job->output_md4)`, `patch.c:227`) — integrity of the
  reconstructed result.

### 1.7 rsync provenance (the original, for citing)

- `tech_report.tex:75-79` — "splits the file B into a series of non-overlapping
  **fixed-sized blocks of size S** ... The last block may be shorter."
- `:80-83` — per block, a "weak 'rolling' 32-bit checksum ... and a strong
  128-bit MD4 checksum."
- `:86-90` — α "searches through A to find all blocks of length S bytes
  (**at any offset, not just multiples of S**)" — the all-offsets rolling search.
- `:101-102` — "only requires one round trip" (β→α signature, α→β delta).
- `:135-140` — the weak sum is a cheap **first-level** screen; the strong sum,
  "much more expensive," is computed **only** where the weak sum matches.
- `:142-175` — the 3-level search: 16-bit tag hashtable → 32-bit rolling sum →
  strong sum.
- `:268-273` — evidence: false alarms were "< 1/1000 of the number of true
  matches"; each checksum pair is 20 bytes (4 weak + 16 MD4).
- `match.c:55-88` `build_hash_table`; `match.c:140-345` `hash_search`; the
  one-byte roll is `match.c:320-329` (trim leading byte, add trailing byte);
  strong confirm + `false_alarms` counter at `match.c:238-241`.
- `match.c:348-355`, `:411-426` — rsync also computes a **whole-file** MD4 and
  transmits it "as protection against corruption on the wire."

A note on block sizes: librsync's default block is **2048 bytes**
(`RS_DEFAULT_BLOCK_LEN`, `librsync.h:367`); rsync's report found 500–1000 good
(`tech_report.tex:77` footnote). These are **WAN** defaults chosen to maximize
match probability and minimize bytes; Merkle Sync's proposed **32 KiB** is far
larger, trading dedup granularity for a smaller index and higher LAN throughput
(see §4).

---

## 2. Patterns to ADOPT

### ADOPT-1 — Two-tier checksum: cheap weak filter gates the expensive strong hash; compute the strong hash lazily

The single most reused idea in the whole codec: never pay for the strong hash
unless a cheap pre-filter already matched. rsync states it directly
(`tech_report.tex:135-140`); librsync implements the laziness in
`rs_block_match_cmp` — the strong sum of a candidate is computed **only** when a
weak hit demands it (`sumset.c:67-73`), and false alarms are < 1/1000 of matches
(`tech_report.tex:268-273`).

Map to Merkle Sync: the per-block **SHA-256 is the Merkle leaf / transfer
identity** (already `content_hash` per `decisions/phase0/merkle-leaf-shape.md`).
The reusable lesson is the *gating discipline*:
- the scanner must use a **cheap pre-filter** (size + mtime in `FileInfo`) before
  re-hashing a file's bytes — re-hashing every file every scan is the slow path;
- if we ever add intra-file delta or cross-file dedup, gate the SHA-256 behind a
  weak rolling sum exactly as here.
This is concurrency-safe (pure functions over byte slices) and table-testable
(weak-collision / strong-confirm cases), satisfying go-rules' testability axis.

### ADOPT-2 — Signature / delta / patch as three independent, I/O-decoupled units driven as state machines

librsync keeps the three operations in separate files with separate constructors
(`rs_sig_begin`/`rs_delta_begin`/`rs_patch_begin`, `librsync.h:466/473/521`),
each a `statefn` state machine (`job.h:47-56`) that yields `RS_BLOCKED` rather
than blocking on I/O (`delta.c:131`, `:158`; `librsync.h:180-184`), and the patch
basis read is a **caller-supplied callback** so the codec owns no files or
sockets (`rs_copy_cb`, `librsync.h:502`; `patch.c:175`).

Map to Merkle Sync: structure `internal/reconcile` as three pure units —
**scan→hash** (produce block hashes / index), **diff→plan** (decide which blocks
to request), **apply→atomic-write** (reassemble received chunks) — each
consuming/producing via `io.Reader`/`io.Writer` and **never owning the TCP
conn**. The transport (`internal/transport`) feeds them. This is exactly the
go-rules GR-4 shape ("listeners do not call into each other ... communicate by
sending values on channels to the reconcile core") and the GR-5 rule
("never perform network or disk I/O while holding the lock"): codec steps operate
on copied-out buffers, the socket lives elsewhere. It also makes each unit
independently table-testable (feed bytes via `iotest` readers, assert output),
the testability win the contract grades.

### ADOPT-3 — Versioned magic-number header for algorithm agility; fail-closed on unknown

Every librsync artifact starts with a `u32` magic that encodes **which** weak and
strong algorithm it uses, and new algorithms were added as **new magics** without
breaking old readers: `RS_MD4_SIG_MAGIC`, `RS_BLAKE2_SIG_MAGIC`,
`RS_RK_MD4_SIG_MAGIC`, `RS_RK_BLAKE2_SIG_MAGIC` (`librsync.h:82-109`), decoded by
`rs_signature_weaksum_kind`/`_strongsum_kind` (`sumset.h:114-125`). The format
spec is explicit and **fail-closed**: "newer file types are not supported by
older versions ... Older librsync versions will immediately fail with an error
when they encounter file types they don't support" (`doc/formats.md:13-21`).

Map to Merkle Sync — **this is the mechanism that makes "lock 32 KiB now, defer
CDC" safe**: put an explicit `chunking_scheme` / `algo_version` field in the
INDEX / chunk-map message (`internal/protocol`) so the chunking strategy and hash
function can evolve (fixed-32 KiB+SHA-256 → CDC later) **without a flag day**, and
**reject an unknown version with a typed sentinel error** rather than
misinterpreting bytes (go-rules GR-6: sentinels + `errors.Is`, and GR-7's
"don't trust the bytes a peer sent"). Cross-references the existing
`decisions/phase0/message-type-codes.md` and `framing-format.md`.

### ADOPT-4 — Whole-object strong checksum as end-to-end corruption protection, verified before commit

Beyond per-block sums, rsync computes a **whole-file** MD4 and sends it "as
protection against corruption on the wire" (`match.c:348-355`, `:411-426`); the
patch side tracks an MD4 of its reconstructed output (`patch.c:227`).

Map to Merkle Sync: after reassembling a file from received fixed blocks,
**recompute the whole-file SHA-256 and compare it to the expected leaf
`content_hash` before the temp→atomic-rename commit** (sync-rules SR-1: "never
write directly to a destination — temp + atomic rename"). Per-block hashes give
per-chunk integrity; the final whole-file check catches reassembly/ordering bugs
and truncation before anything replaces the user's file. Cheap insurance for a
no-data-loss engine.

---

## 3. What Merkle Sync deliberately does DIFFERENTLY (simpler)

### DIFF-1 (primary) — No signature/delta/patch round-trip and no per-file rolling-checksum search; the Merkle tree of content-addressed blocks is the source of truth

rsync/librsync are **base-relative, pairwise, stateful** delta codecs: one side
must already hold an old version of the file, ship a per-file **signature**, and
the other runs a **byte-by-byte rolling search** to re-align against it before
emitting a COPY/LITERAL instruction stream (`tech_report.tex:75-96`;
`delta.c:137-148`; `match.c:140-345`). That machinery exists to win **WAN**
bandwidth on a low-bandwidth, high-latency link (the paper's stated assumption,
`tech_report.tex:14-25`).

Merkle Sync instead makes a **Merkle tree of fixed-size, content-addressed block
hashes** the source of truth (`decisions/phase0/merkle-leaf-shape.md`): peers
exchange index/tree-node hashes, diff in O(log n) by recursing only into
mismatching branches, and **request whole blocks by hash**. Consequences of the
difference, all simplifying:
- **No rolling checksum, no signature exchange, no COPY/LITERAL codec, no basis
  file required** — block hashes are self-identifying and compared directly, so
  "what differs" is a set-difference of hashes, not a delta computation.
- **Stateless comparability** — two peers converge iff their root hashes match;
  there is no per-file negotiation state to manage.
- **Fits LAN** — bandwidth is cheap and round-trips/latency + implementation
  simplicity dominate, so we decline rsync's sub-block byte savings.

The tradeoff we **knowingly accept**: with *fixed* blocks and *no* rolling
search, inserting one byte near the start of a file shifts every later block
boundary, changing all subsequent block hashes and re-sending the file's tail —
precisely the weakness rsync's rolling search **and** CDC both avoid (by search
and by content-defined boundaries respectively). Quantifying and deciding whether
that matters on a LAN is exactly the §4 hand-off to the merkle-researcher.

### DIFF-2 (scope) — Adopt the algorithm shape, not the deployment machinery

rsync bundles a full client/server daemon, remote-shell launching, protocol
multiplexing, and a file-list protocol (`clientserver.c`, `io.c`, `flist.c`,
`sender.c`/`receiver.c`/`generator.c`); librsync is "just the library."
Merkle Sync is deliberately narrower than even rsync-the-tool's transport: **LAN-
only UDP multicast discovery, no central/global discovery server, no relay / NAT
traversal, no GUI** (`plan/README.md` Stack + deferral list; roster
"LAN-only multicast, no global discovery server"). We take the codec/data-model
lessons (§2) and leave the daemon/relay machinery out of scope.

---

## 4. Framing for the fixed-32 KiB-vs-CDC decision (input to merkle-researcher)

> This section is **evidence + an input recommendation only.** The binding
> chunking decision is the Phase 2 merkle-researcher's, logged under
> `decisions/phase2/` with `findings/merkle/` (per `plan/agent_roster.md`).

The real design space is **three** options, distinguished by *how block
boundaries are chosen* and *whether matching needs a basis + search*:

| dimension | (1) Fixed-size content-addressed blocks (Syncthing / Merkle; **Merkle Sync's proposal @ 32 KiB**) | (2) rsync/librsync: fixed-block signature + **rolling search** | (3) Content-defined chunking (FastCDC / CDC) |
|---|---|---|---|
| Boundary placement | fixed byte offsets (every 32 KiB) | fixed in basis; new file scanned at **every** offset | **content-defined** (cut when rolling hash hits a pattern) |
| Insert-1-byte behavior | shifts all later boundaries → all later block hashes change → re-send tail | tolerated: rolling search re-aligns (`delta.c:145-146`) | tolerated: only the local chunk changes; later chunk hashes stable |
| Needs a basis / old version? | **no** — compare hash lists | **yes** — delta is relative to a basis | no — chunk hashes are content-addressed |
| Round-trips per file | none beyond index diff | a signature→delta exchange (`tech_report.tex:101-102`) | none beyond index diff |
| CPU cost | low (hash fixed blocks once) | medium/high (rolling sum over **every** byte of the new file) | medium (rolling hash over every byte to *find cuts*) |
| Dedup / sub-file reuse | coarse (whole-block, alignment-sensitive) | fine (byte-granular within a file) | fine (shift-resistant, cross-file) |
| Merkle-tree fit | **excellent** — block hash = leaf, fixed fan-out | poor — COPY/LITERAL stream isn't a tree of stable leaves | good but variable-size blocks complicate fixed fan-out / tree shape |
| Complexity & testability | **lowest** — deterministic, trivial table tests (same bytes → same blocks) | highest — stateful codec, rolling math, hashtable, edge cases (short final block, `delta.c:43-49`) | medium — needs CDC params (min/avg/max, normalization), harder determinism tests |
| LAN suitability | **high** (bandwidth cheap; simplicity + no round-trips win) | low (its wins are WAN-bandwidth; round-trip + state cost) | medium (robustness helps large/edited files; added CPU+complexity) |

**Input recommendation (non-binding):**

1. **Pick (1) fixed-size content-addressed blocks for v1**, at the proposed
   32 KiB. It is the simplest, is deterministic and trivially table-testable
   ("scan the same folder twice → identical root hash" — the WS-1 acceptance
   criterion in `plan/agent_roster.md`), and maps 1:1 onto the Merkle leaf model
   already decided. On a LAN, re-sending a file's tail after an early insert is
   cheap; the simplicity and the absence of per-file round-trips are worth more.
2. **Do NOT adopt rsync's rolling-search delta machinery.** Its benefit is WAN
   byte savings under a basis+signature round-trip; that doesn't map onto
   "block hashes are tree leaves," adds a stateful codec and an extra round-trip,
   and is the opposite of the project's simplicity posture (DIFF-1, DIFF-2).
3. **Reserve the escape hatch via ADOPT-3:** put an explicit
   `chunking_scheme`/`algo_version` field in the INDEX/chunk-map message so
   **CDC (3) can be introduced later without a flag day**, fail-closed on unknown
   versions. If real workloads later show many large, edited-in-place files where
   the insert-shift re-send hurts, CDC — not rsync rolling search — is the
   upgrade path, because CDC preserves the content-addressed-leaf model that
   rsync's delta breaks.

This matches the roster's own suggested tightening ("lock chunking to fixed 32KB,
defer CDC", `plan/agent_roster.md` final section), now grounded in the real
source.

---

## 5. Evidence index (file:line)

librsync (`github.com/librsync/librsync/blob/master/`, accessed 2026-06-28):
- `src/librsync.h:71-109` magics; `:180-184` rs_result; `:367` default block
  2048; `:384-410` job/iter/drive; `:466-521` begin fns + build_hash_table +
  copy_cb.
- `src/job.h:47-124` job state-machine struct (statefn, scan_*, basis_*,
  weak_sum, copy_cb).
- `src/mksum.c:54-56` sig header; `:67-85` per-block weak+strong; `:88-108` fixed
  block_len read + short tail.
- `src/sumset.h:35-56` block sig + signature struct; `:84-86` find_match;
  `:114-125` algo-from-magic. `src/sumset.c:63-77` lazy strong-sum + memcmp;
  `:84-85` hashtable instantiation.
- `src/delta.c:33-49` design + short-final-block; `:122-162` scan loop; `:137`
  block-available guard; `:139-148` match/miss; `:145-146` byte-by-byte rotate;
  `:229-254` findmatch; `:258-299` append match/miss; `:287-288` 32 KiB miss
  segmentation; `:396-410` delta_begin requires hashtable.
- `src/rollsum.h:33,36-40,51-56,72-75` classic rolling sum.
- `src/rabinkarp.h:36-41,55-95` polynomial rolling sum ("better alternative").
- `src/checksum.h:34-44,46-62,119-126` weak/strong abstraction (tagged union).
- `src/patch.c:42-47,51-201` command state machine (LITERAL/COPY/END); `:175`
  copy_cb; `:227` output MD4.
- `doc/formats.md:13-21` magic/fail-closed; `:27-52` signature wire format;
  `:54-79` delta command format.

rsync (`github.com/WayneD/rsync/blob/master/`, accessed 2026-06-28):
- `tech_report.tex:14-25` WAN assumption; `:75-96` fixed blocks + all-offset
  search; `:101-102` one round trip; `:109-140` rolling checksum + weak-screens-
  strong; `:142-175` 3-level search; `:268-273` false-alarm/size evidence.
- `match.c:55-88` build_hash_table; `:140-345` hash_search; `:238-241` strong
  confirm + false_alarms; `:320-329` one-byte roll; `:348-355,411-426` whole-file
  MD4 corruption check.

Tree listings (accessed 2026-06-28):
`api.github.com/repos/librsync/librsync/git/trees/HEAD?recursive=1`,
`api.github.com/repos/WayneD/rsync/git/trees/HEAD?recursive=1`.
