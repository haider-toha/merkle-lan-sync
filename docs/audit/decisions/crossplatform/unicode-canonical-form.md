# Decision: Unicode canonical form = NFC, normalised at scan time (and on receive)

- Area: crossplatform / pathnorm (confirms XP-2)
- Status: **decided** (Phase 2 — crossplatform-researcher owns and confirms the
  preliminary XP-2 rule)
- Date: 2026-06-28
- Decider: crossplatform-researcher
- Confirms / supersedes: `docs/audit/rules/crossplatform-rules.md` XP-2
  ("preliminary — confirm in Phase 2"); cross-refs SR-5, SR-13, GR-12, the leaf
  shape (`decisions/phase0/merkle-leaf-shape.md`) and BEP failure mode #6
  (`findings/literature/syncthing-bep.md` §10.6 — "filenames are raw bytes, BEP
  does NOT normalize").

## Context

The same logical filename can have two different byte representations: composed
(NFC, `é` = U+00E9) and decomposed (NFD, `e` + U+0301). The convergence oracle
SR-5 ("equal root hash ⇔ converged") and the canonical-identity rule SR-13 require
that the *same logical file* hashes to the *same bytes* on Mac and Windows. If one
peer keys a file as NFC and the other as NFD, the diff sees **two leaves**, the
roots never converge, and the file is endlessly re-sent.

What actually happens per platform (this is the load-bearing evidence, not folk
memory):

- **macOS / APFS is normalization-PRESERVING, not normalizing.** Reproduced with
  Go 1.26.4 on this machine's APFS volume (`scratchpad/normprobe/main.go`):
  - a file created with an **NFC** name is returned by `os.ReadDir` as **NFC**
    bytes (`72 c3 a9 73 75 6d c3 a9 2e 74 78 74`);
  - a file created with an **NFD** name is returned by `os.ReadDir` as **NFD**
    bytes (`72 65 cc 81 73 75 6d 65 cc 81 2e 74 78 74`).
  So the scanner cannot assume the OS hands back a consistent form — it returns
  *whatever an application wrote*. (This is the APFS change from HFS+, which used
  to force everything to NFD on disk. "APFS is normalization-preserving and
  normalization-insensitive by default … you can save and access files using
  either NFC or NFD and it'll find files as expected", "filenames are still
  stored … not normalized like with HFS+", APFS uses "normalization-insensitive
  hashes" — [Michael Tsai, *APFS Native Normalization*](https://mjtsai.com/blog/2017/06/27/apfs-native-normalization/),
  [The Eclectic Light Co., *Unicode, normalization and APFS*](https://eclecticlight.co/2021/05/08/explainer-unicode-normalization-and-apfs/),
  [Apple APFS FAQ](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/FAQ/FAQ.html),
  all accessed 2026-06-28.)
- **macOS lookup is normalization-INSENSITIVE.** In the same probe, after creating
  the file as NFC, `os.Open` succeeded via *both* the NFC and the NFD name; and
  creating *both* forms in one directory produced **1** entry (the second write
  hit the first file). So on APFS the two forms cannot coexist.
- **Windows/NTFS and Linux/ext4 are normalization-SENSITIVE** (they store and
  compare raw code units, no normalization), so NFC and NFD *are* two different
  files there and *can* coexist. (BEP itself does no normalization — names are raw
  bytes — `findings/literature/syncthing-bep.md` §10.6, accessed 2026-06-28.)

So: we must pick one canonical form for the tree/wire, and we must pick **where**
the normalisation happens.

## Options (scored 1–5, 5 = best)

### Option A — store raw bytes, no normalisation (BEP "bag of bytes")
- Correctness: **1** — an NFC peer and an NFD peer produce different keys for the
  same file → SR-5 never converges; this is exactly BEP failure-mode #6.
- Concurrency-safety: 5 (nothing to do). Testability: 4. Cross-platform: **1**.
- Rejected: defeats the entire Mac↔Windows requirement.

### Option B — canonical **NFD**
- Correctness: **3** — internally consistent, but you must decompose every name
  for the *majority* of the world (Windows + Linux are NFC-leaning) and re-impose
  NFD on the wire; more churn, more surprise.
- Cross-platform: **2** — fights Windows/Linux conventions; a Windows user who
  types `résumé.pdf` (NFC) sees the engine prefer the decomposed form.
- Rejected: the majority of peers and tools expect NFC.

### Option C — canonical **NFC**, normalise at **scan time + on receive** (PROPOSED)
- Normalise each path component to NFC (`golang.org/x/text/unicode/norm`,
  `norm.NFC.String`) **when reading it into the tree** and **when accepting a
  FileInfo from a peer**. The canonical key, the structural hash input, and the
  wire form are always NFC. The *raw on-disk name from `os.ReadDir`* is retained
  alongside the canonical key and used for every filesystem call.
- Correctness: **5** — one form everywhere ⇒ SR-5/SR-13 hold; matches the
  Windows/Linux majority; idempotent (`norm.NFC.IsNormalString` is testable).
- Concurrency-safety: **5** — normalisation is a pure function of the name; no
  shared state.
- Testability: **5** — table-driven NFC/NFD round-trip unit tests run on the Mac
  (plan/README "Unicode NFC/NFD normalisation unit tests"); `norm` ships Unicode
  15.0.0 tables, deterministic across OSes.
- Cross-platform: **5** — NFC is the de-facto canonical on Windows/Linux; on
  APFS, lookup is normalization-insensitive so an NFC key still opens an NFD-on-disk
  file, and we additionally keep the raw name so it works on the *sensitive*
  filesystems too.

### Option D — canonical **NFKC** (compatibility composition)
- Correctness: **2** — NFKC is **lossy**: it folds ﬁ (U+FB01) → `fi`, ① → `1`,
  full-width → half-width, etc. Two genuinely distinct filenames would collapse to
  one canonical key → silent merge / data loss.
- Rejected: compatibility folding changes the *meaning* of names; never use it for
  filename identity.

## Decision

Adopt **Option C: NFC is the canonical Unicode form**, applied **at scan time and
on receive from a peer**. Two refinements pinned now:

1. **Keep the raw `os.ReadDir` name alongside the canonical NFC key**, and use the
   *raw* name for every filesystem operation (open / read / rename / delete). The
   canonical NFC key is an **identity/hash key only**. Rationale from the probe:
   APFS is normalization-preserving (the on-disk bytes may be NFD even though our
   key is NFC) and NTFS/ext4 are normalization-sensitive (the NFC key may not open
   an NFD-on-disk file). Using the raw name for I/O makes the design correct on
   *all four* filesystems instead of leaning on APFS's insensitive lookup. This
   sharpens XP-2, which previously said to keep the on-disk form "if needed to
   re-open the file on macOS" — it is needed generally, not only on macOS.
2. **`golang.org/x/text/unicode/norm` is an approved dependency** (logged here as
   the GR-11 "any further dependency needs a logged decision" exception, alongside
   `fsnotify`). It is pure-Go, ships its own Unicode tables (v0.38.0 / Unicode
   15.0.0, accessed 2026-06-28), and is the standard tool;
   [pkg.go.dev/golang.org/x/text/unicode/norm](https://pkg.go.dev/golang.org/x/text/unicode/norm).
   API used: `norm.NFC.String(name)` per path component; `norm.NFC.IsNormalString`
   in tests.

Normalise **per path component**, then join with `/` — never normalise across the
`/` separators, so the separator itself can't be affected.

## Rationale

- NFC matches the Windows/Linux majority and the way most apps and users type
  names, minimising surprise and re-writes.
- The probe proves the scanner *cannot* trust the OS to deliver a consistent form,
  so normalisation at scan time is mandatory, not optional.
- Keeping the raw on-disk name decouples *identity* (NFC, for hashing/convergence)
  from *access* (raw bytes, for the filesystem), which is the only design that is
  correct on both normalization-insensitive (APFS) and normalization-sensitive
  (NTFS/ext4) volumes.

## Consequences

- **Normalisation collisions** become a real class: on a normalization-sensitive
  peer (Linux), `résumé.txt` may exist as *both* NFC and NFD — two distinct files
  that both map to the *same* NFC canonical key. They cannot both be represented
  under one key, and cannot coexist on APFS at all (probe: coalesces to 1). This is
  handled by the **canonical-key collision policy**
  (`decisions/crossplatform/case-and-normalization-collision-policy.md`): detect,
  refuse the second, flag — never clobber.
- The structural hash hashes the **NFC** canonical component names (already implied
  by SR-13 / leaf-shape "child name (canonical)"); both peers therefore agree
  byte-for-byte.
- Drives `internal/pathnorm/normalize.go` (`norm.NFC`) and the `FileInfo` carrying
  both `path` (canonical NFC) and an on-disk name handle for I/O.
- Adds `golang.org/x/text` to `go.mod`; the CI cross-compile (`GOOS=windows`) and
  matrix must include it.
- Unit-testable on the Mac. The APFS dual-form coexistence edge and Linux-peer
  normalisation collisions are testable locally too (the probe already exercises
  them); the *Windows* side of round-trips is closed by the Phase 6 CI
  `windows-latest` job + `CROSS_PLATFORM_CHECKLIST.md`.
