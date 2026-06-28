# Skeptic #3 vote — crossplatform-critic-2

- Finding: "'Never clobber' is unsound: collision detection folds in canonical-key
  space with Unicode simple case-fold, but the filesystem clobbers in OS-name space
  with NTFS's frozen per-volume $UpCase table."
- Role: skeptic #3 of 3, tasked to REFUTE.
- Vote: **REFUTED** (the finding as stated overreaches; severity is inflated and the
  recommendation largely restates the existing plan).
- Confidence: medium.

## What I checked

- The finding file `crossplatform-critic-2.md`.
- The mechanism it attacks:
  `docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`
  (Sub-decision 1 Option 1B, Sub-decision 2 Option 2B/2C, the startup probe,
  Consequences).
- `docs/audit/findings/crossplatform/case-sensitivity.md`.
- `plan/README.md` (Phase-6 NTFS deferral).

The finding's abstract point is technically true: an in-engine fold (`unicode.SimpleFold`
over NFC) is not *provably* identical to NTFS name-equality (`$UpCase`). I do not
dispute that narrow statement. I dispute that it rises to a HIGH-severity, silent
data-loss defect, and that the recommendation meaningfully beats the status quo.

## Grounds for refutation

1. **The high-severity claim (silent clobber) is asserted, not demonstrated, and the
   cited evidence points the *other* way.** The data-loss direction requires the engine
   fold to be *finer* than the volume's `$UpCase` (engine sees two keys, NTFS sees one,
   second write overwrites). The finding never exhibits a single concrete character pair
   where `SimpleFold` distinguishes but `$UpCase` conflates. Worse, its own cited fact —
   "Unicode 13 (2020) introduced 169 new case-folding entries vs version 8 (2015)" —
   means newer Unicode folds *more* together, i.e. `SimpleFold` trends **coarser** over
   time relative to an older frozen `$UpCase`. Coarser-engine is the finding's own
   *non*-data-loss direction (false refusal / over-flag). The evidence supplied
   substantiates the benign direction, not the catastrophic one.

2. **The "a-z passes through the table" quirk does not produce a finer pair.**
   `SimpleFold` also relates `a-z`<->`A-Z`, so any ASCII pair NTFS conflates the engine
   also conflates. That quirk yields no `SimpleFold`-distinct / `$UpCase`-same example.

3. **The DFIR.ru "system update -> identical forms" quote is misapplied.** That passage
   describes NTFS's *own internal* inconsistency (the driver's in-memory mapping drifting
   from the volume's frozen `$UpCase`, so NTFS itself trips over two on-disk files). It
   is not evidence that our engine's `SimpleFold` disagrees with `$UpCase` in the
   clobber direction. It is borrowed to dramatize a different failure than the one being
   alleged.

4. **The design never claimed a *proven* NTFS guarantee — it explicitly deferred it.**
   The decision's Consequences say verbatim it "Cannot be fully verified on the Mac for
   the Windows/NTFS side ... Closed by Phase 6 CI `windows-latest` +
   `CROSS_PLATFORM_CHECKLIST.md`." `plan/README.md` lists "NTFS case-insensitive
   collisions" as the canonical Mac-unverifiable item. So the finding attacks a
   finalized guarantee that the design did not actually assert; the soundness gap is
   already an open, scheduled verification item, not a silently-closed one.

5. **The recommendation mostly restates the existing plan, so it does not clearly beat
   the status quo.** Rec (a) "probe equivalence" and rec (c) "Phase-6 NTFS matrix" are
   already present: the decision *already* probes the target via Syncthing's
   directory-listing technique, and Phase 6 already owns the NTFS matrix. Rec (b)
   "directory-listing pre-write existence check" is the one genuinely additive item, and
   it is already cited in `case-sensitivity.md` as Syncthing's "detect the real on-disk
   case using directory listing methods" — i.e. a known refinement, not a discovery.
   That is a worthwhile implementation note, but it is an incremental hardening, not a
   HIGH design defect.

## Concession

Rec (b) (let the filesystem's own verdict, via a pre-rename directory listing, gate the
write rather than trusting the in-engine fold) is sound and should be folded into the
WS-4 apply path as an implementation note. But that is a small, low-risk addition to an
already-deferred verification item — it does not validate the "unsound / silent data
loss / HIGH" framing.

## Verdict

REFUTED. The core technical observation is real but minor; the HIGH severity rests on a
data-loss direction that is asserted without a concrete example and is contradicted by
the finding's own Unicode-versioning evidence, the marquee citation is misapplied, and
the design already defers NTFS equivalence to Phase 6 with the directory-listing
technique on record. Downgrade to a low/medium implementation note on WS-4, not a
design-blocking finding.

VOTE: REFUTED
