# Skeptic #1 vote — protocol-critic-1 (Framing MaxFrameLen accounting)

VOTE: REFUTED (the *high-severity* design-defect claim is overstated; the
residual content is a known, already-deferred implementation-tuning nit).

## What I checked

- The finding file itself.
- `docs/audit/decisions/phase0/framing-format.md` (the framing decision).
- `docs/audit/findings/protocol/PR-1-wire-protocol-and-framing.md` (the wire grammar).
- `plan/README.md` (the 32 KiB chunk baseline).

## Why the finding does not hold up as a high-severity design defect

### 1. The central "livelock from a *conformant* puller" claim is self-contradicted by the cited spec.

The finding's worked trigger is `REQUEST{length = 16 MiB}` and it asserts this is
"triggered by a *conformant* puller, because the spec defines no clamp."

But the spec it cites does define a clamp. PR-1 §4 line 125 states the field as
`length : uint32  # ≤ MaxFrameLen - overhead`. That is the conformance bound. A
puller that sends `length = 16 MiB` is **violating the stated grammar**, so it is
non-conformant by definition. The finding cannot simultaneously cite that line as
evidence and claim "no enforced bound / conformant puller." The only genuine gap is
that the word "overhead" is not given a numeric value — a documentation nit, not a
stream-corruption defect. A malicious/non-conformant peer being dropped on a bad
length is the *designed* behaviour (the max-length guard "turns a textbook DoS into a
dropped connection," framing-format.md line 133), not a bug.

### 2. The off-by-one is not a miscount; the design states the exact formula.

`L = 1 + len(payload)` is stated unambiguously in *both* documents
(framing-format.md lines 42-43, 97-99; PR-1 §2 lines 33-36, 40). From that, "max
payload = MaxFrameLen − 1" and "max RESPONSE data = MaxFrameLen − 6" are trivial
one-line derivations the implementer performs from the published formula — exactly
what the `≤ MaxFrameLen − overhead` comment directs them to do. Calling a
to-be-computed constant an "off-by-one bug in the protocol" is mislabeling a normal
implementation step as a design flaw. There is no desync: the reader rejects
*before* allocating and returns a typed sentinel; the stream is never corrupted.

### 3. The "16 MiB natural round value" scenario is a strawman; the real chunk size makes the byte-accounting margin ~500×.

The baseline chunk size is **32 KiB** (README line 97 "32KB chunk streaming";
framing-format.md line 105 names fixed-32 KiB as the candidate). 32 KiB against a
16 MiB ceiling is a ~512× margin — an off-by-6-bytes is irrelevant at that scale.
framing-format.md lines 103-107 explicitly say bulk content is "streamed as many
small chunk messages ... well under the ceiling" and that the per-chunk size is a
**separate decision deferred to the merkle/reconcile workstream.** The finding reads
"deferred + well under the ceiling" as "nobody computed it / will ship a 16 MiB
chunk," which is not a fair reading of an explicitly-bounded open sub-decision.

### 4. The recommended change is mostly already on the roadmap.

"Pin `MaxChunkLen` and enforce on the sender" is precisely the deferred
merkle/reconcile chunk-size decision (framing-format.md lines 103-107, 148). The
sender-side assert and a typed `INVALID_REQUEST` code are reasonable *hardening*
additions, but they refine an already-correct design rather than fix a stream-
severing defect. They belong in the chunk-size decision and the validation rule, at
**low/medium** severity, not as a "high" framing-corruption finding.

## What I concede (does not change the vote)

Three genuine but minor improvements survive: (a) give "overhead" a concrete value /
named constant; (b) add a sender-side size assertion so a budget bug fails on the
offender; (c) add an `INVALID_REQUEST` error code for graceful decline. These are
worth doing during implementation. None of them rises to "permanently severs the
connection / blocks SR-5 for any conformant transfer" — that impact statement
requires a non-conformant peer and ignores the 32 KiB baseline.

## Verdict

The evidence is real but is read uncharitably: the spec already states the puller
bound and the exact length formula, defers (with an explicit upper bound) the chunk
size, and uses 32 KiB chunks with a ~512× margin. The high severity and the
"conformant puller livelock" framing are overstated. Recommend downgrade to a
low/medium implementation-hardening note folded into the chunk-size decision.

refuted = true; confidence = medium.
