# Skeptic #1 vote — protocol-critic-3 (conflict-copy non-deterministic identity)

**Vote: REFUTED** (confidence: medium)

I was assigned to refute. After reading the finding and every cited source
(`PR-3` §2/§4/§6/§7, `leaf-shape-and-structural-hash.md` D.1/D.3, `sync-rules.md`
SR-5, Syncthing's `conflictName`), I conclude the finding overstates a minor
prose-clarity gap into a "high"-severity non-convergence defect, leans on a
strawman, and recommends what the spec already intends and tests. The premise
(VV is in the structural hash, confirmed at `leaf-shape-and-structural-hash.md:113`
and SR-5 `sync-rules.md:76-88`) is correct, but the conclusions drawn from it are not.

## 1. The `time.Now()` charge is a strawman the spec explicitly forecloses

The finding's "Duplication" impact rests entirely on the conditional *"If the
timestamp is the resolution-time wall clock (exactly what Syncthing's conflictName
uses: `time.Now()`)."* PR-3 never says `time.Now()`. That implementation choice is
imported from Syncthing and projected onto a spec that says the opposite:

- PR-3 §6 (lines 124-131) frames the UTC date/time as *"a cross-platform
  **determinism requirement**"* — the entire reason §6 specifies a fixed UTC format
  is to make the name reproducible across peers, which `time.Now()` would defeat.
- PR-3 §7 test obligation **#3** (line 140): *"Equal mtime → assert the **same**
  conflict-copy filename is produced on both peers."* A spec that requires a test
  proving identical filenames is not specifying `time.Now()`.
- PR-3 §7 test obligation **#5** (line 143): two instances in different `TZ` produce
  the identical suffix.

So "non-deterministic **as specified**" (the title's load-bearing word) contradicts
the spec's stated determinism requirement and its acceptance tests. The only
plausible deterministic, FileInfo-derived instant is the loser's mtime — which is
exactly the finding's own recommendation #1. The recommendation therefore does **not
beat the status quo**; it restates the spec's evident intent as a one-line
clarification. That is a documentation nit, not a high-severity correctness defect.

## 2. The VV concern is real-but-hedged, misattributed, and unproven

The version-vector half is the only substantive part, and even there the finding
undermines its own severity:

- **Self-hedged.** The finding asks *"the renamer's freshly-bumped VV? the loser's
  original VV? a Merge?"* and recommends *"fix one rule."* That is the language of an
  underspecification to clarify, not a demonstrated divergence. An open choice among
  three convergent options is not a proof of non-convergence.
- **No reproduction.** The contract requires runnable evidence for current-fact
  claims; the finding offers none showing the engine actually fails to reach equal
  roots. It is a textual-gap argument only.
- **Misattributed locus.** The conflict copy is a **new path** that, per PR-3 §2
  (lines 40-46), *"syncs as an ordinary file."* The VV of any newly-created/scanned
  file is governed by the general seeding mechanism, not by PR-3 — PR-3 §8 explicitly
  cross-references `protocol/vv-counter-seeding.md` (incl. the equal-content/
  equal-VV backstop). Whether two independently-created same-content files converge
  on a VV is a general engine property, handled elsewhere; pinning it as a PR-3
  high-severity defect is the wrong address. If `vv-counter-seeding` is itself wrong,
  that is a separate finding against that decision, not against PR-3's symmetry proof.

## 3. The proof's actual load-bearing claim is untouched

PR-3 §4's proof establishes that `W` is **total + commutative** and *"ignores 'local
vs remote' and depends only on intrinsic fields both peers observe identically"*
(lines 111-112). That is what the proof claims and it is sound. The finding reframes
the proof as if it claimed byte-identical *leaves* including VV — but §4's stated
conclusion is that both peers pick the **same loser** and the **same loser-derived
suffix** (`<modified_by>`), which §6 supports. The name's date/time and the copy's VV
are derivations the spec under-documents, not steps the proof asserts and gets wrong.
"Severity: high" is calibrated to a non-convergence-of-the-no-data-loss-path defect;
what actually exists is "add two sentences to §6 pinning timestamp=mtime and the
copy's VV rule." That is low/medium severity at most.

## Where the finding has a grain of merit

PR-3 genuinely does not write the sentence "timestamp = loser mtime, truncated to
whole seconds" nor "conflict copy VV = `Merge(loserVV, winnerVV)`." A one-line spec
clarification (recommendation #1, and one of the #2 options) is worth folding into
PR-3 §6 for the implementer. But that is editorial hardening of an already-determinism-
constrained spec — not the "duplication / non-convergence on the central no-data-loss
path" the finding advertises. The mtime-precision-rounding wrinkle (Mac ns vs FAT
rounding) is a legitimate but pre-existing tier-1 `W` concern, orthogonal to whether
the *symmetric* model is sound.

## Verdict

The evidence does not support the claimed severity: the timestamp charge is a
strawman the spec forecloses (§6 + tests #3/#5), the VV charge is hedged, unproven,
and misattributed away from its real locus (`vv-counter-seeding`), and the
recommended fixes restate the spec's existing intent. The symmetric proof's actual
claim (intrinsic-field-only, total+commutative `W`) stands. REFUTED.
