# Decision (WS-4): watcher-as-hint + debounce, rescan-as-truth, startup reconcile

- Area: ws4 / internal/reconcile (watcher.go, scanloop.go, engine.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-4 implementer
- Plan items: WS-4 #10 (watcher drops recovered by rescan; debounce coalesces a burst);
  startup side of MK-6 (deletion-across-restart).
- Reads-first: SR-11 (watcher events are hints; periodic full rescan is the source of
  truth; on overflow → rescan), GR-9 (fsnotify is advisory: non-recursive, drops
  watches on rename/delete, coalesces/drops events, `ErrEventOverflow`,
  `WithBufferSize` on Windows), GR-10 (debounce ~150 ms quiet window),
  `decisions/crossplatform/watcher-trust-and-debounce.md`, MK-6 +
  `merkle.{Scan,RescanCandidate,SynthesizeDeletions,LoadSnapshot,SaveSnapshot}`,
  CDD-7.1 (distinguish locally-authored vs remotely-applied deletions on restart).

## Context

Local change detection must be correct under a watcher that silently drops events
(Windows `ReadDirectoryChangesW` 64 KiB overflow; rename-watch loss; non-recursive),
must coalesce an editor's burst of writes into one hash/diff, and must recover a
deletion that happened while the daemon was down (MK-6). `internal/merkle` already
provides `Scan` (always hashes — the truth), `RescanCandidate` (size/mtime prefilter,
a HINT only), `SynthesizeDeletions`, and snapshot persist/load.

## Options — change detection (scored: correctness / concurrency / testability / cross-platform)

### Option D1 — watcher-only (act on each fsnotify event)
- correctness **1** — drops under load = permanently missed changes; partial-file
  hashing mid-write; the GR-9/SR-11 anti-pattern. Rejected.

### Option D2 — rescan-only (periodic full `Scan`, no watcher)
- correctness **5** — always converges (the rescan is the truth). latency **2** — a
  change is seen only at the next rescan tick. Kept as the **safety net + the sole
  mechanism if no watcher is available** (network FS, GR-9).

### Option D3 — watcher as a debounced *hint* that schedules a targeted re-hash, with periodic full rescan as the source of truth + overflow→rescan (CHOSEN)
Raw fsnotify events feed a **per-path ~150 ms quiet-window debounce**; when a path
goes quiet, the engine re-hashes *that path* (cheap, targeted) and runs the
local-authorship check. A **periodic full `Scan`** (the truth) runs on a ticker and
**on any watcher `ErrEventOverflow`/error**; its delta vs the recorded `files` set
catches everything the watcher missed. The watcher is wrapped behind a small interface
so tests inject synthetic events and the engine never hard-depends on a live fsnotify.
- correctness **5** — rescan guarantees eventual detection regardless of watcher
  fidelity (SR-11); debounce avoids partial-file hashing (GR-10).
- concurrency-safety **5** — the debounce map is owned by the scanloop goroutine (a
  GR-4 actor; not the RWMutex-guarded core); it emits settled-path hints on a channel.
- testability **5** — `TestDebounce_CoalescesBurst` (N synthetic events for one path
  within the window ⇒ exactly one hint) + `TestRescan_RecoversDroppedEvent` (no watcher
  event delivered; the periodic rescan still detects + converges).
- cross-platform **5** — `WithBufferSize` raised on Windows; watch-set reconciled on
  every settled change + rescan (the Windows-no-remove-on-rename + non-recursive
  realities); kqueue on macOS (not FSEvents) — mechanism named correctly, rule
  unchanged.

## Options — startup reconcile (MK-6)

### Option U1 — trust the fresh scan only (no snapshot)
- correctness **2** — a deletion while down is indistinguishable from "never existed"
  ⇒ resurrection. Rejected.

### Option U2 — load snapshot, full scan, `SynthesizeDeletions`, reseed if snapshot missing (CHOSEN)
On start: `LoadSnapshot` → `Scan` → `SynthesizeDeletions(snapshot, scan, self)` (a
live path absent on disk ⇒ a fresh tombstone; an existing tombstone carried forward
**unchanged** so a restart never re-stamps a peer-authored tombstone as local —
CDD-7.1; a reappeared path ⇒ a legitimate create). A missing/corrupt snapshot ⇒
create-only + **cold-start reseed** flag (Merge peer VVs on first INDEX before
asserting authorship — vv-counter-seeding). Persist the snapshot on clean shutdown and
periodically.
- correctness **5** — recovers deletion-across-restart (R-5); never mass-deletes on
  first run (the mass-delete antipattern); distinguishes local vs remote tombstones.
- concurrency-safety **5**, testability **5** (the merkle layer's snapshot tests
  already cover the diff; the engine wires them + a restart integration path),
  cross-platform **5**.

## Decision

**D3 + U2.** The watcher is a **debounced hint** (per-path ~150 ms quiet window) behind
a small injectable interface; a **periodic full `Scan` is the source of truth**, also
triggered on watcher overflow/error; the watch-set is reconciled on every settled
change and rescan. Local-authorship is confirmed by `content_hash` (size/mtime only
prefilter a re-hash — SR-4/AL-11), feeding the SR-6 bump+broadcast and the SR-8 echo
filter. Startup runs `LoadSnapshot`→`Scan`→`SynthesizeDeletions`; a missing snapshot is
create-only + cold-start reseed; the snapshot is persisted on shutdown + periodically.

## Rationale

- Rescan-as-truth is the only design robust to the documented per-OS watcher drops; the
  debounced watcher is a latency optimisation layered on top, never the correctness
  floor (SR-11/GR-9).
- Confirming authorship by content hash (not by "an event fired") is what lets the same
  mechanism both detect a genuine edit and filter our own apply echo (PR-6 §5).
- The snapshot reuse closes R-5 without reintroducing a multi-device index DB (N4).

## Consequences

- Drives `watcher.go` (fsnotify wrapper: per-dir watches, `WithBufferSize`, watch-set
  reconcile, `Errors`→rescan, behind a `fsWatcher` interface) and `scanloop.go`
  (debounce, targeted re-hash, periodic `Scan`, snapshot persistence).
- Tests inject synthetic events through the interface, so the suite never depends on a
  live OS watcher (the real `ReadDirectoryChangesW` overflow/rename-cleanup under load
  → Phase 6). `github.com/fsnotify/fsnotify` is the one third-party dep (GR-11,
  pre-approved); added to `go.mod`.
- Debounce granularity (~150 ms) and rescan period are tunable constants; a too-short
  debounce only costs extra (harmless) hashes, never correctness.
- Cross-refs: SR-4/6/8/11, GR-9/10/11, MK-6, CDD-7.1, watcher-trust-and-debounce.
