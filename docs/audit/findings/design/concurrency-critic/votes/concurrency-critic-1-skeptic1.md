# Skeptic vote — concurrency-critic-1 (skeptic #1 of 3)

**Vote: REFUTE (refuted = true). Confidence: high.**

The finding's headline claim — "the discovery registry is a concurrent-map race
touched by three goroutines with no lock" — rests on a factual misreading of the
documented design, and its central recommendations describe the design that
already exists. The legitimate residue (the global rules file could enumerate
per-package state owners) is a documentation nicety, not a high-severity
data-loss design hole.

## 1. The "three writers over one map" headline is factually wrong

The finding (item 1) names three goroutines that allegedly race over one
`map[DeviceID]peer`: the **announce ticker** (`announce.go`), the **multicast
receiver** (`multicast.go`), and the **eviction ticker** (`registry.go`).

But the announce ticker does **not** touch the registry. Its job is to broadcast
*our own* presence outbound:

- `structure.md:104` — "`announce.go` | periodic announce `{DeviceID, addr,
  port}` ticker goroutine". It sends our announce packets; it does not read or
  write the peer registry.
- `go-rules.md:81` (GR-4 listener table) confirms the split: "one goroutine
  reading multicast + one ticker goroutine **announcing**." The announcer
  announces; the receiver reads.

So the registry has at most **two** touchers (receiver adds/refreshes, eviction
deletes) — the canonical single-map-with-one-mutex pattern, not the lurid
"three-way unsynchronised map" the finding leads with. The headline severity is
inflated by miscounting the writers.

## 2. The dial path reads a channel, not the registry map

The finding asserts "the dial path **reads the entry** to connect (`dial.go`)",
making the registry shared cross-package mutable state. The documented design
says the opposite:

- `structure.md:106` — "`discovery.go` | orchestrator: ... emits `peerEvents`
  channel".
- `go-rules.md:85-88` (GR-4) — listeners "communicate by sending values on
  channels to the reconcile core (e.g. `peerEvents` ...)". They "do not call into
  each other directly."

`dial.go` (transport) consumes `peerEvents`; it does not reach into `discovery`'s
internal map. The very cross-goroutine sharing the finding fears is already
routed through a channel by GR-4. The finding even concedes GR-4 "governs
cross-subsystem hand-off" — then ignores that this is exactly the hand-off it
claims is unguarded.

## 3. The recommended fix is already the documented design (circular)

Recommendation 2(b) — the finding's own *preferred* option — is: "make the
registry a single-goroutine actor ... emits add/evict `peerEvents` on a channel
(consistent with GR-4). Option (b) removes the lock entirely and is the most
testable."

That is verbatim what `structure.md:106` already specifies (a `discovery.go`
orchestrator that owns the goroutines and emits `peerEvents` per GR-4). The
finding constructs a naive strawman implementation that contradicts the
documented design, declares it a race, then "recommends" the design that already
exists. A finding whose top recommendation is already in the artifact it critiques
does not beat the status quo.

## 4. Item 3's "mutual inconsistency" is a misread — the design already does what the finding asks

The tombstone-GC decision (`tombstone-retention-gc.md:43`) states per-peer ack
state is "evaluated by the single writer **under the `RWMutex`** (GR-5)." The
finding spins this into "the rule and the decisions are mutually inconsistent."

It is the opposite: the decision **proactively placed** per-peer ack state under
the same lock as the tree, owned by the single writer (`reconcile`). That is
precisely recommendation 1 of this finding ("anything read by a peer-diff reader
must be inside this lock"). GR-5's intent is "the single writer's state is
serialised by one lock"; the tree is the principal example, and the decision
extends the same discipline to ack state without contradiction. The finding cites
its own recommendation as already-implemented and labels it a bug.

## 5. Items 2 and 4 are speculation about hypothetical implementer mistakes

Item 2 ("an implementer MIGHT touch shared state off-lock") and item 4 ("the
natural reading of 'record expected hash'" is a side map) are not design defects —
they are guesses about future Phase-5 errors. The design already carries the
guardrails:

- GR-5 (`go-rules.md:116-118`): "if more than one lock ever exists, define and
  document a total lock order ... **Prefer a single lock + immutable snapshots to
  avoid the problem entirely.**"
- GR-13 (`go-rules.md:254-263`): `go test ./... -race -count=1` is the mandatory
  gate, already wired in CI.

A scanloop debounce map or an apply-time hash record built off-lock would fail
`-race` in CI. The finding asks (rec 5) for exactly the tests that GR-13 already
mandates and that `structure.md:107` already specifies for discovery (announce +
eviction). The marginal ask ("also race dial concurrently") is incremental test
coverage, not a missing concurrency model.

## 6. structure.md is a non-binding sketch; rules are principles, not a data inventory

`structure.md:4-7` is explicit: implementers "may split a file when it grows, but
the package boundaries and dependency direction below are the contract." The
one-line file purposes are not the concurrency contract. GR-5 deliberately scopes
to the **tree** because the tree is the *cross-subsystem* shared state; ownership
of state *internal to a single package* is an implementer decision graded at
Phase 5 against GR-13/`-race`. Demanding the global rules file pre-enumerate every
future internal map is scope creep — and the one cross-subsystem channel (GR-4)
and the one cross-subsystem lock (GR-5) are both already specified.

## 7. Severity "high" is overstated (double-counted)

The finding borrows GR-13's data-loss severity ("a race that drops a change is
data loss") for a race that GR-13's mandatory `-race` gate is specifically built
to catch — and that the existing discovery test exercises. A concurrent-map
misuse aborts loudly under `-race` and cannot ship green on the documented test
path. This is, at most, a low/informational documentation-clarity item ("the
rules file could name per-package state owners"), not a high data-loss design
flaw.

## Conclusion

The concrete race example is misdescribed (announce ticker doesn't touch the
registry; dial reads a channel, not the map), the preferred fix already exists in
the design (GR-4 channel + `discovery.go` orchestrator), the claimed
rule/decision inconsistency is the design correctly applying the finding's own
recommendation, and the remaining items are speculation guarded by GR-5 and
GR-13. The kernel of a fair suggestion (spell out per-package state ownership) is
real but does not survive as a **high**-severity finding.

**refuted = true, confidence = high.**
