# Skeptic-2 vote — crossplatform-critic-2

- Finding: "'Never clobber' is unsound: collision detection folds in canonical-key
  space with Unicode simple case-fold, but the filesystem clobbers in OS-name
  space with NTFS's frozen per-volume `$UpCase` — distinct keys can collide on
  disk and silently overwrite."
- Role: skeptic #2 of 3, tasked to REFUTE.
- Vote: **REFUTED** (confidence: medium)

## Summary

The finding contains a real theoretical kernel — the engine's fold and the
target FS's name-equality are different functions — but it inflates that kernel
into a "high, silent data loss" verdict that its own cited evidence does not
support. The one concrete mechanism it offers for the *dangerous* direction
actually points the *safe* way, no failing character is ever exhibited, the
reference implementation it leans on (Syncthing) uses the very same
Unicode-fold posture without being bitten, and the NTFS side is already
explicitly deferred to Phase-6 CI + checklist. Net: weak/over-stated, refute.

## Reasons to refute

### 1. The headline time-divergence example points to the SAFE direction, not the dangerous one
The finding's only concrete mechanism for "engine fold *finer* than NTFS"
(the data-loss direction) is, verbatim: "case pairs added to Unicode after the
volume's `$UpCase` was frozen." Walk it through:
- `$UpCase` is frozen at format time → it reflects an **older** Unicode.
- The engine's `unicode.SimpleFold` reflects a **newer** Unicode (Go's tables).
- Newer Unicode added *more* case-fold entries (the finding itself quotes "169
  new case-folding entries" v8→v13).
- Therefore for any pair added after the freeze: the **engine conflates** it,
  the **frozen NTFS table does not**. That is engine **coarser** than NTFS →
  by the finding's own taxonomy this is the *false-refusal* branch ("Bad, but
  not data loss"), NOT silent clobber.

The finding files this example under "finer pairs exist," but the mechanism it
describes produces *coarser* engine behaviour. The single concrete driver for
its high-severity claim is self-contradicting.

### 2. No counter-example for the dangerous direction is ever produced
To prove silent clobber the finding must exhibit at least one NFC pair where
`unicode.SimpleFold` keeps the pair distinct but a real NTFS `$UpCase`
conflates them (NTFS *coarser* than current Unicode fold). It exhibits none.
The autonomy contract forbids memory-only claims and demands a runnable
reproduction / file:line / URL for current facts; the dangerous-direction
existence assertion ("finer pairs exist") has neither a character nor a repro.
The cited DFIR.ru passage ("a system update leading to these file names being
mapped into identical forms… a file system driver will unexpectedly find two
existing files sharing the same name") is about **one filesystem's own table
changing across an OS update** — an NTFS-vs-future-NTFS hazard — not about the
engine's fold disagreeing with NTFS. It is quoted out of its scenario.

### 3. The "a-z passes through the table" quirk is a no-op for the failure mode
The DFIR quote that ASCII a-z is routed through `$UpCase` is offered as
evidence of divergence, but a-z → A-Z is exactly what Unicode simple fold does
for ASCII too. It introduces no engine/NTFS disagreement, so it cannot be a
source of the claimed finer-pair clobbers.

### 4. It demands the design exceed its own validated reference for an unobserved bug
Syncthing — cited as "the reference posture" throughout `case-sensitivity.md`
and the decision — performs case-collision detection with Unicode
lower/fold semantics plus directory-listing checks; it does **not** reconstruct
NTFS `$UpCase`. If "the fold must be derived from the target's frozen table or
no-clobber is unsound" were a live, high-severity hazard, the mature
production reference would have surfaced it. The finding asks the design to
out-engineer the gold standard against a failure that the gold standard has
not hit in the field.

### 5. Severity "high" is overstated — the NTFS side is already gated
The decision (`case-and-normalization-collision-policy.md`, Consequences) and
both findings already state the NTFS behaviour "cannot be fully verified on the
Mac," and route closure through the Phase-6 `windows-latest` CI job +
`CROSS_PLATFORM_CHECKLIST.md`, with an explicit collision test obligation. This
is a known, scheduled verification gap, not an un-caught design hole. A
Phase-3 design critique flagged "high / silent data loss" against an area the
plan has *already* fenced for empirical Phase-6 confirmation is double-counting
risk that the process owns.

### 6. The recommendation is incremental hardening, not a refutation of soundness
Recommendation (b) — directory-list the target and let the FS's own verdict
decide before `os.Rename` — is a reasonable belt-and-suspenders refinement, and
it overlaps what the decision already cites ("detect the real on-disk case
using directory listing methods"). But (a) "probe equivalence per character
class in use" is heavy machinery for an unquantified hazard, and even (b) still
needs *some* canonical-key comparison to decide the colliding entry is a
"different canonical key," so it does not eliminate the engine's fold from the
loop — it only adds an OS existence gate. That is a worthwhile apply-path TODO,
not evidence the *design* is unsound.

## What I concede (kept honest)
- It is literally true that `unicode.SimpleFold(NFC(name))` is not the NTFS
  `$UpCase` function, and a naive `os.Rename(tmp, dst)` without an OS-level
  existence check could overwrite a differently-cased on-disk entry. A
  pre-rename directory-listing existence check (rec. (b)) is a sensible
  hardening worth folding into the WS-4 apply path / Phase-6 test matrix.
- That concession is an implementation-hardening note, not a high-severity
  design defect: the dangerous direction is unsubstantiated, the cited
  divergence mechanism points the safe way, and the area is already gated for
  Phase-6 empirical closure.

## Verdict
The finding is weak where it matters most: the data-loss claim rests on an
existence assertion with no exhibited character and a self-contradicting
mechanism, leans on a quote about a different scenario, and over-states
severity against an already-deferred verification track. Refute; salvage rec.
(b) as a Phase-6 hardening note, not as confirmation of the finding.

VOTE: REFUTED
