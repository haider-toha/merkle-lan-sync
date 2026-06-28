---
id: concurrency-critic-4
title: GR-13's "channel has exactly one closer (the sender side)" is unsafe for the multi-sender fan-in channels the design mandates, and "close conn to cancel a blocked read" races the writer; both yield a panic/crash on shutdown or disconnect
severity: medium
status: rejected
---

# concurrency-critic-4 — Channel-close rule contradicts the multi-sender fan-in; cancel-by-close races the writer

## Claim

Two channel/cancellation rules in the design are correct for the simple 1-sender /
1-receiver case but are **wrong for the topology the design actually uses**, and
each produces a crash rather than a graceful shutdown:

1. **Fan-in close.** GR-13 states "A channel has exactly one closer (the sender
   side); never close from the receiver." But `inboundMsgs` (and `peerEvents`) are
   **fan-in**: *many* per-connection reader goroutines send to one engine receiver.
   There is no single sender to be "the one closer." Following GR-13 literally —
   "the sender side closes" — means *some* reader closes a channel the *other*
   readers are still sending on → `panic: send on closed channel`. The only safe
   choices both contradict GR-13 as written: either the **receiver** closes (GR-13
   forbids it, and it still races in-flight senders) or the channel is **never
   closed** (GR-13 says it must have a closer).
2. **Cancel-by-close.** GR-4 says blocking reads are made cancellable by closing
   the conn/socket. But the same `tls.Conn` is used by the per-conn **writer**
   goroutine (concurrency-critic-3); closing it to unblock the reader, while the
   writer is mid-`Write`, races unless the close is `sync.Once`-guarded and ordered.
   The design names "ctx-cancel/close" (`structure.md:93`) but specifies neither the
   once-guard nor who owns the close.

Net: at **shutdown** a fan-in reader can panic the daemon on a closed channel, and
on **disconnect** an unguarded close can double-close / race the writer.

## Evidence

The fan-in topology (many senders → one receiver):

- `docs/audit/rules/go-rules.md:84` (GR-4) — per connection there is "one reader
  ... goroutine **per** connection"; all of them feed the engine.
- `docs/audit/rules/go-rules.md:85-88` (GR-4) — listeners "communicate by sending
  values on channels to the reconcile core ... the **single consumer**." Multiple
  producers, one consumer = fan-in.
- `docs/audit/plan/structure.md:114` — engine "consumes `fsChanges`/`peerEvents`/
  `inboundMsgs`"; `peerEvents` likewise has multiple producers once a
  transport-disconnect producer is added (see concurrency-critic-3).

The rule that breaks on that topology:

- `docs/audit/rules/go-rules.md:256-258` (GR-13) — "A channel has **exactly one
  closer (the sender side)**; never close from the receiver."

Why it crashes (standard Go semantics): sending on a closed channel **panics**
(`panic: send on closed channel`), and closing an already-closed channel panics.
With N reader goroutines as senders, there is no race-free way for "the sender
side" to close `inboundMsgs` while peers are still being torn down — whichever
reader closes it can be beaten by another reader's in-flight send. The documented
safe pattern for fan-in is the opposite of GR-13: **do not close the channel at
all**; signal completion via `ctx` + a `WaitGroup` over the senders, and let the
receiver drain until all senders have exited and the channel is simply abandoned
(this is the canonical Go pipeline-shutdown guidance).

Cancel-by-close race:

- `docs/audit/rules/go-rules.md:89-92` (GR-4) — "close the connection/socket on
  cancel so the blocked `Read` returns an error." Combined with the per-conn writer
  on the same `conn` (`structure.md:93`), an unguarded `conn.Close()` from the
  cancel path races the writer's `Write` and a second close from the writer's own
  error path.

## Impact

- **Medium.** The failure mode is a **crash** (`panic: send on closed channel` or a
  double-close panic), not silent data corruption — SR-1's temp-then-rename means an
  in-flight transfer interrupted by the panic leaves no torn file, so this is a
  data-*availability* bug, not data-loss. But it triggers on the most routine events
  (process shutdown; peer churn), so left unaddressed it is a recurring, hard-to-
  reproduce crash. It also directly **mis-guides implementers**: GR-13 is a hard
  rule, and following it literally on the fan-in channels *causes* the panic.
- The cancel-by-close race is timing-dependent and would surface intermittently
  under `-race` (or as a rare double-close panic) on peer disconnect — i.e. it can
  pass CI and bite in the field.

## Recommended-change

1. **Amend GR-13 for fan-in.** Add: *"A fan-in channel (multiple senders, one
   receiver) is **never closed**. Shutdown is signalled by cancelling `ctx`; each
   sender exits its send loop on `ctx.Done()`; an owning `WaitGroup` waits for all
   senders; the receiver drains until `Wait` completes, then stops. The 'exactly one
   closer' rule applies only to single-sender channels (e.g. a per-conn outbound
   channel, closed by that conn's writer-owner)."* This keeps GR-13 correct for the
   channels it *does* fit (per-conn outbound) and safe for the fan-in channels it
   currently breaks.
2. **Make per-conn close idempotent and owned.** Wrap `conn.Close()` in a
   `sync.Once` per connection; only the per-conn owner (concurrency-critic-3's
   `connCancel` path) closes it. Reader and writer both observe the closed conn /
   cancelled `connCtx` and return; neither closes the conn directly except through
   the once-guard. This removes the double-close / write-vs-close race.
3. **Document drain-on-shutdown.** The engine, on root-`ctx` cancel, stops its
   select loop only after the listener/dialer `WaitGroup` reports all per-conn
   senders gone, so no sender can send into a torn-down engine. Pair with the
   per-conn WaitGroup from concurrency-critic-3.
4. **Test:** a shutdown-under-load test (peers actively sending while root ctx is
   cancelled) asserting no panic and a clean drain, run with `-race`; a
   disconnect-during-write test asserting no double-close panic.

This beats the status quo because GR-13 as written is not merely incomplete — it is
*actively unsafe* for the fan-in channels GR-4 mandates; the amendment makes the
documented rule match the topology and replaces a panic-on-shutdown with a
ctx+WaitGroup drain that is the standard, race-detector-clean Go pattern.
