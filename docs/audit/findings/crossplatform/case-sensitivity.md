# Cross-platform finding — Case sensitivity (File.txt vs file.txt) without clobber

- Slug: `case-sensitivity` · confirms **XP-4**
- Phase: 2 (crossplatform-researcher, elevated track)
- Reads-first: `docs/audit/rules/crossplatform-rules.md` (XP-1..XP-6), rest of
  `docs/audit/rules/`, `findings/literature/syncthing-bep.md`, `findings/synthesis/`.
- Decision logged before this finding:
  `docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`.
- Access date for all URLs: **2026-06-28**.
- Severity: **High** — proven *silent* data loss on a case-insensitive volume if
  unhandled; cross-OS; the NTFS side cannot be tested on the Mac.

## Claim

`File.txt` and `file.txt` are two independent logical files (case-sensitive
canonical keys), but they cannot coexist on a case-insensitive target (Windows
always; macOS default). On such a target, detect the collision via a
case-fold index and **refuse the second write + flag it — never clobber**. A
case-sensitive Linux peer (or case-sensitive macOS volume) keeps both.

## Evidence (runnable reproduction — silent clobber on this Mac)

On this machine's default APFS volume (case-insensitive/case-preserving):

```
$ echo "first"  > File.txt
$ echo "second" > file.txt
$ ls -1 [Ff]ile.txt | wc -l      # -> 1   (only one file exists)
$ cat File.txt                   # -> second   (the FIRST write was overwritten)
```

The OS returned **no error**; the second create silently overwrote the first.
A naive "apply the received file" that does `os.OpenFile(dst, ...TRUNC)` would
therefore destroy data on every case-only collision — exactly the SR-7 no-data-loss
violation. (`filepath.Separator` was also confirmed `/` here; see
`path-separators.md`.)

## Evidence (the platform matrix, cited)

- **Windows / macOS are case-insensitive** by default: "Do not assume case
  sensitivity. For example, consider the names OSCAR, Oscar, and oscar to be the
  same … Note that NTFS supports POSIX semantics for case sensitivity but this is
  not the default behavior"
  ([Microsoft, *Naming Files, Paths, and Namespaces*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file),
  accessed 2026-06-28). macOS APFS/HFS+ default volumes are case-insensitive,
  case-preserving (clobber reproduced above).
- **Linux (and case-sensitive macOS volumes) are case-sensitive** — both names are
  distinct files and a peer there will send both.
- **Syncthing's posture (the reference) = detect + don't clobber.**
  `caseSensitiveFS` is **`false` by default**, meaning "Syncthing's case
  sensitivity safety checks are enabled … [it] will then attempt to detect and
  prevent case-only file name collisions that can occur on case insensitive systems
  such as Windows and macOS"; the option "is **not** meant to change the basic
  principles of how Syncthing handles case-sensitivity"
  ([caseSensitiveFS docs](https://docs.syncthing.net/advanced/folder-caseSensitiveFS.html),
  v2.1.0, accessed 2026-06-28). Internally keys stay case-sensitive
  ("`file.txt` and `FILE.txt` denote two independent things"); on collision it
  raises a sync error or writes a `Foo.case-conflict-<timestamp>-<dev>.txt` copy,
  detecting the real on-disk case "using directory listing methods" (released
  v1.9.0 — [Syncthing wiki: Filesystem Case Sensitivity](https://github.com/syncthing/syncthing/wiki/Filesystem-Case-Sensitivity),
  accessed 2026-06-28).

## Decision applied

`decisions/crossplatform/case-and-normalization-collision-policy.md`:
- **Keys stay case-sensitive NFC** (Option 1B) — folding case into the key (Option
  1A) would merge `File.txt`/`file.txt` and lose one on a case-sensitive peer.
- A **fold-and-normalise collision index** keyed on `fold(NFC(name))` detects both
  case and normalisation collisions with one mechanism; it lives under the single
  tree `RWMutex` (GR-5), owned by the reconcile writer.
- On a detected collision on a case-insensitive target: **refuse + flag** (Option
  2B, default; never clobber), with an optional `.case-conflict` copy (Option 2C,
  Syncthing-style) behind a config flag. Winner is chosen deterministically (SR-7
  tiebreak: newer mtime, else larger DeviceID) so both peers pick the same winner.
- **Target case-sensitivity is probed at startup** (two temp names differing only
  by case; observe survival — Syncthing's technique).

## Test obligations

- Mac/Linux-testable: build the tree with `File.txt` and `file.txt` from a
  case-sensitive source; assert the apply step **refuses** the second on a
  case-insensitive target and **flags** it, with zero bytes lost (both retained in
  the tree). Assert `fold(NFC("File.txt")) == fold(NFC("file.txt"))`.
- Deterministic-winner test: both simulated peers select the same survivor.

## Cannot be verified on the Mac → Phase 6

The real **NTFS** case-collision behaviour is explicitly out of reach on one Mac
(plan/README: "NTFS case-insensitive collisions (File.txt vs file.txt)"). The
macOS-insensitive side is reproduced above; the NTFS side is closed by the CI
`windows-latest` job + `docs/audit/CROSS_PLATFORM_CHECKLIST.md`.
