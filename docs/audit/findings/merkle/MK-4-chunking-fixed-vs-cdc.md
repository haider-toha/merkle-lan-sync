# Finding MK-4 — Sub-file chunking: fixed 32 KiB blocks for v1, CDC as a forward-compatible upgrade

- Slug: `MK-4-chunking-fixed-vs-cdc`
- Phase / role: Phase 2 — merkle-researcher
- Status: complete; backs `decisions/merkle/chunking-fixed-32kib-vs-cdc.md`.
  **Implemented in WS-4** (`internal/reconcile/transfer.go`): fixed 32 KiB
  content-addressed blocks (`BlockSize`), local content-addressed reuse before any
  network fetch, and the whole-file verify-after-reconstruct before an atomic
  temp→fsync→rename→dir-fsync (`atomicWriteVerify`); tested by `reconcile_test.go`
  (`TestNumBlocks`, `TestAtomicWriteVerify_*`, `TestRename_NoNetworkTransfer`) +
  integration `TestBackpressure_BidirectionalConverges`. Commit
  `af12de099165f38e11556555acc986b9ba385f24`.
- Severity: **medium** (the v1 choice is low-risk; the cross-peer-determinism
  requirement for any future CDC is high-stakes but deferred; the large-file
  index-bloat caveat is real)
- Date / access date for all URLs: 2026-06-28
- Reads-first honoured: `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/literature/{cdc-chunking,rsync-algorithm}.md`,
  `docs/audit/findings/codebases/rsync-or-librsync.md`,
  `docs/audit/decisions/phase0/{framing-format,message-type-codes}.md`

## Claim

For v1, **fixed-size content-addressed blocks (32 KiB)** are the right sub-file
transfer unit: deterministic, no cross-peer coordination, trivially testable, and
identical on Mac/Windows — exactly Syncthing's shipped tradeoff. Content-defined
chunking (CDC) is **deferred, not rejected**: it is the correct upgrade path *iff*
measured workloads need insert/delete-shift resilience, and only with a **fixed
shared** table (never a randomized polynomial). A mandatory `algo_version` field
makes that upgrade a no-flag-day change. The "streaming chunk" and the "dedup block"
are **two different things** and must not be conflated.

## Evidence

### Fixed blocks are the proven LAN choice

Syncthing — our primary reference — ships **fixed** blocks, chosen by file size, not
CDC (verified first-hand, BEP v1, https://docs.syncthing.net/specs/bep-v1.html,
accessed 2026-06-28): *"File data is described and transferred in units of blocks,
each being from 128 KiB … to 16 MiB in size, in steps of powers of two … constant in
any given file, except for the last block which may be smaller,"* and *"the desired
block size … is the smallest block size that results in fewer than 2000 blocks."* The
maintainer's rationale: the size "felt like a reasonable trade off between data to
shuffle and metadata to track" (calmh, Syncthing forum,
https://forum.syncthing.net/t/128-kib-block-size-choice/3128, accessed 2026-06-28;
`cdc-chunking` §6). rsync's *own authors* default to **whole-file** copy when
bandwidth ≥ disk bandwidth — the LAN regime (`rsync-algorithm` §11,
https://download.samba.org/pub/rsync/rsync.1, accessed 2026-06-28), so rsync-style
rolling delta (`cdc-chunking` Option 4 / `rsync-or-librsync` DIFF-1) is the wrong
tool here.

### The accepted weakness, and CDC's exact fix

Fixed blocks suffer **boundary shift**: *"if a user adds a byte to the beginning of
the file … every chunk in the file"* changes (restic,
https://restic.net/blog/2015-09-12/restic-foundation1-cdc/, accessed 2026-06-28;
`cdc-chunking` §2). CDC anchors boundaries to a rolling hash of content so *"unmodified
data … will have the same boundaries"* (Wikipedia, *Rolling hash*,
https://en.wikipedia.org/wiki/Rolling_hash, accessed 2026-06-28); FastCDC (USENIX
ATC'16) is ~10× faster than Rabin with near-equal dedup. On a 2-device LAN with
content-addressed local reuse, the tail re-send after an early insert is cheap, so
the simplicity of fixed blocks wins for v1 (`rsync-or-librsync` §4 input
recommendation).

