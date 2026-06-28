---
id: concurrency-critic-1
title: The RWMutex boundary is specified as "guards the tree" but the design accretes shared mutable state outside the tree with no owner or lock (discovery registry is a concurrent-map race)
severity: high
status: rejected
---

# concurrency-critic-1 — The `RWMutex` "guards the tree" boundary is incomplete; other shared mutable state has no documented owner

## Claim

GR-5 and `structure.md` define exactly **one** synchronisation primitive for the
whole engine: a single `sync.RWMutex` that "guards the tree," with `reconcile`
as "the only package that mutates tree state and is the single writer behind the
`RWMutex`." But the design — as it accreted across Phase 0/2 — now contains at
least **three** other pieces of long-lived mutable state that are read and written
by **different goroutines** and are **not** "the tree." None of them has a named
owner or lock. The most acute is the **discovery peer registry**, which a faithful
implementation will build as a `map` touched by three goroutines with no lock,
producing Go's uncatchable `fatal error: concurrent map read and map write`. Per
GR-13 a data race here is a data-availability/data-loss event, not a flake. The
boundary as written ("one RWMutex, guards the tree") is therefore necessary but
**incomplete**: it silently under-specifies the concurrency model for everything
that is not the tree.

## Evidence

Single named lock, scoped to "the tree":

- `docs/audit/rules/go-rules.md:99` — "GR-5 — One `sync.RWMutex` guards the tree."
- `docs/audit/plan/structure.md:36-38` — "`reconcile` is the only package that
  mutates tree state and is the single writer behind the `RWMutex` (GR-5)."
- `docs/audit/rules/go-rules.md:271` (quick-ref) — "Tree guarded by one `RWMutex`."

Shared mutable state that is **not** the tree and has **no** named lock/owner:

1. **Discovery peer registry — three writers/readers, no lock named.**
   `docs/audit/plan/structure.md:104-106` specifies a periodic announce *ticker
   goroutine* (`announce.go`), a multicast *receive* path (`multicast.go`), and a
   *heartbeat eviction timeout* (`registry.go`). That is: the multicast receiver
   **adds/refreshes** a peer entry, an eviction ticker **deletes** stale entries,
   and the dial path **reads** the entry to connect (`dial.go`,
   `structure.md:95`). Three goroutines over one `map[DeviceID]peer` with no
   documented mutex. GR-5 explicitly covers only the tree; GR-4
   (`go-rules.md:85-87`) says listeners "communicate by sending values on
   channels," which governs *cross-subsystem* hand-off but says nothing about the
   registry map *internal* to `discovery`.
2. **Scanloop debounce state — two goroutines, no lock named.** GR-4's listener
   table (`go-rules.md:83`) lists, for the watcher, "one goroutine draining
   `watcher.Events`/`watcher.Errors`; a debounce goroutine" — **two** goroutines.
   `scanloop.go` (`structure.md:116`) holds the per-path debounce timers / pending
   set those two goroutines share. Not the tree; no lock named.
3. **Per-peer "acknowledged / last-advertised index" state.** The tombstone-GC
   decision (`docs/audit/decisions/protocol/tombstone-retention-gc.md:36-38`) and
   the VV-seeding decision rely on tracking, per peer, the VV the peer has
   acknowledged. The GC decision asserts this is "evaluated by the single writer
   **under the `RWMutex`** (GR-5)" — which means the lock in fact guards *the tree
   **plus** per-peer ack state*, i.e. strictly **more than "the tree."** The rule
   text never says so, so an implementer reading GR-5 literally may place ack state
   outside the lock.
4. **The apply-time "expected `content_hash`" record (SR-8 guard).**
   `apply.go` must "record expected hash to break echo loop"
   (`structure.md:118`); `scanloop.go` reads it to decide "no new authorship"
   (PR-6 §3 guard 2). Writer = apply path; reader = debounce/scan path. If this is
   the tree's `FileInfo` it is covered; if it is a side map (the natural reading of
   "record expected hash") it is unguarded shared state.

Why this is a real (not theoretical) race for #1: a concurrent unsynchronised map
in Go is not a benign data race — the runtime actively aborts the process:
`-race` and even race-free builds fault with `fatal error: concurrent map read and
map write` (Go runtime map-access guard). GR-13 (`go-rules.md:254-263`) makes
`-race` the mandatory gate and declares "a race that double-applies or drops a
change is data loss, not a flake."

## Impact

- **Discovery registry (high):** a `fatal error: concurrent map read and map
  write` crashes the daemon mid-operation. It will surface exactly under the
  conditions the product targets — a second peer announcing while the eviction
  ticker fires — so it is not a tail case. A crash loses in-flight sync progress
  and, worse, is intermittent and timing-dependent, so it can ship "green" if the
  `-race` discovery test (`discovery_test.go`, `structure.md:107`) does not
  exercise announce + evict + dial **concurrently**.
- **Boundary ambiguity (high):** because the only documented invariant is "lock
  guards the tree," each of items 2–4 is an independent opportunity for an
  implementer to touch shared state off-lock. Each is a `-race` finding that, per
  GR-13, is graded as data-loss severity. The tombstone-GC decision already
  *assumes* the lock covers more than the tree (item 3), so the rule and the
  decisions are mutually inconsistent today.

## Recommended-change

State the **complete** concurrency model, not just "one RWMutex for the tree":

1. **Rename/rescope the invariant** from "the RWMutex guards the tree" to "the
   RWMutex guards the **reconcile state**: the tree **and** per-peer ack/last-index
   state **and** the apply-time expected-hash record" — i.e. make GR-5 cover the
   whole cluster the single writer mutates, matching what
   `tombstone-retention-gc.md` already assumes. Anything read by a peer-diff
   reader must be inside this lock.
2. **Give `discovery` its own explicitly-documented guard.** Either (a) a private
   `sync.Mutex` on the registry with a stated lock order (registry lock is a *leaf*
   — never acquired while holding the tree lock, and no I/O under it), or
   preferably (b) make the registry a single-goroutine actor: announce-receive and
   the eviction ticker `select` in one goroutine that owns the map and emits
   add/evict `peerEvents` on a channel (consistent with GR-4's "share by
   communicating"). Option (b) removes the lock entirely and is the most testable.
3. **Make `scanloop` debounce single-owner.** Fold the watcher-drain and the
   debounce timer into one goroutine that owns the timer map and emits *settled*
   paths on a channel; no shared timer map across goroutines.
4. **Pin the expected-hash record into the tree's `FileInfo`** (so SR-8's guard is
   covered by the same RWMutex) rather than a side map; if a side map is required
   for the optional event-suppression window, document its lock.
5. **Add the missing concurrency tests** (these are the cheap proof): a
   `discovery` `-race` test that runs announce + eviction + dial concurrently and
   asserts no fault; a `reconcile` `-race` test that fires watcher bursts while
   `apply` writes and a peer diff `RLock`s, asserting `-race` clean and no
   double-apply. GR-13 already mandates `-race`; these tests make item-2/3/4
   regressions impossible to ship green.

This beats the status quo (a single sentence covering only the tree) by making the
ownership of *every* shared structure explicit, which is the only way the
mandatory `-race` gate (GR-13) can actually catch a regression rather than passing
because the test never raced the right two goroutines.
