# Skeptic-1 vote — crossplatform-critic-4

VOTE: REFUTED
Confidence: medium

## Finding under review

"Case-sensitivity is probed once at the sync root and applied tree-wide, but NTFS
case sensitivity is per-directory (Win10 1803+) and macOS is per-volume — a single
root verdict mis-classifies subtrees, causing false refusals or a clobber in a
subtree wrongly believed case-sensitive." Severity: medium.

## What is true

The two technical facts are accurate and well-cited: NTFS per-directory case
sensitivity exists since Windows 10 1803 (build 17107), toggled by
`fsutil file setCaseSensitiveInfo`, inherited at creation
(https://learn.microsoft.com/en-us/windows/wsl/case-sensitivity, accessed
2026-06-28), and macOS case sensitivity is a per-volume format property. It is also
true that the design's probe is a single startup probe at the sync root applied as
one global verdict
(`docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`,
Decision bullet 3). So the finding is not vague or fabricated; it points at a real
granularity gap in the probe. I refute on impact, novelty, and the recommended-change
delta, not on factual error.

## Why it is refuted

1. **The data-loss direction is fully subsumed by crossplatform-critic-2 and by a
   mechanism already on record in the design.** The dangerous outcome (root probes
   case-sensitive -> no-clobber check skipped -> insensitive subtree clobbers) is
   structurally identical to critic-2's "engine writes K2 because it believes the
   target is sensitive, the filesystem had one slot, K1 is overwritten." The accepted
   remedy for that is a **defensive pre-write existence check via directory listing**
   on the actual target — which is exactly the Syncthing posture the design already
   cites ("detecting the real on-disk case using directory listing methods",
   `case-and-normalization-collision-policy.md` Context). That check stats the real
   destination directory at apply time, so it is **inherently per-directory and
   per-volume** — it cannot be fooled by a wrong global probe verdict, because it
   never consults the probe verdict; it asks the actual target directory. Once that
   apply-path check is in (and critic-4's own recommendation #2 *requires* it), the
   clobber direction of critic-4 simply cannot occur, regardless of probe
   granularity. The finding's headline harm therefore adds nothing over critic-2,
   which all three skeptics already refuted as a design-blocking item and downgraded
   to a Phase-6/WS-4 implementation note.

2. **The unique residual is benign and recoverable.** Strip out the
   already-covered clobber direction and what is left unique to critic-4 is the
   *false-refusal* direction (root insensitive, a WSL-created case-sensitive subtree
   exists, the fold index over-refuses a `File.txt`/`file.txt` pair the subtree could
   hold). That is availability/convergence friction on a flagged path, not data loss —
   the finding itself labels it "recoverable." A recoverable, narrow over-refusal does
   not support a medium severity; it is a low implementation note.

3. **The trigger configuration is a corner of a corner, and the common case is
   already safe.** The dangerous direction needs an *unusual* root verdict
   (case-sensitive: a case-sensitive APFS volume, or an fsutil-marked NTFS directory
   as the daemon root) AND a *more unusual* nested insensitive subtree under it.
   macOS mounts volumes at `/Volumes`, not nested inside a user's sync folder; NTFS
   per-directory sensitivity is an opt-in WSL feature requiring explicit fsutil
   toggling. The overwhelmingly common roots — Windows default and macOS default —
   both probe **insensitive**, which enforces the fold index tree-wide and fails to
   the safe (over-refuse) side everywhere. So in the default world the global verdict
   is conservatively correct; the misclassification only bites configurations a user
   has deliberately and atypically constructed.

4. **The recommended per-directory probe (rec #1) is dominated, not additive.**
   Building a per-directory probe-and-cache on Windows plus per-volume backing
   detection on macOS is real complexity, and it is made redundant by the very
   defensive existence check the finding endorses in rec #2: if the apply path always
   stats the real target directory, you do not need to know each directory's
   sensitivity in advance to avoid a clobber. The simpler, already-cited mechanism
   beats the proposed new subsystem on testability and cross-platform robustness, so
   the recommended change does not beat status-quo-plus-critic-2.

## Conclusion

Factually sound but low-incremental: its severe (data-loss) outcome is duplicative of
crossplatform-critic-2 and is neutralised by the directory-listing apply-path check
already on record (and which this finding itself depends on); its unique residual is a
recoverable over-refusal in atypical, deliberately-constructed mixed-sensitivity
trees; and its headline recommendation (per-directory/per-volume probing) is dominated
by that simpler check. Medium severity is overstated. Best disposition: fold into the
critic-2 Phase-6/WS-4 hardening note ("apply path must stat the real target directory;
no-clobber must not rely on a global probe verdict"), not a standalone design finding.

REFUTED (medium confidence).
