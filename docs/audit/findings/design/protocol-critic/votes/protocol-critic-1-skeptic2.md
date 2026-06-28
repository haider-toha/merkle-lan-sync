# Skeptic #2 vote — protocol-critic-1 (Framing MaxFrameLen accounting is unpinned)

**Vote: REFUTED** (confidence: medium)

## Summary

The finding correctly reports the *arithmetic* (`L = 1 + len(payload)`, so a
`RESPONSE` chunk has 6 bytes of envelope) and correctly notes that
`REQUEST.length` carries only a loose, comment-level bound. But it inflates a
minor, deferred hardening opportunity into a "high-severity, permanent
non-convergence livelock triggered by a conformant puller." The load-bearing
words in the title and impact — "valid-intent transfer" and "conformant puller"
— are not supported by any real scenario once you read the surrounding
decisions. The actual high-leverage protection (the reader-side reject-before-
alloc guard) is already correctly specified, so the residual gap is polish, not
a stream-corrupting defect.

## Why the trigger scenario is not "valid intent" / "conformant"

1. **The worked trigger requires a 16 MiB chunk, which contradicts an already-
   logged decision.** `framing-format.md` lines 103-107 state bulk content is
   "streamed as many small chunk messages, not one giant frame ... it stays well
   under the ceiling," and the plan (`plan/README.md`, "Chunking: fixed 32KB vs
   content-defined") fixes the per-chunk size at ~32 KiB. Against a 16 MiB
   ceiling that is a ~500x margin. A 6-byte off-by-one is physically
   unreachable at 32 KiB. The finding's "implementer who sets the chunk size to
   16 MiB" is not a conformant implementer — it ignores the chunking direction
   the design already took.

2. **A puller that sets `REQUEST.length = 16 MiB` is not conformant either.**
   PR-1 §4 line 125 already states `length : uint32 # <= MaxFrameLen -
   overhead`. A puller emitting `length = MaxFrameLen` is violating the stated
   (admittedly loose) bound. The finding equates "a legal `uint32` value" with
   "conformant"; those are not the same. The grammar expresses the intended
   constraint; the gap is that it is unenforced, not that the design *invites*
   16 MiB requests.

3. **Against a peer that deliberately ignores the bound, dropping the
   connection is correct and already-specified behaviour.** The threat model
   (transport-security TOFU) is *semi-trusted* LAN peers. An authenticated peer
   that intentionally requests oversized frames is misbehaving, and such a peer
   can DoS in countless cheaper ways (send garbage, never respond, half-open the
   header — the finding's own item 4 concedes the stall case is a *distinct*
   root cause). The `ErrFrameTooLarge`-drop is the textbook-correct outcome the
   framing decision was designed to produce (framing-format.md lines 131-132).
   Calling the resulting reconnect a "livelock" reframes correct adversarial
   handling as a defect.

## Why the severity is overstated

- **The real DoS class — unbounded length → OOM / desync — is already closed.**
  SR-12 / GR-8 mandate `io.ReadFull` + reject `length == 0 || length >
  MaxFrameLen` *before* allocation (framing-format.md 39-40, 102; PR-1 §2). The
  finding does not dispute this; it concedes the guard works. What remains is a
  *sender-side* assertion and a *receiver-side REQUEST validation* — robustness
  hardening, not a stream-corruption bug. The "off-by-one desynchronises every
  subsequent message" framing borrowed from the framing decision does **not**
  apply here: an over-budget frame is rejected atomically before any body read,
  so the stream never desyncs — the connection simply drops. There is no
  cross-message corruption, the signature harm of the off-by-one class.

- **The unstated constant is expected at this phase, not a defect.**
  framing-format.md lines 104-106 explicitly *defer* per-chunk size ("a separate
  decision deferred to the merkle/reconcile workstream"). Criticising a Phase
  0/2 envelope spec for not pinning a number that, by the design's own
  structure, belongs to a later workstream is premature. `MaxChunkLen =
  MaxFrameLen - 6` is naturally pinned when chunk size is decided; its absence
  now is sequencing, not an off-by-one shipped to production.

## On the recommended change vs status quo

The recommendations are sensible (define the overhead constant, assert on the
writer, add an `INVALID_REQUEST` code, clamp `REQUEST.length`) and I would not
oppose folding the REQUEST-validation + a typed decline code into the
implementation workstream as low-priority hardening. But they do **not** "beat"
a broken status quo, because the status quo — small (32 KiB) chunks plus the
already-mandated reader guard — already converges every valid transfer. The
change defends only against a peer that deliberately or buggily ignores both the
chunking decision and the stated `length` bound. That is a real but **low/medium**
robustness improvement, not the "high-severity, blocks-SR-5-convergence" defect
the finding asserts. Severity and the "conformant / valid-intent" framing are
overstated; the genuine remediation is a one-line clamp + one enum value, easily
caught by the finding's own proposed unit test.

## Conclusion

REFUTED. The arithmetic is right but the severity, the "valid-intent" title, and
the "conformant puller" trigger are not supported: the only paths to the claimed
livelock require ignoring the already-logged 32 KiB chunking decision or the
already-stated `length <= MaxFrameLen - overhead` bound, i.e. a buggy/malicious
peer for which connection-drop is the correct, designed response. The actual DoS
guard is already specified and uncontested. Worth a low-priority hardening
ticket (REQUEST validation + `INVALID_REQUEST` code), not a high finding.
