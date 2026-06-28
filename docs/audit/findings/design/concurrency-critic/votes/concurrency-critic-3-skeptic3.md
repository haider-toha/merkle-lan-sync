---
finding: concurrency-critic-3
skeptic: skeptic-3
vote: REFUTE
confidence: high
date: 2026-06-28
---

# Skeptic-3 vote on concurrency-critic-3 — REFUTE

## Summary

The finding claims the design "does not implement" a per-connection close
handshake and therefore leaks a goroutine + per-peer state on every single-peer
disconnect. On inspection the central premise is a misreading of the audit
artifacts: the exact handshake it "recommends" is already written into the
binding design rules that `conn.go` is explicitly created under, and the
engine-deregister path it says is "missing" already exists via discovery's
heartbeat eviction. What remains is at most a low-severity documentation/latency
nit, not a "high" graded leak.

## Why the core claim does not hold

### 1. The handshake the finding "recommends" is already the spec (Recommendation #1 and #2 are restatements of GR-3)

The finding's headline is that the design has "no specified mutual-close
handshake." But GR-3 (`go-rules.md:62-63`) states it verbatim:

> "On peer disconnect, the per-connection reader and writer goroutines must both
> exit (close the conn → unblock the reader → cancel the writer) and be Wait'ed."

The finding itself quotes this line and even concedes "GR-3 states the obligation
precisely." That is the close-once handshake (Recommendation #1) and the
owner-Waits-the-pair rule (Recommendation #2) already in the design.

Critically, `structure.md:93` lists `conn.go` as **"created by WS-2 · GR-3 · GR-4
· GR-8"** — i.e. `conn.go` is explicitly bound to implement GR-3. Per the roster,
`go-rules.md` is a set of *hard rules* every implementer "reads first" and must
obey. So GR-3 is not an aspirational aside; it is the binding design constraint
for exactly this file. The handshake is part of the design.

The finding's argument reduces to "`structure.md` doesn't repeat the mechanism."
But `structure.md` is, by construction, a one-line-per-file layout table
(`README.md`: "each file one-line purpose"). Expecting it to restate the GR-3
mechanism is a category error. The terse "ctx-cancel/close" cell plus the GR-3
tag *is* the pointer to the full mechanism. There is no design omission — only a
layout table that doesn't (and shouldn't) duplicate the rules file.

### 2. LEAK #2 (engine never learns peer P is gone) is contradicted by the documented heartbeat-eviction path

The finding asserts: "There is no specified 'peer P disconnected' event ... the
engine never learns to drop P." This overlooks the discovery layer's documented
mechanism:

- `structure.md:105` — `registry.go`: "peer registry: add on announce; **heartbeat
  eviction timeout**."
- `structure.md:106` — `discovery.go`: "emits `peerEvents` channel."
- `structure.md:114` — `engine.go`: "consumes `fsChanges`/**`peerEvents`**/`inboundMsgs`."
- `discovery_test.go` (`structure.md:107`) — acceptance: "**silent peer evicted
  after heartbeat timeout**."

So when peer P drops, P stops announcing, discovery's heartbeat eviction fires,
and a peer-removed `peerEvents` reaches the engine, which deregisters P and
releases its per-peer state. The engine *does* learn P is gone — via the
discovery eviction path that the design explicitly specifies and tests. The
finding's claim that peerEvents only carries additions ("flows FROM discovery,
not from transport on disconnect") conflates "the signal isn't from transport"
with "there is no signal." Per-peer state is reclaimed; it is bounded by the
heartbeat timeout, not retained "forever / unbounded."

### 3. Recommendation #3 does not clearly beat the status quo

The only genuinely additive proposal is a transport→engine "disconnected" event.
This is a *latency* optimization over heartbeat eviction (instant vs. one
timeout window), not a fix for an actual leak — eviction already bounds the
state. A reasonable enhancement, but it does not "beat the status quo" on the
leak-prevention axis the finding rests on, and it carries its own complexity
(two independent dereg sources to reconcile). Not load-bearing for correctness.

### 4. Severity is overstated

"High (graded invariant)" assumes the design fails to specify the mechanism. It
specifies it (GR-3, bound to `conn.go`) **and** gates it with a required test
(`transport_test.go` goroutine-leak-on-disconnect, `structure.md:96`;
`go-rules.md:69-71`). If an implementer ignores GR-3, that is an implementation
bug the gating test is designed to catch — not a design-phase leak. A design
finding whose own evidence shows the design already prescribes the fix and the
test is, at most, a low-severity "cross-reference the handshake more visibly in
structure.md" nit.

## Counter-examples / checks performed

- `go-rules.md:57-71` (GR-3) prescribes the close-once handshake + owner Wait +
  the exact leak test the finding calls "missing."
- `structure.md:93` binds `conn.go` to GR-3 and GR-4; `structure.md:94` gives
  `listener.go` a per-conn-handler `WaitGroup`. The finding asserts the WaitGroup
  is "root-ctx only," but nothing in the text restricts the owner's Wait to
  global shutdown; GR-3 explicitly requires Wait on single-peer disconnect.
- `structure.md:105-107,114` establish the discovery→engine deregister path that
  refutes LEAK #2.

## Verdict

REFUTE. The finding misreads a deliberately terse layout table as a design gap,
ignores that GR-3 (binding on `conn.go`) already prescribes the precise handshake
it recommends, and ignores discovery heartbeat eviction as the specified
engine-deregister path. The one novel idea (instant transport→engine disconnect
event) is a minor latency improvement, not a leak fix. Severity is overstated;
the design already contains both the mechanism and the gating test.
