---
id: crossplatform-critic-4
title: "Case-sensitivity is probed once at the sync root and applied tree-wide, but NTFS case sensitivity is per-directory (Windows 10 1803+) and macOS case sensitivity is per-volume — a single root verdict is wrong for mixed-sensitivity subtrees, causing either false refusals or a clobber in a subtree the engine wrongly believes is case-sensitive"
severity: medium
status: rejected
phase: 3
critic: crossplatform-critic
focus: case-insensitive collision handling without clobber
created: 2026-06-28
---

# A single root-level case-sensitivity probe mis-classifies subtrees

- Reads-first honoured:
  `docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`,
  `docs/audit/findings/crossplatform/case-sensitivity.md`, XP-4, SR-7.

## Claim

The design decides whether to enforce the case/normalisation collision policy from a
**single startup probe at the sync root** ("create two temp names differing only by
case in the sync root, observe survival"). It then applies that one boolean verdict to
the **entire tree**. But case sensitivity is **not a per-root property**: on Windows
10 1803+ it is a **per-directory** NTFS attribute (a subdirectory can be case-
sensitive while its parent is not, and vice versa), and on macOS it is a **per-
volume** property (a case-sensitive APFS volume can be mounted inside an otherwise
case-insensitive tree). A root probe therefore returns the wrong answer for any
subtree whose sensitivity differs, defeating the enforce/allow decision exactly where
it matters.

## Evidence

- The design's probe granularity, verbatim
  (`docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`,
  Decision):
  > "**Target case-sensitivity is probed at startup** (create two temp names
  > differing only by case **in the sync root**, observe whether both survive ...).
  > The result selects whether the fold index is enforced (insensitive target) or
  > collisions are allowed (case-sensitive target)."
  One probe, one global verdict — there is no per-directory re-probe.

- **NTFS case sensitivity is per-directory since Windows 10 1803 (build 17107).**
  "Starting with Windows 10 version 1803, you can enable case sensitivity at the
  folder level"; the attribute "can only be set on directories", new directories
  "inherit the case sensitivity from [their] parent directory", and it is toggled per
  directory with `fsutil.exe file setCaseSensitiveInfo <path> enable|disable`
  (Microsoft, *Adjust case sensitivity*, accessed 2026-06-28,
  https://learn.microsoft.com/en-us/windows/wsl/case-sensitivity; Microsoft DevBlogs,
  *Per-directory case sensitivity and WSL*, accessed 2026-06-28,
  https://devblogs.microsoft.com/commandline/per-directory-case-sensitivity-and-wsl/).
  WSL routinely creates case-sensitive directories inside an ordinary NTFS volume, so
  a sync root can legitimately contain a case-sensitive subtree.

- **macOS case sensitivity is per-volume**, not per-folder: APFS/HFS+ are formatted
  either "Case-sensitive" or the default case-insensitive, and a separate case-
  sensitive volume can be mounted anywhere in the namespace (Apple APFS guide,
  accessed 2026-06-28). The root probe sees the root volume's setting only.

- **Both error directions are real:**
  - Root probes **insensitive**, a subtree is actually **case-sensitive** (a WSL
    `fsutil ... enable` directory). The engine enforces the fold index and
    **refuses/flags** `File.txt` + `file.txt` in that subtree even though the
    filesystem could hold both -> a **false refusal**: that path never converges and
    is permanently flagged "needs attention" though nothing is wrong.
  - Root probes **case-sensitive** (e.g. the daemon's root is on a case-sensitive
    APFS volume, or an NTFS dir marked case-sensitive), but files are applied into a
    **case-insensitive** subtree/volume nested under it. The engine believes
    collisions are "allowed", so it **skips the no-clobber check** and writes both
    `File.txt` and `file.txt` -> the insensitive subtree **clobbers** one ->
    **silent data loss** (SR-7), the exact outcome XP-4 forbids.

## Impact

- The enforce/allow gate for the entire no-clobber policy can be set to the wrong
  value for part of the tree. In the dangerous direction (root case-sensitive, subtree
  insensitive) it disables clobber protection precisely where clobbering happens.
- This is structurally invisible to the Mac unit tests (which probe one temp dir) and
  to a naive single-directory Windows CI run; it only appears with a mixed-sensitivity
  tree, which WSL users and multi-volume Mac users create routinely.

## Recommended-change

1. **Do not treat case sensitivity as a single global flag.** Determine it at the
   granularity at which it actually varies: per-directory on Windows (query via the
   directory case-sensitivity attribute / `fsutil queryCaseSensitiveInfo` semantics)
   and per-volume on macOS (detect the volume backing each path). Cache per-directory
   results.
2. **Make the no-clobber check independent of the probe verdict for safety.** Even
   when a subtree is believed case-sensitive, keep the defensive pre-write existence
   check recommended in `crossplatform-critic-2` (stat the target via directory
   listing; if an OS-equal entry with a different canonical key exists, refuse+flag).
   That way a mis-probe degrades to a false refusal (recoverable) and **never** to a
   clobber (data loss) — i.e. fail safe.
3. **Test obligation:** a Phase-6 scenario with a case-sensitive subdirectory nested in
   a case-insensitive root (and vice versa), asserting the policy verdict is taken
   per-directory and that no clobber occurs in either configuration.
