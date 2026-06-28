# Skeptic #2 vote — crossplatform-critic-4 (REFUTE)

- Finding: "Case-sensitivity probed once at root, applied tree-wide; NTFS is
  per-directory and macOS per-volume, so a single root verdict mis-classifies
  subtrees → false refusals or clobber."
- Role: skeptic #2 of 3, tasked to refute.
- Vote: **REFUTE** (severity overstated; dangerous direction exotic; unique fix
  is redundant with crossplatform-critic-2).
- Confidence: medium.

## What is true (conceded)

The underlying OS facts are correctly cited and real:
- NTFS per-directory case sensitivity since Win10 1803 (the Microsoft/DevBlogs
  links check out).
- macOS case sensitivity is per-volume (a case-sensitive APFS volume can be
  mounted inside a case-insensitive namespace).

So the *premise* ("sensitivity can vary within one sync tree") is sound. The
refutation is not that the premise is false — it is that the **impact and
severity are overstated**, the **dangerous direction is non-realistic**, and the
finding's **only non-redundant recommendation addresses only the benign
direction**.

## Refutation

### 1. The dangerous (data-loss) direction requires an adversarial, stacked config that essentially no user creates

The "clobber" branch needs: **root probes case-sensitive** AND a
**case-insensitive subtree nested under it**.

- On **Windows**, NTFS is case-insensitive by default. For the *root* probe to
  return case-sensitive, the operator must have deliberately
  `fsutil ... enable` the sync root itself — which is not what WSL does (WSL flags
  specific project directories, not a user's whole sync folder). Then, the
  finding's own cited rule is that **children inherit case sensitivity from the
  parent**, so a child of a case-sensitive dir is *born* case-sensitive. To
  produce the insensitive subtree you must *additionally* and explicitly
  `fsutil ... disable` that child. Two deliberate, contrary toggles stacked on a
  non-default root. This is not a configuration real users stumble into.
- On **macOS**, it needs the sync root volume formatted case-sensitive (Apple's
  non-default mode that breaks many apps and is explicitly discouraged) **plus** a
  case-insensitive volume mounted inside it. Also exotic.

A "medium" severity claiming **silent data loss** rests on a configuration that
is effectively never seen in the wild.

### 2. The *realistic* mis-probe direction is the SAFE one, by construction

The common real-world case is the inverse: root on a **default insensitive**
NTFS/APFS (probe = insensitive) containing a **case-sensitive** subtree (a WSL
`fsutil enable` dir, or a mounted case-sensitive APFS volume). The finding itself
classifies this as a **false refusal**: "that path never converges and is
permanently flagged 'needs attention' though nothing is wrong." That is
**recoverable, visible, non-destructive** — and it is precisely the design's
deliberate refuse+flag posture
(`case-and-normalization-collision-policy.md`, Sub-decision 2, Option 2B). So the
*likely* mis-probe degrades to the design's intended fail-safe behaviour. The
dangerous direction is the rare one; the rare one is the only one that loses data.

### 3. The finding's own safety fix is redundant with crossplatform-critic-2

Recommended-change #2 is verbatim: "keep the defensive pre-write existence check
recommended in `crossplatform-critic-2` (stat the target via directory listing;
... refuse+flag)." That defensive directory-listing check makes no-clobber depend
on the **filesystem's own verdict**, not on any probe — root-level or
per-directory. If critic-2 (a separate, higher-severity finding) is adopted, the
clobber direction of critic-4 is **fully neutralised regardless of probe
granularity**. The decision already gestures at this mechanism, quoting
Syncthing detecting on-disk case "using directory listing methods"
(`case-and-normalization-collision-policy.md`, Context). So critic-4 contributes
**no independent safety value** beyond critic-2; its unique addition
(per-directory re-probing) only changes the **false-refusal/availability**
direction, which is already fail-safe.

### 4. The finding attacks an implementation detail as if it were a frozen design commitment

The decision under review is about **key representation (case-sensitive NFC keys
+ fold index)** and **collision action (refuse+flag, never clobber)**. Probe
*granularity* ("at startup ... in the sync root") is one line describing how the
enforce/allow gate is selected — not a load-bearing architectural invariant. The
fold-index-and-refuse mechanism is **granularity-agnostic**: re-running the same
probe per-directory (or per-volume) at implementation time is a localized
refinement that requires no design change. The decision's own Consequences
already concede the NTFS side "cannot be fully verified on the Mac" and defer it
to Phase 6 CI (`windows-latest`) + `CROSS_PLATFORM_CHECKLIST.md` — exactly where
a per-directory probe belongs. Promoting an implementation TODO to a Phase-3
design finding inflates its standing.

### 5. Net

- Evidence supports the *facts* but not the *severity*: the data-loss outcome
  needs an adversarial config; the realistic outcome is fail-safe.
- The unique recommendation (per-directory probe) buys only availability in the
  benign direction.
- The safety recommendation duplicates critic-2 and is moot once critic-2 lands.

A finding whose dangerous case is exotic, whose common case is already safe, and
whose only safety fix is owned by another finding does not meet the bar for an
independent open design defect.

## Recommendation to consolidator

REFUTE as a standalone finding. Fold its one actionable nugget — "re-probe
case-sensitivity per-directory (NTFS) / per-volume (macOS) at apply time, and
cache it" — into the implementation notes of **crossplatform-critic-2**, whose
filesystem-verdict defensive check already makes no-clobber granularity-proof. No
separate decision is warranted.
