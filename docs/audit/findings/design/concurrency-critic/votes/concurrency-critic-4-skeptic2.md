---
finding: concurrency-critic-4
skeptic: skeptic2 (refuter)
vote: REFUTED
confidence: medium
date: 2026-06-28
---

# Vote: REFUTE concurrency-critic-4

## Verdict

**Refuted.** The finding rests on a misreading of GR-13 and a factual error about
`net.Conn` close semantics. Its central claim — that following GR-13 "literally"
*forces a panic-on-shutdown* on the fan-in channels — does not hold. There is a small
kernel of legitimate doc-clarity value buried in the recommendation, but it is far
below the "actively unsafe / recurring crash" framing the finding asserts, and the
status quo does not actually mandate the unsafe behaviour described.

## Why the finding fails

### 1. GR-13 does not require fan-in channels to be closed — so there is no contradiction

GR-13 (`docs/audit/rules/go-rules.md:257-258`) reads: *"A channel has exactly one
closer (the sender side); never close from the receiver."* This is a **constraint on
who may close**, not a mandate that every channel **must** be closed. The finding
manufactures the contradiction by asserting (line 24) that GR-13 "says it must have a
closer." It does not. In Go, channels need not be closed at all; closing only signals
"no more values," and unclosed channels are garbage-collected.

The finding's own "documented safe pattern" — *never close the fan-in channel; signal
via `ctx` + `WaitGroup`, let the receiver stop, abandon the channel* — is **fully
compatible with GR-13 as written**. Not closing the channel violates nothing in
GR-13 (it neither closes from the receiver nor adds a second closer). The claimed
"only safe choices both contradict GR-13" is false: the canonical pattern satisfies it.

Independent confirmation that GR-13 *is* the established idiom, not a faulty rule:
the Go blog's pipeline guidance states "channels must be closed exactly once, and only
by the sender side... only one place (typically a dedicated goroutine that waits on the
WaitGroup) should close the shared output channel"
([go.dev/blog/pipelines](https://go.dev/blog/pipelines), accessed 2026-06-28). GR-13
is a verbatim restatement of consensus Go practice. The finding paints the
textbook-correct rule as "actively unsafe."

### 2. The "double-close panic / crash on disconnect" claim is factually wrong for `net.Conn`

The cancel-by-close leg (point 2) claims an unguarded `conn.Close()` "races the
writer's `Write`" and that "a second close from the writer's own error path" yields a
"double-close panic." This conflates **channel** semantics (where double-close and
send-on-closed *do* panic) with **`net.Conn`/`tls.Conn`** semantics, which do not:

- `net.Conn` is documented safe for concurrent use: *"Multiple goroutines may invoke
  methods on a Conn simultaneously"* and *"Close closes the connection. Any blocked
  Read or Write operations will be unblocked and return errors"*
  ([pkg.go.dev/net#Conn](https://pkg.go.dev/net#Conn), accessed 2026-06-28).
- Closing while a `Write` is in flight therefore **returns an error, by design — it
  does not panic or corrupt**. That is precisely the mechanism GR-4 relies on.
- Calling `Close` twice **returns an error (`net.ErrClosed`), not a panic.**

So the headline "on disconnect an unguarded close can double-close / race the writer →
crash" overstates a benign, documented error return as a daemon crash. The only true
"panic" surface in the whole finding is the channel one, which §1 already neutralises.

### 3. GR-4 already offers the race-free option; close-by-cancel is not mandated

GR-4 (`docs/audit/rules/go-rules.md:89-92`) explicitly gives **two** options: *"set a
read deadline driven off `ctx`, **or** close the connection/socket on cancel."* The
read-deadline option sidesteps the close-vs-write interaction entirely. The design
does not force the contested path; `structure.md:93` lists "ctx-cancel/close" as an
implementation note for WS-2 to resolve, not a binding choice. A `sync.Once`-guarded,
owner-only close is standard implementation hygiene any competent implementer applies
(and conn.go already carries a "goroutine-leak-on-disconnect" test,
`structure.md:96`). This is a code-review checklist item, not a design-rule defect.

### 4. Severity is overstated and self-undercut

The finding rates this "medium / crash," then concedes (lines 73-76) that SR-1
temp-then-rename means no torn file — i.e. *no data loss*, only availability. Combined
with §1 (no forced channel close) and §2 (conn close returns errors, not panics), the
realistic worst case is a doc that is *less explicit than ideal*, not a recurring
panic. That does not meet the bar for a standing medium design finding.

## What is salvageable (and why it still nets to REFUTE)

The recommendation "explicitly state fan-in channels are never closed; signal via
ctx+WaitGroup" is reasonable documentation polish and would prevent a naive
implementer from inventing a receiver-side close. But (a) GR-13 + GR-4 + the
listener/`WaitGroup` shutdown table (`go-rules.md:79-95`) already imply the correct
pattern, and (b) the finding justifies the change by asserting a contradiction and a
crash that do not exist. A finding whose stated mechanism is a misreading plus a
factual error about stdlib semantics should not stand on the strength of an
incidental, optional doc tweak. Per the default for weak/overstated findings: refute.

## Recommendation to consolidator

Reject as written. If anything survives, downgrade to a one-line **nit** on GR-13:
"add a clarifying sentence that fan-in channels are simply not closed." Do not credit
the panic/crash framing, the "GR-13 is actively unsafe" claim, or the
`net.Conn` double-close-panic claim.

## Sources
- [pkg.go.dev/net#Conn](https://pkg.go.dev/net#Conn) — net.Conn concurrency + Close semantics (accessed 2026-06-28)
- [go.dev/blog/pipelines](https://go.dev/blog/pipelines) — Go pipeline/fan-in close idiom (accessed 2026-06-28)
- `docs/audit/rules/go-rules.md:79-95` (GR-4), `:256-259` (GR-13)
- `docs/audit/plan/structure.md:93`, `:114`
