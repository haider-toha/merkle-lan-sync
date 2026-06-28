---
id: crossplatform-critic-2
title: "\"Never clobber\" is unsound: collision detection folds in canonical-key space with Unicode simple case-fold, but the filesystem clobbers in OS-name space with a different, frozen per-volume fold (NTFS $UpCase) — distinct keys can collide on disk and silently overwrite"
severity: high
status: rejected
phase: 3
critic: crossplatform-critic
focus: case-insensitive collision handling without clobber
created: 2026-06-28
---

# The collision index uses the wrong fold function for the target filesystem

- Reads-first honoured: `docs/audit/rules/crossplatform-rules.md` (XP-4, XP-2),
  `docs/audit/findings/crossplatform/case-sensitivity.md`,
  `docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`,
  SR-7 (no data loss on conflict).
- This attacks the design's central cross-platform safety promise — XP-4 "refuse +
  flag, **never clobber**" — at its detection mechanism.

## Claim

The no-clobber guarantee is enforced by checking a side index keyed on
`fold(NFC(name))`, where `fold` is **Unicode simple case-fold**, computed inside the
engine on the canonical key. But the actual overwrite happens **on the target
filesystem**, which decides name-equality with its **own** case-mapping table —
NTFS uses a *per-volume uppercase table* (`$UpCase`), frozen at format time, that is
**not** Unicode simple case-fold; macOS HFS+/APFS uses yet another table. For the
"never clobber" promise to hold, the engine's fold must be **at least as coarse as
the target's** (it must conflate everything the target conflates). Unicode simple
fold is not guaranteed to be — it is a different, independently-versioned function —
so two keys the engine treats as **distinct** can map to the **same** name on the
target, and the second write **silently clobbers** the first. The design picks one
fold and assumes it predicts collisions on *both* OSes; that assumption is unsound.

## Evidence

- The design's mechanism, verbatim
  (`docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`,
  Sub-decision 1 / Decision):
  > "A side **collision index** maps `fold(key) -> key` where `fold` = **Unicode
  > simple case-fold** of the NFC form. The index is consulted only to *detect* a
  > clash on an insensitive target."
  and the only target-awareness is a boolean probe:
  > "**Target case-sensitivity is probed at startup**" (sensitive vs insensitive).
  There is no step that aligns the engine's fold with the target's actual fold.

- **NTFS does not use Unicode case-folding; it uses a frozen per-volume uppercase
  table.** From a detailed write-up of case-insensitive lookups
  (DFIR.ru, *Playing with case-insensitive file names*, 2021-07-15, accessed
  2026-06-28, https://dfir.ru/2021/07/15/playing-with-case-insensitive-file-names/):
  > "The NTFS driver mainly uses **per-volume uppercase tables** (and even characters
  > from the *a-z* range are passed through this table)."
  and the precise failure mode this finding is about:
  > "Imagine two file names mapped into **non-identical** uppercased/lowercased/
  > case-folded forms and a system update leading to these file names being mapped
  > into **identical** ... forms! A file system driver will unexpectedly find two
  > existing files sharing the same name."
  The same source notes the tables genuinely diverge over time: "the Unicode standard
  version 13 (2020) introduced **169 new case-folding entries** compared to ...
  version 8 (2015)." NTFS freezes `$UpCase` per volume precisely because the mapping
  must stay stable (NTFS.com, *Up-Case Table*, accessed 2026-06-28,
  http://ntfs.com/exfat-upcase-table.htm).

- **The two directions of mismatch, and why one is silent data loss:**
  - *Engine fold coarser than NTFS upcase* (engine conflates a pair NTFS keeps
    separate): the engine refuses/flags a pair that NTFS would happily store as two
    files -> a **false refusal** (a path perpetually "needs attention", asymmetric
    non-convergence). Bad, but not data loss.
  - *Engine fold finer than NTFS upcase* (engine treats a pair as distinct that NTFS
    conflates): the engine sees `fold(K1) != fold(K2)`, so it **writes K2**; NTFS
    maps `upcase(K1) == upcase(K2)` and the write **overwrites K1 on disk**. The
    engine believed they were two files; the filesystem had one. **Silent clobber ->
    data loss**, exactly the SR-7 violation XP-4 exists to prevent. Because Unicode
    simple fold and the frozen NTFS upcase table are independently specified, finer
    pairs exist (e.g. case pairs added to Unicode after the volume's `$UpCase` was
    frozen, and the documented a-z-passes-through-the-table quirk).

- Microsoft confirms Windows case-insensitivity is table-mediated, not a Unicode-fold
  contract: "Do not assume case sensitivity ... consider the names OSCAR, Oscar, and
  oscar to be the same" (Microsoft naming page, accessed 2026-06-28) — but the *rule*
  for which names are "the same" is the upcase table, not `unicode.SimpleFold`.

## Impact

- The single most important cross-platform safety property in the design — "never
  clobber on a case-insensitive target" — is **not actually guaranteed** by the
  stated mechanism. It holds only for the subset of names where Unicode simple fold
  happens to agree with the target's table; for the rest it can either over-refuse
  (availability/convergence loss) or, worse, **under-detect and silently overwrite**
  one of two files the engine thought were independent.
- This is invisible on the Mac (the probe + index logic "pass" with macOS's *own*
  fold) and only manifests on a real NTFS volume in Phase 6 — and only for specific
  characters — so it is exactly the class of bug the elevated cross-platform track
  was created to catch before implementation.

## Recommended-change

1. **State the soundness requirement explicitly**: the collision-detection fold MUST
   be at least as coarse as the target filesystem's name-equality. Do not assume
   Unicode simple fold satisfies this for NTFS or APFS.
2. **Make the fold target-derived, not target-agnostic.** Two safe options to
   enumerate/score in the (re)decision:
   (a) **Probe equivalence, not just sensitivity** — at startup, in addition to the
   sensitive/insensitive boolean, detect the target's actual fold by creating probe
   pairs and observing coalescence for the character classes in use; treat the
   target as the authority. (b) **Defensive pre-write existence check on the OS name**
   — before `os.Rename(tmp, dst)`, stat the *target* via directory listing for an
   existing entry that the OS considers equal (Syncthing's "detect the real on-disk
   case using directory listing methods", cited in `case-sensitivity.md`); if one
   exists and it is a *different* canonical key, refuse+flag instead of renaming over
   it. This makes no-clobber depend on the filesystem's own verdict, not on the
   engine's fold guess.
3. **Test obligation:** add a Phase-6 NTFS case-collision matrix over characters where
   `unicode.SimpleFold` and the NTFS upcase table are known to differ (not only the
   ASCII `File.txt`/`file.txt` case the current plan tests), asserting zero bytes lost
   in both mismatch directions.
