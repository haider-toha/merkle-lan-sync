# Skeptic #3 vote — crossplatform-critic-4 (REFUTE assignment)

- Finding: "Case-sensitivity is probed once at the sync root and applied
  tree-wide; NTFS is per-directory and macOS is per-volume, so a single root
  verdict mis-classifies subtrees -> false refusals or a clobber."
- Vote: **REFUTED** (severity overstated; unique recommendation redundant/over-engineered)
- Confidence: medium

## What I verified

- The design probe granularity is exactly as quoted
  (`docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`,
  Decision, lines 100-104): one startup probe at the sync root, verdict selects
  whether the fold index is enforced.
- The cited OS facts are TRUE: NTFS per-directory case sensitivity (Win 10 1803+)
  and macOS per-volume case sensitivity are correctly described. I do not dispute
  the platform mechanics.

## Why the finding does not hold as a medium-severity design defect

1. **The design matches the validated reference implementation's granularity.**
   The finding's own reference posture (Syncthing) keys case-sensitivity off a
   **per-folder** setting (`caseSensitiveFS`/`caseSensitiveFilesystem`), NOT
   per-directory. Confirmed 2026-06-28:
   https://docs.syncthing.net/advanced/folder-caseSensitiveFS.html and
   https://github.com/syncthing/syncthing/wiki/Filesystem-Case-Sensitivity .
   Syncthing has shipped per-folder (i.e. per-sync-root) granularity since v1.9.0
   across millions of mixed Win/macOS/Linux deployments without per-directory
   re-probing. Holding this design to a per-directory standard the industry
   reference does not meet is gold-plating, not a defect.

2. **The data-loss (clobber) direction requires a doubly-contrived,
   non-default configuration on BOTH OSes.** For the dangerous branch the root
   must probe **case-sensitive** AND a nested subtree must be **case-insensitive**:
   - Windows default is case-insensitive, so a case-sensitive root is already a
     deliberate `fsutil setCaseSensitiveInfo enable` / WSL act. New child dirs
     **inherit** the parent's case sensitivity (per the finding's own cited
     Microsoft source), so a case-insensitive subtree under a case-sensitive root
     requires a *second* deliberate toggle-back, or a separately-mounted volume.
     Doubly deliberate.
   - macOS default is case-insensitive; a case-sensitive APFS root with a
     case-insensitive volume mounted inside it is exotic and intentional.
   The common, realistic manifestation (WSL `enable` dir inside a normal NTFS
   root) is the **benign** direction: root insensitive, subtree sensitive ->
   the engine over-enforces the fold index -> a **false refusal that is flagged,
   recoverable, and loses no data**. The finding even concedes this is "recoverable."
   A defect whose realistic failure mode is a recoverable flag, and whose
   data-loss mode needs a doubly-deliberate exotic layout, is low severity at most.

3. **The only safety-relevant recommendation (#2) is redundant with
   crossplatform-critic-2.** Recommendation #2 — keep a defensive pre-write
   directory-listing existence check regardless of the probe verdict, so a
   mis-probe degrades to a refusal and never a clobber — is explicitly lifted from
   crossplatform-critic-2 ("the defensive pre-write existence check recommended in
   crossplatform-critic-2"). This is precisely Syncthing's default-on behaviour
   (safety checks enabled, real case detected "using directory listing methods").
   If that defensive check is adopted (it is the substantive fix and belongs to a
   different finding), the probe verdict can never cause a clobber — which makes
   the **unique** contribution of THIS finding moot.

4. **The unique recommendation (#1, per-directory/per-volume probing) buys no
   marginal safety over #2 while adding real cost.** Once #2 makes the no-clobber
   check verdict-independent, per-directory `fsutil queryCaseSensitiveInfo` plumbing,
   macOS per-volume backing detection, and a per-directory cache add OS-specific,
   fragile, hard-to-test code for zero additional data-loss protection — it would
   only convert some recoverable false-refusals into successful writes, a UX nicety,
   not a correctness fix. The decision file already routes the safety guarantee
   through the fold index + refuse-never-clobber default (Option 1B + 2B), with the
   probe acting only as an *enforcement optimisation*. The correct minimal change is
   "never fully disable detection," not "re-probe every directory."

## Net

The platform facts are accurate, so this is not baseless — but the finding (a)
matches the reference implementation's accepted granularity, (b) overstates
severity by foregrounding a doubly-deliberate exotic clobber configuration while
the realistic case is a recoverable flag, (c) folds the only load-bearing fix into
a duplicate of crossplatform-critic-2, and (d) proposes per-directory/per-volume
probing that adds OS-specific complexity for no safety gain once #2 exists.
Refuted as a standalone medium-severity design defect; its actionable safety core
is already owned by crossplatform-critic-2.

Sources:
- https://docs.syncthing.net/advanced/folder-caseSensitiveFS.html (accessed 2026-06-28)
- https://github.com/syncthing/syncthing/wiki/Filesystem-Case-Sensitivity (accessed 2026-06-28)
