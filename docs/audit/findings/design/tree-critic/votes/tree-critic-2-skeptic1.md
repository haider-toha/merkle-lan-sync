# Skeptic #1 vote — tree-critic-2 (snapshot not crash-safe → VV rollback)

- Finding: `docs/audit/findings/design/tree-critic/tree-critic-2-snapshot-not-crash-safe-vv-rollback.md`
- Role: skeptic #1 of 3 — task is to REFUTE.
- Vote: **REFUTED = true** (confidence: medium)

## Summary of my position

The mechanism the finding describes is internally coherent, and one narrow
observation is technically correct (guard 2 is gated on "no snapshot," guard 3 on
"equal VV" — neither catches a *DominatedBy* produced by a stale snapshot). But the
finding overstates this into an "open, unmitigated, high-severity, every-crash silent
data-loss hole that requires the recommended fix," and on each of those load-bearing
words the status quo already has an answer. The finding's genuine net contribution
over what the decision record already says is a worked example, not a newly-uncovered
hole. That is not enough to carry a *high* severity, status-quo-beating verdict.

## Why the alarming framing does not hold up

### 1. The "hole" is already covered by a pre-authorized, documented fallback.

`vv-counter-seeding.md:76-89, 101-102, 87-89` keeps **Option B (hybrid
`max(prev+1, unixNow)`)** as the *documented fallback* "iff the cold-start reseed
proves too costly to make correct in WS-4." The hybrid floor closes *exactly* this
hole: a post-crash bump is floored to wall-clock (~1.75e9), which is many orders of
magnitude above any logical counter ever minted, so `prev+1` can never collide with a
spent value and the rollback simply cannot happen. The finding even concedes this —
recommendation #1 admits it is "the monotonic-counter half of what the hybrid
`max(prev+1, now)` clock bought for free." So the design is not defenceless against
the described failure; it carries a designed escape hatch that fully neutralises it.
A finding that says "this reopens the hole" while the same decision doc already
records a switch that closes the hole is describing a *contingency the design
anticipated*, not an open gap. Severity "high / open" is therefore overstated.

### 2. This is a restatement of an already-flagged dependency, not a new discovery.

`vv-counter-seeding.md:126-129` already states the **Hard dependency on OQ-5/R-5**:
"If that snapshot is not delivered, the rollback guarantee weakens to
'reseed-on-connect only,' and the §'fallback' hybrid floor must be reconsidered —
flagged for the Phase 3 protocol-critic and concurrency-critic." The finding quotes
this verbatim and calls its own contribution "sharpening." The decision already
routes the exact concern (snapshot durability vs the rollback guarantee) to Phase 3.
The marginal value here — that "behind" is as dangerous as "missing" — is a one-line
trigger refinement to guard 2 (recommendation #2), which is implementation-detail
nailing-down owed to WS-4, not a design defect.

### 3. Exposure is overstated: not "every crash."

The finding claims exposure is "every crash … the window is as large as the snapshot
interval × edit rate." But a *crash* only produces a stale snapshot; **silent data
loss additionally requires** a post-restart NEW edit, to the *same* path, producing
*different* content, committed *before* A reconnects and learns the peer's higher VV.
On a 2-device LAN tool the INDEX exchange on reconnect happens within seconds of
restart, and the watcher trust model is "events as hints + periodic full rescan as
source of truth" (`plan/README.md` key decisions). The realistic restart sequence is:
load snapshot → full rescan → INDEX exchange with B (which carries `f={A:7}`). For the
common case where the post-crash disk already holds the broadcast content C7, the
rescan-vs-snapshot diff sees identical *content hashes* with the peer's copy, so even a
redundant local bump converges with no loss. The loss case is a genuine race
(edit-same-file-before-reconnect), not the blanket "every crash" the Impact section
asserts. That is at most medium exposure, not the high the finding assigns.

### 4. The recommended change does not clearly beat the status quo.

Recommendation #1 puts a **synchronous fsync'd counter-log write on the outbound
`INDEX_UPDATE` hot path** ("durable no later than the broadcast"), plus a staleness
epoch (#2) and per-tombstone origin-VV tracking (#3). That is real new persistence
machinery and a per-broadcast fsync cost — for protection the documented Option B
fallback already provides for *free* (pure integer math, no extra durability). When a
finding's preferred fix is strictly more expensive than an already-recorded
alternative that closes the same failure, it has not shown it "beats the status quo";
at best it has shown one of two paths the decision already enumerated. The honest
resolution is "if WS-4 finds the stale-snapshot trigger awkward, take the documented
hybrid fallback" — which is what `vv-counter-seeding.md` already says.

## What I concede (so the panel can weigh it)

The narrow technical claim is correct: guard 3 catches only *equal-VV*-differing
content, and a stale snapshot yields a clean *DominatedBy*, which guard 3 does not
trip; guard 2's "no snapshot" trigger likewise misses "snapshot present but behind."
If WS-4 were to ship Option A *literally as written*, with the snapshot trusted as
ground truth and no peer-VV merge before post-restart authorship, a contrived race
could lose data. That is a real implementation note. But it is (a) anticipated by the
existing flag, (b) closed by the documented fallback, and (c) overstated in severity
and exposure. A correct implementation note dressed as an open high-severity
data-loss hole is, on balance, refutable.

## Verdict

REFUTED (medium). The core mechanism is not bogus, which is why this is medium and not
high confidence. But the finding's decision-relevant claims — *open*, *unmitigated*,
*high*, *every-crash*, *requires-this-fix* — are each undercut by the existing
record (documented hybrid fallback, already-flagged dependency, narrow race exposure,
cheaper pre-authorized alternative). It should not stand as a new high-severity design
hole; at most it is a WS-4 implementation note already covered by the existing
Phase-3 hand-off in `vv-counter-seeding.md:126-129`.
