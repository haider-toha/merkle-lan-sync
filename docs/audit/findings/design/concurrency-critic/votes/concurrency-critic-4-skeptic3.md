---
finding: concurrency-critic-4
skeptic: 3
vote: REFUTE
refuted: true
confidence: medium
date: 2026-06-28
---

# Skeptic #3 vote on concurrency-critic-4 — REFUTE

## Summary

The finding rests on a pedantic misreading of GR-13, ignores an alternative the
design already provides for GR-4, and contains a concrete factual error about Go
runtime semantics ("double-close panic" for connections). Its central claims —
"GR-13 is *actively unsafe*", "following it literally *causes* the panic", and
"double-close panic" on disconnect — are overstated or wrong. There is at most a
minor doc-clarification kernel here, not a medium-severity crash bug.

## Point 1 (fan-in close) — misreads the rule

GR-13 (`docs/audit/rules/go-rules.md:257-258`) says: *"A channel has exactly one
closer (the sender side); never close from the receiver."* This is the canonical
Go aphorism about **who** may close a channel — not a mandate that every channel
**must** be closed. The finding's load-bearing inference ("GR-13 says it must have
a closer", line 24) does not follow:

- In Go, channels do **not** need to be closed for correctness or GC. Closing only
  signals "no more values." Long-lived daemon fan-in channels (`inboundMsgs`,
  `peerEvents`) that live for process lifetime are correctly **never closed** — a
  routine, idiomatic outcome that GR-13 does not forbid.
- For a multi-sender channel there is, by definition, no "exactly one sender-side
  closer," so the rule's own constraint (*exactly one* closer, on the sender side)
  is **unsatisfiable** for fan-in — which leads a competent reader straight to the
  correct conclusion "don't close it," not to a panic.
- The same rules file already points implementers at the right pattern:
  `go-rules.md:93-95` names "the select-on-`Done()` + channel fan-in pattern and
  the 'context flows down, WaitGroup gathers up' structure" as the consensus
  shutdown shape. That *is* the don't-close-fan-in pattern the finding recommends.
  So the design as a whole already prescribes the safe approach; GR-13 is terse,
  not "actively unsafe."

The panic the finding describes requires an implementer to (a) ignore "exactly one
closer," (b) force a closer onto a channel that has many senders, and (c) close
from a sender mid-teardown. That is a self-inflicted violation of the rule's plain
intent, not behaviour the rule induces.

## Point 2 (cancel-by-close) — ignores the provided alternative and misstates semantics

GR-4 (`go-rules.md:89-92`) offers **two** options in one sentence: *"set a read
deadline driven off ctx, **or** close the connection/socket on cancel."* The
read-deadline option (`SetReadDeadline`) unblocks the reader **without** ever
racing the writer's `Write` or requiring a `conn.Close()` from the cancel path.
The finding cherry-picks the close option and presents the race as inherent, while
the non-racing alternative sits in the same line it cites.

The finding also misstates Go semantics. Impact (lines 73-74, 82) asserts a
"**double-close panic**" on disconnect. Closing a `net.Conn`/`tls.Conn` twice does
**not** panic — `Close()` returns an error (e.g. "use of closed network
connection"). Only closing a **channel** twice panics. The finding imports
channel-close semantics onto conn-close. Likewise, the stdlib `net.Conn`/`tls.Conn`
are documented as safe for concurrent use; `Close()` concurrent with an in-flight
`Write` returns an error rather than corrupting state or panicking. So the
"crash on disconnect" framing is not supported.

## On the recommended change

The recommendations (don't close fan-in channels; `sync.Once`-guarded owned conn
close; WaitGroup drain on shutdown; `-race` shutdown-under-load test) are all sound
Go practice — but they are already the idioms the design cites (`go-rules.md:93-95`)
and, in the conn-ownership case, overlap with concurrency-critic-3. They do not
"beat the status quo" by averting a crash the status quo causes; they restate
existing guidance with more words.

## Severity

Overstated. Labelled medium on the basis of a daemon crash on shutdown/disconnect,
but the crash only materialises if an implementer actively misreads the rules
against their plain intent and ignores the read-deadline option. The genuine
residual is a one-line clarification ("fan-in channels are never closed; the
exactly-one-closer rule applies to single-sender channels"), which is a cosmetic
doc nicety, not a medium correctness defect.

## Verdict

REFUTE — refuted=true, confidence medium. The evidence does not support the
"actively unsafe / causes a crash" claim; it relies on a pedantic over-reading of
GR-13, ignores GR-4's read-deadline alternative, and contains a factual error
about conn double-close. A small doc tweak may be warranted, but the finding as
filed is overstated.
