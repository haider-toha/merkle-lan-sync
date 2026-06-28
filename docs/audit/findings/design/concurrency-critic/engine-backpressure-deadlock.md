---
id: concurrency-critic-2
title: The single reconcile consumer sits inside a cyclic wait — it is the sole drainer of inboundMsgs yet must also produce outbound frames, so under TCP back-pressure two peers deadlock (the real "watcher vs sync-write" deadlock surface)
severity: high
status: rejected
---

# concurrency-critic-2 — Back-pressure deadlock: the engine both drains inbound and produces outbound, on one goroutine

## Claim

The task asks "can the watcher and a sync write deadlock?" The literal
watcher-vs-write case is mostly closed by GR-5 ("zero I/O under the lock"), but the
**real** deadlock surface is elsewhere and the design walks straight into it. GR-4
makes the reconcile core "the **single consumer**" of `fsChanges`, `peerEvents`,
and `inboundMsgs`. That same core is also the producer of outbound protocol frames
(it must answer an inbound `INDEX` with `REQUEST`s, and a `REQUEST` with
`RESPONSE` chunk data — SKILL §5 message table). So the engine is simultaneously
(a) the *only* goroutine draining inbound and (b) a *blocking producer* of
outbound. Under TCP/TLS back-pressure (the peer's socket buffers fill because it
is busy sending to us), the engine blocks in an outbound send while it is the only
thing that could drain inbound; the peer's per-connection **reader** then blocks
sending the next message on `inboundMsgs`; both peers wedge. The per-connection
"separate reader + writer goroutine" pattern (`structure.md:93`) does **not** break
this cycle, because the cycle runs *through the engine*, not through the conn.

## Evidence

The single-consumer + producer shape, from the design:

- `docs/audit/plan/structure.md:114` — "`engine.go` | core loop: owns `RWMutex` +
  tree; **single writer**; **consumes `fsChanges`/`peerEvents`/`inboundMsgs`**."
- `docs/audit/rules/go-rules.md:85-88` (GR-4) — "listeners ... communicate by
  sending values on channels to the reconcile core ... The reconcile core is the
  **single consumer**."
- The engine must also *emit* protocol traffic in direct response to inbound:
  SKILL §5 (`.claude/skills/merkle-sync/SKILL.md:229-238`) — a `REQUEST` is
  answered by `RESPONSE` chunk data, an `INDEX` triggers `REQUEST`s; SKILL §5
  line 218-219 — "Bulk file content is streamed as **many** small chunk messages."
- Per-conn reader/writer split: `structure.md:93` — "`conn.go` | ... per-conn
  reader + writer goroutines."

The deadlock interleaving (both peers symmetric; one connection between A and B):

```
1. A's engine is streaming a large file to B: it emits hundreds of RESPONSE
   frames (SKILL §5). It writes them either directly to B's conn or by sending to
   B-conn's bounded outbound channel.
2. B is doing the same to A (large file the other direction). B is busy applying +
   not draining its socket as fast as A produces.
3. TCP back-pressure: A's writes to B block (B's receive window / A's send buffer
   full). A's engine is now parked in send.
4. Because A's engine is parked, it is NOT draining A's `inboundMsgs`.
5. A's per-conn READER (decoding frames B sent in step 2) blocks trying to hand
   the next frame to A's engine via `inboundMsgs` (channel full / no receiver).
6. Symmetrically on B. Neither engine returns to its select loop. DEADLOCK —
   no ctx cancellation fires (nothing crashed), no error returns; both daemons
   hang with sync incomplete.
```

This is the textbook "request/response handled on the same goroutine that must
also read replies" deadlock, and the standard distributed form "both peers writing,
neither reading." The reader/writer split *would* solve the pure-transport version
— but only if the engine never blocks on outbound. The design's single-consumer
engine violates exactly that precondition. GR-4 even names the precondition for
reads ("blocking network reads must be made cancellable," `go-rules.md:89`) but
states **no** equivalent rule for the engine's outbound sends, which is where this
deadlock lives.

Note this is *worse* than a per-connection hang: because there is **one** engine
for **all** peers and subsystems, an outbound block on a single slow/wedged peer
also stops the engine from draining `fsChanges` and `peerEvents` — so a single
back-pressured peer freezes local change detection and discovery for the whole
daemon (head-of-line blocking across unrelated work).

## Impact

- **High.** Two peers mid-bulk-transfer (the common case for an initial sync of a
  large folder) can deadlock with no error and no recovery short of killing a
  daemon. Convergence (SR-5) never completes. Because nothing panics or cancels,
  the failure is silent — the worst kind for a "walk-away" daemon.
- **Amplified blast radius:** the single shared engine means one wedged peer also
  starves `fsChanges`/`peerEvents`, so local edits stop syncing and new peers stop
  being processed — the whole daemon is effectively dead while appearing alive.
- It is also **invisible to the planned tests** unless an integration test forces
  simultaneous large bidirectional transfers with constrained socket buffers; the
  unit-level `transport_test.go` "split-frame survival" (`structure.md:96`) will
  not surface it.

## Recommended-change

Break the cycle by guaranteeing the engine **never blocks on outbound while it is
the sole inbound drainer**. Concretely, in priority order:

1. **Decouple outbound from the engine via per-conn writer goroutines that own the
   socket, fed by a per-conn outbound channel — and make the engine's send
   non-blocking-or-shed.** The engine's emit step must be `select { case
   outCh <- frame: default: /* mark peer slow */ }` or send with a bounded policy
   that, on a full buffer, *drops the peer* (cancel its per-conn ctx, deregister)
   rather than parking the engine. The writer goroutine is the only thing that ever
   calls `conn.Write`; it absorbs TCP back-pressure without involving the engine.
2. **Make the bulk-transfer producer its own goroutine, not the engine.** Streaming
   a file's `RESPONSE` chunks (the high-volume path) should run in a per-transfer
   or per-conn goroutine that the engine *spawns and owns* (WaitGroup, GR-3), so a
   slow peer back-pressures only that goroutine, never the engine's select loop.
   The engine hands off "serve these chunks to peer P" and returns immediately.
3. **State the missing rule.** Add a GR-4 companion to the existing "blocking reads
   must be cancellable": *"the reconcile core must never perform a blocking send to
   a peer from its main select loop; outbound is owned by per-conn writer goroutines
   and is either buffered-with-shed or offloaded."* This is the symmetric partner
   to `go-rules.md:89` and closes the deadlock by construction.
4. **Add the proof test:** an integration scenario with two in-process instances,
   small socket buffers, each pushing a large file to the other simultaneously;
   assert convergence completes within a timeout (a hang = the deadlock). This is
   the system-level version of the leak/deadlock obligation the concurrency-critic
   is graded on.

This beats the status quo (a single-consumer engine that both drains inbound and
blocks on outbound) because it removes the engine from the wait cycle entirely:
back-pressure is absorbed by per-conn writer goroutines that do nothing but write,
so a slow peer can never stall inbound draining, local change detection, or
discovery.
