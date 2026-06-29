# Decision (WS-4): chunked transfer, verify-after-reconstruct + atomic apply, REQUEST validation

- Area: ws4 / internal/reconcile (transfer.go, apply.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-4 implementer
- Plan items: WS-4 #3 (killed mid-stream ⇒ no corrupt file), #9 (REQUEST validated;
  source declines cleanly).
- Reads-first: `decisions/merkle/chunking-fixed-32kib-vs-cdc.md` (fixed 32 KiB
  content-addressed blocks; verify-after-reconstruct before the atomic rename; the
  `algo_version`/featureFlags fail-closed hook), MK-4, CDD-2 (`MaxChunkLen`; REQUEST
  validation + clean decline with `RESPONSE{GENERIC, empty}`), SR-1/SR-2 (temp →
  `tmp.Sync()` → `os.Rename` → parent-dir fsync; discard temp on any error, never
  touch dst until verify passes), SR-3 (idempotent content-addressed apply),
  `findings/antipatterns/{no-verify-after-reconstruct,windows-rename-not-atomic,
  change-during-hash-transfer}.md`, the `protocol` framing budget
  (`MaxFrameLen`/`MaxChunkLen`/`ResponseHeaderLen`, `Request`/`Response`).

## Context

After the diff+resolver decides a path must hold content with whole-file hash `H`,
the engine must move `H`'s bytes and commit them without ever leaving a corrupt or
half-written file at `dst` — even if the process is killed mid-transfer (criterion 3).
The wire already provides `REQUEST{reqID,path,content_hash,offset,length}` /
`RESPONSE{reqID,code,data}` framed under a 16 MiB `MaxFrameLen`, with
`MaxChunkLen = MaxFrameLen - 1 - 5`. Two sub-questions: (1) how to reconstruct +
commit safely, and (2) how the *source* validates a REQUEST so a malformed/oversized
one neither over-allocates nor wedges the puller (CDD-2).

## Options — reconstruct + commit (scored: correctness / concurrency / testability / cross-platform)

### Option R1 — stream chunks straight into `dst` (in-place)
- correctness **1** (DISQUALIFIER) — a kill mid-stream leaves a truncated/garbage
  `dst`; readers see a half-file; the antipattern `no-verify-after-reconstruct` +
  SR-1 forbid it outright. Rejected.

### Option R2 — temp file in dst's dir → verify whole-file SHA-256 == H → fsync → rename → dir fsync (CHOSEN)
Reassemble 32 KiB blocks into `os.CreateTemp(dir, ".msync-*.tmp")` on the **same
filesystem** as `dst`; after the last block recompute the whole-file SHA-256 and
assert it equals the leaf `content_hash` **before** the rename; on success
`tmp.Sync()` → `os.Rename(tmp,dst)` → fsync the parent dir; on **any** error discard
the temp and leave `dst` untouched; a re-run completes.
- correctness **5** — atomic swap (a reader sees old-complete or new-complete, never
  partial); verify-before-commit catches reassembly/ordering/truncation/corruption
  before the user's file is replaced (MK-4 integrity backstop, rsync prior art);
  kill-safety by construction (the temp is the only thing a crash can damage).
- concurrency-safety **5** — the temp is per-transfer; the `FileInfo`-map mutation +
  expected-hash record happen under the engine lock *after* a successful commit, off
  the I/O path.
- testability **5** — `atomicWriteVerify(dir,dst,H,src)` is a pure-ish function
  testable with an in-memory/short reader; a "kill after K bytes" test asserts dst
  absent-or-previous + no leftover temp + re-run completes.
- cross-platform **5** — `os.CreateTemp`+`os.Rename` in the same dir is the portable
  atomic-write idiom. **Caveat (SR-2):** Windows `os.Rename` over an existing file is
  not POSIX-atomic; Go's `os.Rename` uses the replace path, verified on real Windows
  in Phase 6. The verify-before-commit makes even a non-atomic replace safe (we only
  rename verified bytes).

### Option R3 — write temp, rename, THEN verify (verify-after-commit)
- correctness **2** — a failed verify means `dst` is already replaced with bad bytes;
  recovery requires a rollback copy. Strictly worse than R2. Rejected.

