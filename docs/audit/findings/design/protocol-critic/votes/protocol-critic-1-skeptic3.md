# Skeptic #3 vote — protocol-critic-1 (Framing MaxFrameLen accounting)

- Finding: protocol-critic-1 — "Framing MaxFrameLen accounting is unpinned — type-byte
  off-by-one + unbounded peer-chosen REQUEST.length turn a valid-intent transfer into an
  ErrFrameTooLarge connection-drop livelock"
- Role: skeptic #3 of 3 (charge: REFUTE)
- Vote: **REFUTED** (confidence: medium)
- Date: 2026-06-28

## Verdict

The finding dresses a minor, already-deferred spec-tightening item up as a **high**-severity
"permanent non-convergence + connection-drop livelock." The worked trigger contradicts the
project's own pinned chunking decision, the claimed missing error path already exists, and the
"unpinned contract" framing is contradicted by the very spec lines the finding cites. The
legitimate kernel (define a named `MaxChunkLen`, add writer-side asserts) is a worthwhile
WS-2/WS-4 implementation-checklist item, not a high-severity design defect.

## Why the evidence does not support the claimed severity

### 1. The worked trigger contradicts the project's own pinned chunk size.

The finding's entire livelock scenario rests on a `REQUEST{length = 16 MiB}` / 16 MiB chunk.
But the transfer chunk size is **already decided and pinned**:
`docs/audit/decisions/merkle/chunking-fixed-32kib-vs-cdc.md` lines 112-114 — "Streaming chunk
= the same 32 KiB carried in one `RESPONSE` frame (≪ the MaxFrameLen ceiling)." That is
32 KiB against a 16 MiB ceiling — a **512×** headroom margin.

Consequently consequence #1 (the type-byte / RESPONSE-header off-by-one) is a non-issue for the
actual design: the difference between `MaxFrameLen - 1` and `MaxFrameLen - 6` only ever matters
within 6 bytes of the boundary, and the design operates ~16 million bytes below it. The
off-by-one is real arithmetic but has **zero reachable consequence** at the chosen operating
point.

The finding asserts the livelock is "triggered by a *conformant* puller, because the spec
defines no clamp." That is false: a conformant puller in this design requests 32 KiB chunks
(the pinned `BlockSize`). A puller emitting `REQUEST{length = 16 MiB}` is **non-conformant** —
it violates the chunking decision, not merely an unstated framing rule.

### 2. The "no error code to decline" claim is wrong — GENERIC already exists.

The finding (Impact bullet 3; Evidence line 54) claims "there is no error code for 'request
too large'… so the source cannot decline cleanly," forcing the failure through
`ErrFrameTooLarge`. But the over-large `REQUEST` is itself a tiny, fixed-size frame
(`reqID:u32 + path + hash:32 + offset:u64 + length:u32` ≈ tens of bytes) — it is received
without any framing problem. The source then **validates the request before building a
RESPONSE** and, if it cannot serve it, answers `RESPONSE{errorCode = 1 GENERIC, data = empty}`.
`errorCode = GENERIC` is exactly the BEP-modeled "I can't serve this" reply
(PR-1 §4 note, lines 142-144: "a source that no longer has a requested block answers cleanly
rather than hanging the puller"). The connection survives; no `ErrFrameTooLarge` is ever
produced.

The claimed livelock therefore requires **two independent implementation bugs that contradict
the documented design**: (a) a non-conformant puller requesting 16 MiB, AND (b) a source that
blindly echoes the requested length into an over-budget RESPONSE frame instead of validating
and declining. That is a strawman source, not the BEP-modeled source the design specifies. A
chain of two contradicted implementation choices is not a high-severity design defect.

### 3. The contract is not "unpinned" — the writer budget is fully determined.

The title's load-bearing word is "unpinned," but the spec the finding cites pins the exact
arithmetic: `framing-format.md` line 41-42 and PR-1 §2 line 34 both state `L = 1 + len(payload)`.
That single equation fully determines the writer-side budget (`len(payload) ≤ MaxFrameLen - 1`);
the writer trivially derives it. The `REQUEST.length` field even carries the bound inline
(`≤ MaxFrameLen - overhead`, PR-1 line 125). So both halves of the contract are present; one is
informal. "Unstated" / "unpinned" overstates a documentation looseness (an undefined word
"overhead" and an uncomputed exact `MaxChunkLen`).

### 4. Threat model: TOFU-paired peers, where a malicious peer self-harms.

The only way to reach the livelock with a genuinely conformant source is a **malicious** puller.
But peers are TLS-1.3 mutually authenticated and pinned to a TOFU allow-list
(PR-1 §5 step 1; `transport-security-tofu`). A paired-and-trusted peer that deliberately drops
its *own* connection harms only itself and can already do far worse (lie about file content,
withhold chunks). "Permanent non-convergence" for the *folder* is overstated: it is at worst a
self-inflicted stall on one misbehaving peer's own pull, recoverable by any conformant peer.

### 5. The chunk-size budget was deliberately and correctly deferred — not omitted.

`framing-format.md` lines 104-107 and 146-148 explicitly hand per-chunk sizing to the
merkle/reconcile workstream ("a separate decision deferred… stays well under the ceiling").
That workstream then pinned 32 KiB. Demanding that the Phase-0 framing decision also pin
`MaxChunkLen` is asking it to do a job it documented as belonging elsewhere — and which was
subsequently done. This is the documented division of labor working as intended, not a gap.

## What survives

A genuinely useful but **low/info**-severity hardening kernel remains, and it should be folded
into the WS-2 (transport) / WS-4 (reconcile) implementation checklist rather than logged as a
high design finding:

- Name the constant `MaxChunkLen = MaxFrameLen - 1 - ResponseHeaderLen(5)` in
  `internal/protocol` and add a `WriteFrame` assert `1 + len(payload) ≤ MaxFrameLen`. Cheap
  defense-in-depth; turns any future budget bug into a loud sender-side error.
- Validate `REQUEST.length` on receipt and decline with the **existing** `GENERIC` code (a new
  `INVALID_REQUEST` code is optional polish, not required to avoid the livelock).
- The idle-read-deadline item (Recommended-change #4) is the finding's own admission it is a
  *distinct* root cause already owned by transport/concurrency-critic — not this finding's
  charter.

None of these rises to "high / permanently severs the connection / blocks SR-5 convergence."

## Conclusion

The finding is **overstated**: its headline livelock requires a non-conformant puller plus a
non-validating source, both contradicting pinned decisions (32 KiB chunks; BEP source-side
serving with GENERIC declines); its "no error code" premise is false; and "unpinned" misreads a
spec that already fixes `L = 1 + len(payload)`. Per the skeptic default (refute when the claim
is overstated/unsupported at the asserted severity), I vote REFUTED. The salvageable hardening
belongs on an implementation checklist at info/low severity.

VOTE: REFUTED
