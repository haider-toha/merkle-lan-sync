---
finding: concurrency-critic-4
skeptic: 1
vote: REFUTE
verdict: refuted
confidence: medium
date: 2026-06-28
---

# Skeptic #1 vote on concurrency-critic-4 — REFUTE

## Summary

The finding claims GR-13 ("A channel has exactly one closer (the sender side);
never close from the receiver") is "actively unsafe" for the fan-in channels GR-4
mandates, and that GR-4's "close conn on cancel" races the writer / risks a
double-close panic. Both halves rest on misreadings of the cited text and on
incorrect Go semantics. The core premise is manufactured, the second claim is
factually wrong about `net.Conn`, and the severity is overstated. Refuted.

## Point 1 — GR-13 does NOT mandate that fan-in channels be closed; "never close" is fully GR-13-compliant

The finding's whole claim #1 hinges on this sentence: *"the channel is never closed
(GR-13 says it must have a closer)."* That is a misreading. GR-13
(`docs/audit/rules/go-rules.md:256-258`) reads: "A channel has exactly one closer
(the sender side); never close from the receiver." This is an **ownership
invariant constraining WHO may close** — it does not impose an obligation that
every channel be closed. Nowhere does GR-13 say a channel "must have a closer."

The canonical Go idiom is exactly this: you close a channel only when the receiver
must learn there are no more values (e.g. to terminate a `range`). A long-lived
daemon fan-in channel (`inboundMsgs`, `peerEvents`) is simply **never closed**;
shutdown is signalled by `ctx` + `WaitGroup`. That pattern:

- does NOT have the receiver close it (GR-13 satisfied),
- does NOT close it from a non-owning sender (GR-13 satisfied),
- closes it from nobody (no GR-13 clause violated).

So GR-13, read as written, is fully consistent with the safe pattern the finding
itself recommends. The finding's own "recommended" remedy ("a fan-in channel is
never closed; shutdown via ctx + WaitGroup") is **already permitted** by GR-13.
There is no contradiction; the finding invents one by inserting a non-existent
"must be closed" obligation.

Indeed, "exactly one closer, the sender side" is precisely the standard Go
guidance (Dave Cheney's channel axioms / "Channels Axioms") whose *purpose* is to
**prevent** the `send on closed channel` panic the finding describes. A literal,
correct reading of GR-13 already forbids the dangerous act (a receiver close; a
close by one of N senders is not "the sender side" singular). GR-13 is terse, not
unsafe.

## Point 2 — The cancel-by-close "race" is wrong about `net.Conn` semantics

Claim #2 says closing the `tls.Conn` to unblock the reader "races the writer's
`Write` ... unless the close is `sync.Once`-guarded and ordered." This contradicts
the documented Go contract. Per the `net.Conn` interface docs
(https://pkg.go.dev/net#Conn, accessed 2026-06-28):

> "Multiple goroutines may invoke methods on a Conn simultaneously."

and for `Close`:

> "Any blocked Read or Write operations will be unblocked and return errors."

Closing a connection concurrently with a blocked or in-flight `Write` is the
**intended, supported cancellation mechanism**, not a data race. The `Write`
returns `net.ErrClosed`. The `-race` detector does **not** flag `Close` vs
`Write`/`Read` on a `net.Conn`, because the implementation synchronises them
internally. The finding asserts it "races unless sync.Once-guarded" and "would
surface intermittently under `-race`" — that is incorrect for the very case it
describes. `tls.Conn` wraps a `net.Conn` and inherits this concurrency safety.

## Point 3 — "double-close panic" on a conn is factually wrong

The Impact section warns of "a rare double-close panic" on peer disconnect.
Calling `Close()` twice on a `net.Conn`/`tls.Conn` does **not** panic — it returns
an error (`net.ErrClosed` or a wrapped close error). The finding conflates Go's
*channel* double-close semantics (which do panic) with *connection* double-close
semantics (which do not). So the headline failure mode ("double-close panic"
on the conn) does not exist as stated.

## Point 4 — the double-closer scenario is imported, not in the design

GR-4 (`go-rules.md:89-92`) and `structure.md:93` name a single cancel path
("ctx-cancel/close"). There is no second closer in the design. The finding
manufactures the double-close by importing a hypothetical "writer's own error path
also closes" from concurrency-critic-3's *recommendations* — i.e. it argues
against a design that does not yet exist. Even if a writer error path existed,
Point 3 shows a second conn close returns an error rather than crashing.

## Point 5 — severity overstated; remedy is documentation polish

- The finding concedes (Impact) that SR-1 temp-then-rename means **no data loss**
  — worst case is a crash on shutdown.
- That crash only materialises if an implementer affirmatively closes a
  multi-sender channel — an act GR-13 already discourages and no competent Go
  author writing a daemon would do (you don't close a never-terminating fan-in).
- Recommendation #1 ("amend GR-13 to spell out fan-in is never closed") is a
  helpful *clarity* edit, but it does not fix a defect — it documents what GR-13
  already permits. That is a low-severity wording nit, not a medium correctness
  bug, and certainly not "actively unsafe."

## Point 6 — evidence gap on the load-bearing claim

The remediation leans on "the documented safe pattern for fan-in" / "canonical Go
pipeline-shutdown guidance" but cites **no URL** for it (contrast GR-13 itself,
which cites goperf.dev / oneuptime). Per the autonomy contract, current-fact
claims need a citation; the finding's central "canonical pattern" assertion is
memory-only.

## Steelman (why not stronger than REFUTE)

GR-13's terseness is a genuine, if minor, clarity risk: a junior reader *could*
misread "exactly one closer" as "must be closed." A one-line clarifying amendment
is reasonable hygiene. That salvages recommendation #1 as a low-value doc tweak —
but it does not rescue the finding's framing ("actively unsafe," medium,
crash-on-every-shutdown) nor claim #2, which is wrong on Go semantics.

## Verdict

REFUTE. The core contradiction is manufactured by misreading GR-13; the
cancel-by-close "race" and "double-close panic" are factually incorrect about
`net.Conn` semantics; the double-closer is imported from another finding's
hypothetical; severity is overstated (no data loss, requires an implementer to
ignore the rule's plain meaning); and the load-bearing remedy is uncited. At most
this is a low-severity wording clarification, not the medium safety defect claimed.