### The killer trap for any future CDC: cross-peer determinism

restic draws a **random** polynomial **per repository** (anti-fingerprinting); two
peers with different polynomials compute different boundaries on identical bytes →
**zero** chunk reuse and broken convergence reasoning (`cdc-chunking` §4.2, §7
failure mode 5, restic `polynomials.go`/`doc.go`, accessed 2026-06-28). **Any CDC
Merkle Sync ever adopts MUST use a single fixed shared table compiled into the
protocol** (our trust model — TLS LAN peers — makes the anti-fingerprint randomization
unnecessary; SR-5/SR-13).

### Disambiguation: streaming chunk vs dedup block

`cdc-chunking` §9.1: a **transfer/streaming chunk** (bytes per `RESPONSE` frame, pure
flow-control, ≤ `MaxFrameLen`) is independent of a **dedup/delta block** (the unit
whose hash decides send-or-skip). "Fixed 32 KiB vs CDC" is only about the latter;
plan/README's "32 KB chunk streaming" is the former. v1 sets **both to 32 KiB** so a
block maps 1:1 to a frame (32 KiB ≪ 16 MiB `MaxFrameLen`).

### Forward-compat: the versioned-magic discipline

librsync's versioned magic lets new algorithms ship as new magics while old readers
**fail closed** (`doc/formats.md`, accessed 2026-06-28; `rsync-or-librsync` ADOPT-3).
Mirror it: an `algo_version`/`chunking_scheme` field in the INDEX/chunk-map message,
v1 = fixed-32KiB+SHA-256; an unknown value is a typed sentinel error and a dropped
connection, never a mis-chunk (GR-6). This is what makes "lock 32 KiB now, add CDC
later" safe.

### Integrity backstop regardless of chunking

After reassembling from blocks, **recompute the whole-file SHA-256 and assert it
equals the leaf `content_hash` before the atomic rename** (rsync's mandatory
whole-file verify is the prior art — `rsync-algorithm` A1,
https://download.samba.org/pub/rsync/rsync.1, accessed 2026-06-28; AL-12; SR-3). This
catches reassembly/ordering/truncation bugs before anything replaces the user's file.

## The known caveat (why severity is medium, not low)

Fixed 32 KiB produces ~512× more block records than adaptive for a 16 MiB file
(`syncthing-bep` §7.3) — index/metadata bloat for **large** files. v1 accepts this
per the README pilot posture; the **first** forward-compat step (before CDC) is
**adaptive power-of-two** block size (Syncthing's "< 2000 blocks" rule), a smaller,
still-deterministic, still-content-addressed change reachable via the same
`algo_version` field.

## Recommendation / impact

- **DECISION:** fixed 32 KiB blocks for v1, defer CDC; see
  `decisions/merkle/chunking-fixed-32kib-vs-cdc.md` for the scored options and the
  forward-compat plan.
- **Implementers:** `internal/reconcile/transfer.go` (+ `internal/merkle/hash.go`);
  the `algo_version` field in `internal/protocol/messages.go`.
- **Test obligations:** "same bytes ⇒ same block set"; "one byte changed ⇒ only the
  overlapping block(s) differ"; killed-transfer ⇒ no corrupt `dst` + temp discarded
  (SR-1); unknown `algo_version` ⇒ typed error, no mis-chunk.
- **Cross-refs:** SR-1/2/3/5/12/13, GR-6/11; AL-8/12/13; literature `cdc-chunking`,
  `rsync-algorithm`, `rsync-or-librsync`, `syncthing-bep`.
