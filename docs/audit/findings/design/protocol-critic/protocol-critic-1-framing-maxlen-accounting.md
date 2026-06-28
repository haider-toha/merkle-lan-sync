---
id: protocol-critic-1
title: Framing MaxFrameLen accounting is unpinned — type-byte off-by-one + unbounded peer-chosen REQUEST.length turn a valid-intent transfer into an ErrFrameTooLarge connection-drop livelock
severity: high
status: rejected
area: framing
---

# protocol-critic-1 — Framing length budget is specified for the reader but not the writer/puller; the off-by-one and the unbounded `REQUEST.length` desync/sever the stream

## Claim

The framing design pins exactly one half of the contract — the **reader's** reject
rule `0 < L <= MaxFrameLen` where `L` counts `type byte + payload` — and leaves the
**complementary writer-side and puller-side size budget unstated**. Two concrete
consequences fall out, both squarely in this critic's charter ("length-prefix
off-by-one / unbounded length DoS corrupts or hangs the stream"):

1. **Off-by-one on the type byte (and the RESPONSE header).** Because `L = 1 +
   len(payload)`, the maximum *payload* is `MaxFrameLen - 1`, not `MaxFrameLen`; and
   for a `RESPONSE` the chunk `data` field has a further 5 bytes of envelope
   (`reqID:u32 + errorCode:u8`) in front of it, so the largest chunk that fits is
   `MaxFrameLen - 6`. Nobody has computed that number. An implementer who sets the
   chunk size to a natural round value (`16 MiB`) — or who subtracts only the one
   type byte they can see in the spec (`MaxFrameLen - 1`) — produces a frame the
   receiver rejects with `ErrFrameTooLarge` and drops the peer.

2. **Unbounded, peer-chosen `REQUEST.length`.** `REQUEST.length` is a `uint32` the
   *puller* picks, with no enforced bound (only a comment "`<= MaxFrameLen -
   overhead`", and "overhead" is undefined). One `REQUEST` is answered by one
   `RESPONSE` (the grammar says "for **a** prior REQUEST"). A request with `length
   >= MaxFrameLen - 5` forces the source to emit a `RESPONSE` frame the receiver
   *always* rejects. There is no error code for "your requested length exceeds my
   frame limit" (the enum is `0 OK, 1 GENERIC, 2 NO_SUCH_FILE, 3 INVALID_FILE`), so
   the source cannot decline cleanly; the puller reconnects, re-requests, is dropped
   again — a **livelock**, and that byte range (hence that file) never converges.

This is not a clean error path. It is a handful-of-bytes miscount in the protocol's
size budget that **permanently severs the connection and blocks SR-5 convergence**
for the affected file — the highest-leverage framing bug class.

## Evidence

- `docs/audit/decisions/phase0/framing-format.md`: `L = 1 + len(payload)` so empty
  payload ⇒ `L == 1` (lines 42-43, 97-99); `MaxFrameLen = 16 MiB`, reject
  `L == 0 || L > MaxFrameLen` *before* allocating (lines 39-40, 102); "Bulk file
  content is streamed as many small chunk messages ... well under the ceiling"
  (lines 104-107) — the **exact** maximum chunk is never pinned, only "well under".
- `docs/audit/findings/protocol/PR-1-wire-protocol-and-framing.md` §4: `REQUEST` =
  `... | length : uint32  # <= MaxFrameLen - overhead` is the *only* bound, a
  comment with no validation rule and an undefined "overhead" (lines 121-126);
  `RESPONSE = reqID:u32 | errorCode:u8 | data:(rest of frame)` (lines 127-130);
  "chunk data ... for **a prior REQUEST**" (line 79) ⇒ one REQUEST.length maps 1:1
  to one RESPONSE data size; error enum has no "request too large" code (line 128).
- `docs/audit/rules/sync-rules.md` SR-12 (lines 206-219) and
  `docs/audit/rules/go-rules.md` GR-8 (lines 162-167) specify **only** the reader's
  `io.ReadFull` + reject-before-alloc guard; nothing constrains the writer or the
  puller's chosen `length`.
- The framing decision itself names this class as the thing to hunt:
  "a single off-by-one in a length prefix desynchronises every subsequent message"
  (framing-format.md lines 16-19, 118-121); `.claude/agents/protocol-critic.md`
  lines 16-19.
- **Worked trigger.** Puller sends `REQUEST{length = 16 MiB}` (a legal `uint32`,
  unclamped by the spec). Source builds `RESPONSE`: frame `L = 1(type) + 4(reqID) +
  1(errorCode) + 16 MiB = 16 MiB + 6 > MaxFrameLen`. Receiver's `ReadFrame` returns
  `ErrFrameTooLarge` → transport drops the peer (PR-1 §2 "the transport then drops
  that peer"). Reconnect → same diff → same REQUEST → dropped again. The file is
  never transferred; `HELLO.rootHash` never matches; SR-5 "converged ⇔ equal root"
  is unreachable for that file.
- Max-message sanity-check provenance: Stephen Cleary, *Message Framing* — the
  guard "turns a textbook DoS into a dropped connection" but says nothing about the
  *sender* staying under the limit (https://blog.stephencleary.com/2009/04/message-framing.html,
  accessed 2026-06-28).

## Impact

- **Permanent non-convergence + connection-drop livelock** for any file whose
  transfer is driven by an over-budget `REQUEST.length` or an over-budget chunk
  sizing — triggered by a *conformant* puller, because the spec defines no clamp.
- The off-by-one is silent and easy to ship: `MaxFrameLen - 1` *looks* correct
  (accounts for the type byte) but still overshoots a `RESPONSE` by 5 bytes.
- No clean degradation: the absence of a "request too large" error code forces the
  failure through `ErrFrameTooLarge` (peer-dropping) rather than a recoverable
  decline the puller can act on.

## Recommended-change

1. **Pin one named constant and enforce it on the sender.** Define
   `MaxChunkLen = MaxFrameLen - FrameTypeLen(1) - ResponseHeaderLen(5)` in
   `internal/protocol`. `WriteFrame` MUST assert `1 + len(payload) <= MaxFrameLen`
   and the `RESPONSE` builder MUST assert `len(data) <= MaxChunkLen`, returning a
   typed error *before* sending — so a budget bug fails loudly on the offender, not
   as a peer-dropping `ErrFrameTooLarge` on the victim.
2. **Validate `REQUEST` on receipt.** Reject `length == 0 || length > MaxChunkLen`
   (and `offset + length` beyond the advertised size) with a **new error code**
   (e.g. `4 = INVALID_REQUEST`) so the puller shrinks and retries instead of
   livelocking. The puller MUST clamp `REQUEST.length <= MaxChunkLen` and split a
   large range across multiple `REQUEST`s.
3. **Test obligations:** a golden test that `MaxChunkLen` round-trips at the exact
   boundary; a test that `REQUEST{length = MaxFrameLen}` is declined by the *source*
   with the typed code (and that the connection survives), not rejected by the
   puller's `ReadFrame` as `ErrFrameTooLarge`.
4. **Defense in depth (flagged; overlaps transport / concurrency-critic):** the
   frame reader also needs a steady-state *idle read deadline*, not only the
   ctx-cancel deadline GR-9 mandates (go-rules.md lines 89-92). A peer that sends
   the 4-byte header for a 16 MiB frame and then stalls pins the allocation and
   parks the reader goroutine indefinitely; pair an idle deadline with PING-miss
   eviction. (Distinct root cause from the size budget; noted, not double-counted.)
