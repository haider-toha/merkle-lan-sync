---
finding: concurrency-critic-3
skeptic: skeptic2
vote: REFUTE
confidence: medium
date: 2026-06-28
---

# Skeptic #2 vote on concurrency-critic-3 — REFUTE

## Summary

The finding's central claim — "the design's teardown mechanism, as specified, only
handles global shutdown, not the disconnect of one peer" and "there is no specified
... handshake" — is **contradicted by the very evidence the finding cites**. The
per-connection close handshake it calls "missing" is written verbatim in the design
rules, and the file responsible is explicitly bound to that rule. The per-peer-state
half of the leak (LEAK #2) is reclaimed by a specified mechanism the finding never
mentions (discovery heartbeat eviction → `peerEvents` → engine deregister). What
remains of the finding is a real but minor enhancement (a faster, transport-sourced
disconnect event), which does not justify "high" severity.

## Point-by-point

### 1. The "missing handshake" is explicitly specified in GR-3.

`go-rules.md:61-63` (cited by the finding itself) states:

> "On peer disconnect, the per-connection reader and writer goroutines must both
> exit (close the conn → unblock the reader → cancel the writer) and be `Wait`ed."

That is exactly the "per-conn close handshake" the finding's recommended-change #1
proposes. It is not absent from the design — it is a graded rule. The finding tries
to escape this by arguing the rule is "stated precisely but the design does not
implement its two halves" and that "structure.md never names it (it says only
ctx-cancel/close)." But structure.md is a one-line-per-file layout table, not the
algorithm spec; the rules file IS part of the design. And critically,
`structure.md:93` tags `conn.go` with **`GR-3`** in its "created by" column:

> `conn.go | wrap tls.Conn; per-conn reader + writer goroutines; frame loop via
> protocol; ctx-cancel/close | WS-2 · GR-3 · GR-4 · GR-8`

So the file that must implement the handshake is explicitly bound to the rule that
defines the handshake. Reading "the design omits the handshake" out of a table cell
that links to the rule containing the handshake is a misread, not a gap.

### 2. "ctx-cancel/close" already names the unblock mechanism.

The finding repeatedly treats teardown as "root-ctx only" (LEAK #1: "nothing closes
outboundCh or the conn from the reader's exit path"). But the cell says
"ctx-cancel/**close**". The `close` is `conn.Close()` — precisely the operation
that unblocks a writer parked in `conn.Write` and lets the reader's error path
propagate. The design names both levers (ctx cancellation for `select`s, conn close
for blocked I/O), which is the standard Go idiom and matches GR-3's parenthetical.
The finding's "writer blocks FOREVER" interleaving only holds if you assume the
implementer ignores both GR-3 and the word "close" in their own file's spec — i.e.
it is an implementation-bug hypothesis, not a design defect.

### 3. LEAK #2 (per-peer state) has a specified reclamation path the finding omits.

The finding asserts the engine "never learns to drop P" because "peerEvents flows
FROM discovery, not from transport on disconnect." But discovery's design already
reclaims dead peers:

- `structure.md:105` — `registry.go`: "peer registry: add on announce; **heartbeat
  eviction timeout**."
- `structure.md:106` — `discovery.go`: "emits `peerEvents` channel."
- `structure.md:114` — `engine.go`: "consumes `fsChanges`/**`peerEvents`**/`inboundMsgs`."

So when P drops, P stops announcing, the heartbeat timer evicts P, discovery emits a
peer-gone `peerEvents`, and the engine deregisters P — dropping its routing entry and
ack/last-index state. Per-peer state is therefore **bounded by the heartbeat
timeout**, not "retained forever" / "unbounded" as the finding claims. The finding's
LEAK #2 ("per-peer maps grow unbounded") is false against the design as written.

### 4. The genuinely new recommendation is an enhancement, not a leak fix.

The one part of the finding that is not already in the design is recommendation #3:
a *transport-sourced* disconnect event, faster than waiting for the discovery
heartbeat eviction. That has real value — heartbeat eviction can lag a drop by the
timeout window, and a direct event tightens that. But "state is reclaimed a few
seconds late via heartbeat" is a latency/tidiness improvement, not a leak, and does
not support a "high (graded invariant)" severity. The graded invariant (GR-3 /
`runtime.NumGoroutine()` back to baseline) is about **goroutines**, and those are
covered by points 1-2.

## Why default-refute applies

The finding rests on representing the design as ctx-only and handshake-absent, when
its own cited lines (`go-rules.md:62-65`, `structure.md:93`) specify the handshake
and bind it to `conn.go`, and a second cited subsystem (`registry.go`/`discovery.go`/
`engine.go`) specifies the per-peer-state reclamation it claims is missing. The
residual valid kernel (prefer a transport-emitted disconnect event over slow
heartbeat eviction) is a medium/low improvement, not a high-severity leak. The
finding materially overstates both the gap and the severity.

## Vote

REFUTE (confidence: medium — the transport-event enhancement is worth tracking
separately as a low/medium item, but the finding as written misrepresents the spec).
