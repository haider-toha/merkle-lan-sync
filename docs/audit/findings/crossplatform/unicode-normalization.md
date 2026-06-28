# Cross-platform finding — Unicode normalisation (macOS NFD vs Windows/Linux NFC)

- Slug: `unicode-normalization` · confirms **XP-2**
- Phase: 2 (crossplatform-researcher, elevated track)
- Reads-first: `docs/audit/rules/crossplatform-rules.md` (XP-1..XP-6), rest of
  `docs/audit/rules/`, `findings/synthesis/problem-space-map.md` (R-1).
- Decision logged before this finding:
  `docs/audit/decisions/crossplatform/unicode-canonical-form.md`.
- Access date for all URLs: **2026-06-28**.
- Severity: **High** — this is the synthesis's #1 convergence risk (R-1). The
  "same" name in two byte forms = two leaves = roots never converge (SR-5).

## Claim

Canonical Unicode form is **NFC**, applied **at scan time and on receive from a
peer**, normalising **per path component**. The raw `os.ReadDir` name is retained
alongside the canonical key and used for all filesystem I/O. Two distinct on-disk
files that map to the same NFC key are a **normalisation collision** handled like a
case collision (refuse + flag, never clobber).

## Evidence (runnable reproduction — the decisive part)

`scratchpad/normprobe/main.go` (Go 1.26.4 on this machine's APFS volume) proves
macOS is **normalization-preserving, not normalizing** — the scanner cannot assume
a consistent form:

```
--- ReadDir after creating NFC name (U+00E9) ---
  name="résumé.txt" bytes=72 c3 a9 73 75 6d c3 a9 2e 74 78 74  ==NFC?true  ==NFD?false
  os.Open via NFC form: OK
  os.Open via NFD form: OK
--- ReadDir after creating NFD name (U+0301) ---
  name="résumé.txt" bytes=72 65 cc 81 73 75 6d 65 cc 81 2e 74 78 74  ==NFC?false ==NFD?true
  os.Open via NFC form: OK
  os.Open via NFD form: OK
--- created BOTH nfc+nfd in same dir: 1 entries (normalization-insensitive coalesces) ---
```

Three facts fall out, each load-bearing:
1. **`os.ReadDir` returns whatever form the file was written in** (NFC stays NFC,
   NFD stays NFD). HFS+ used to force NFD on disk; APFS does **not**. So at scan
   time we may receive *either* form for the same logical name → we **must**
   normalise to a canonical form ourselves.
2. **macOS lookup is normalization-insensitive** (`os.Open` worked via both forms),
   so an NFC canonical key can open an NFD-on-disk file on APFS.
3. **On APFS the two forms cannot coexist** (creating both yielded 1 entry) — but
   on normalization-**sensitive** filesystems (NTFS, Linux ext4) they *can*, so a
   Linux peer can legitimately send two files that collapse to one NFC key.

## Evidence (corroborating sources)

- "macOS uses NFD (Normalization Form Decomposed) by default when storing
  filenames, while Windows and Linux primarily expect NFC"; the visible symptom of
  getting it wrong: a Mac `résumé.pdf` shows up on Windows as `re´sume´.pdf`
  ([The Eclectic Light Co., *Unicode, normalization and APFS*](https://eclecticlight.co/2021/05/08/explainer-unicode-normalization-and-apfs/);
  [Michael Tsai, *APFS's "Bag of Bytes" Filenames*](https://mjtsai.com/blog/2017/03/24/apfss-bag-of-bytes-filenames/),
  accessed 2026-06-28).
- APFS is "normalization-preserving and normalization-insensitive by default …
  you can save and access files using either NFC or NFD"; it stores names un-
  normalised and uses "normalization-insensitive hashes", unlike HFS+ which stores
  the NFD form ([Michael Tsai, *APFS Native Normalization*](https://mjtsai.com/blog/2017/06/27/apfs-native-normalization/);
  [Apple APFS FAQ](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/FAQ/FAQ.html),
  accessed 2026-06-28).
- The protocol layer will not save us: Syncthing's BEP sends `FileInfo.name` as a
  raw byte string and does **no** normalisation — normalisation is the
  application's job (`findings/literature/syncthing-bep.md` §3, §10.6, citing
  `bep.proto @2775f424f228`, accessed 2026-06-28).
- Tooling: `golang.org/x/text/unicode/norm` — "Package norm contains types and
  functions for normalizing Unicode strings"; use `norm.NFC.String(name)` and
  `norm.NFC.IsNormalString` (v0.38.0, Unicode 15.0.0,
  [pkg.go.dev/golang.org/x/text/unicode/norm](https://pkg.go.dev/golang.org/x/text/unicode/norm),
  accessed 2026-06-28). Logged as an approved dependency (GR-11 exception) in the
  decision.

## Decision applied

- **NFC, at scan time + on receive**, per component;
  `decisions/crossplatform/unicode-canonical-form.md` (Option C). NFC chosen over
  NFD (Windows/Linux majority), over raw "bag of bytes" (non-convergent), and over
  NFKC (lossy compatibility folding merges distinct names).
- **Keep the raw readdir name for I/O.** This sharpens XP-2's "remember the on-disk
  byte form … if needed to re-open the file on macOS": the probe shows it is needed
  *generally* (NTFS/ext4 are normalization-sensitive, so the NFC key may not open
  an NFD-on-disk file), not only on macOS.
- **Normalisation collisions** are routed to
  `decisions/crossplatform/case-and-normalization-collision-policy.md` (the fold
  index keys on `fold(NFC(name))`, catching both classes).

## Test obligations

- NFC/NFD round-trip unit tests (plan/README "Unicode NFC/NFD normalisation unit
  tests"): `norm.NFC.String(nfd) == nfc`; canonicalising either form yields the
  same key and the same subtree hash (SR-5/SR-13).
- APFS dual-form coexistence and Linux-peer normalisation collision are Mac/Linux-
  reproducible (the probe already exercises the macOS side).

## Cannot be verified on the Mac → Phase 6

The *Windows* end of a Mac→Windows→Mac round-trip (does an NFC name written on
Windows scan back identically?) is closed by the CI `windows-latest` job +
`CROSS_PLATFORM_CHECKLIST.md`. The normalisation logic itself is fully Mac-unit-
testable.