## Options — REQUEST validation on the source (CDD-2)

### Option Q1 — trust the REQUEST, read `[offset,offset+length)` blindly
- correctness **1** — `length` up to 4 GiB over-allocates / OOMs the source; an
  out-of-range offset errors mid-read. Rejected.

### Option Q2 — validate then decline cleanly with `RESPONSE{GENERIC, empty}`, keep the conn alive (CHOSEN)
Reject `length == 0 || length > MaxChunkLen`, and `offset > size || offset+length >
size` against the file's **current** size; on any failure reply
`RESPONSE{reqID, CodeGeneric, nil}` and **keep the connection** (a bad request from a
buggy/hostile peer is not fatal). The puller treats a `CodeGeneric`/`CodeNoSuchFile`
RESPONSE as "abort this file, leave dst untouched." The puller itself **clamps**
`length ≤ MaxChunkLen` and splits large ranges, so a conformant peer never trips this.
- correctness **5** — bounded allocation; the source never wedges; SR-12/CDD-2
  satisfied; matches the `protocol` budget exactly.
- concurrency-safety **5** — the size check reads a fresh `os.Stat`; the read is in
  the per-peer server goroutine, off the loop.
- testability **5** — `TestRequest_OversizeDeclinedConnSurvives` sends
  `length = MaxFrameLen` and asserts a `GENERIC` decline + the conn still serves a
  subsequent valid request.
- cross-platform **5**.

### Option Q3 — drop the peer on a bad REQUEST
- correctness **3** — over-reacts (a transient race where the file shrank between
  advertise and request becomes a disconnect); CDD-2 explicitly wants a *clean
  decline*, not a drop. Rejected.

## Decision

**R2 + Q2.** Fixed **32 KiB** content-addressed blocks (`BlockSize = 32*1024`); a
block maps 1:1 to one `REQUEST`/`RESPONSE`. The puller tries **local
content-addressed reuse** first (copy from any on-disk file whose `content_hash` ==
`H`, incl. the path's previous version — MK-4 §3 / PR-5 zero-network rename), then
fetches missing blocks over the wire stop-and-wait. Reassemble into a temp on dst's
filesystem, **verify whole-file SHA-256 == H before the rename**, then `tmp.Sync()` →
`os.Rename` → parent-dir fsync; discard the temp and leave dst untouched on any error.
The source **validates every REQUEST** (length∈(0,MaxChunkLen], range within current
size) and **declines cleanly** with `RESPONSE{GENERIC, empty}` on failure, keeping the
connection; the puller clamps `length ≤ MaxChunkLen`. An apply whose incoming
`content_hash` (+ VV) already equals the local leaf is a **no-op** (SR-3 idempotence).

## Rationale

- Verify-before-commit + atomic rename is the only option that is kill-safe *and*
  catches reconstruction corruption before the user's file is touched — the SR-1/SR-2
  + MK-4 contract, which is the prime directive at the data-integrity boundary.
- Clean decline (not drop) is exactly CDD-2; it keeps a transient size race or a buggy
  peer from costing a reconnect, while the budget clamp keeps conformant transfers
  from ever tripping it.
- 32 KiB ≪ 16 MiB `MaxFrameLen`, so a block is always one frame; no second size to
  tune (chunking decision §2).

## Consequences

- Drives `transfer.go` (`blocksFor(size)`, local reuse, stop-and-wait pull, server
  read+validate, `atomicWriteVerify`) and `apply.go` (idempotence check, post-commit
  `FileInfo`-map + expected-hash update under the lock).
- A failed verify or a declined REQUEST aborts *that file* only; the next index
  exchange / rescan re-attempts it — convergence is at quiescence (CDD-8).
- `algo_version`/featureFlags negotiation is carried by HELLO `FeatureFlags`; v1 is
  the implicit fixed-32KiB+SHA-256 scheme. An unknown future scheme is a fail-closed
  drop (GR-6) — reserved, not implemented in v1.
- Windows non-atomic `os.Rename` replace semantics under kill-9 → Phase 6 real-Windows
  check (SR-2 caveat); the Mac/APFS atomic path is the unit-test gate here.
- Cross-refs: SR-1/2/3/12, CDD-2, MK-4, PR-5; `protocol` framing budget.
