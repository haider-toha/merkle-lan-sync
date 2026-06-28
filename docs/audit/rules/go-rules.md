# Go rules for Merkle Sync

Go idioms that are **hard rules for this domain** — a decentralised LAN file-sync
daemon with three long-lived concurrent listeners (UDP discovery, TCP
connections, filesystem watcher), a Merkle-tree state model, and a strict
no-data-loss contract. Every rule cites a current (2025–2026) source. These bind
all `internal/` and `cmd/` code and are enforceable in review and `go vet` /
`-race`.

Module baseline: `go 1.23` (see `go.mod`). This matters: the rules below assume
Go ≥ 1.22 loop-variable semantics.

---

## GR-1 — Per-iteration loop variables are assumed; capture is still explicit at goroutine spawn

Since Go 1.22, `for` loop variables have **per-iteration scope**: "Go 1.22
redeclares the variable for each iteration, meaning closures (like goroutines)
capture the per-iteration variable" — for range loops "the effect is as if each
loop body starts with `k := k` and `v := v`"
([Go blog, *Fixing For Loops in Go 1.22*](https://go.dev/blog/loopvar-preview); [Go Wiki: LoopvarExperiment](https://go.dev/wiki/LoopvarExperiment), both accessed 2026-06-28). The new semantics
apply only because our `go.mod` declares `go 1.23`.

- **Rule:** rely on per-iteration scope (our module qualifies), but when a loop
  spawns a goroutine per peer/connection/event, still pass the value as an
  explicit argument to the goroutine func where it makes ownership obvious —
  belt-and-suspenders and self-documenting for reviewers who may not track the
  module version.
- **Why:** pre-1.22 this was the #1 concurrency footgun ("all created goroutines
  would end up printing ... the final value" — same source). We spawn a goroutine
  per discovered peer and per accepted connection; a regression here corrupts
  peer routing.

---

## GR-2 — One `context.Context` tree; cancellation flows down; no `context.Background()` deep in the stack

- **Rule:** create one root context in `cmd/msync` via
  `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` and
  thread it into every subsystem (discovery, transport, watcher, reconcile).
  Every long-lived goroutine `select`s on `ctx.Done()`. Never call
  `context.Background()` below `main`.
- **Why:** "signal.NotifyContext returns a context that is canceled
  automatically when an interrupt signal ... is received, allowing the
  application to perform cleanup and exit gracefully"; and "If your goroutine
  blocks on a channel or other operation, it will not notice cancellation unless
  you also select on ctx.Done()" — and "Do not create a new context.Background()
  deep in your call stack, as this breaks the cancellation chain"
  ([oneuptime, *Gracefully Cancel Goroutines with Context*, 2026-01-25](https://oneuptime.com/blog/post/2026-01-25-gracefully-cancel-goroutines-context-go/view), accessed 2026-06-28).
- **Corollary (GR-2a):** context is a **function parameter, not a struct field** —
  "storing in struct fields makes the lifetime unclear" (same source). The one
  pragmatic exception teams allow is a context stored on a short-lived
  per-connection handler value; document it if you do.

---

## GR-3 — Every spawned goroutine has an owner that waits for it; no leaks on peer disconnect

- **Rule:** pair the context with a `sync.WaitGroup` (or `errgroup.Group`). The
  function that spawns goroutines owns their lifetime and **blocks until they
  return** before it returns. On peer disconnect, the per-connection reader and
  writer goroutines must both exit (close the conn → unblock the reader → cancel
  the writer) and be `Wait`ed.
- **Why:** "Use context to propagate cancellation signals ... Use sync.WaitGroup
  to ensure that all goroutines complete their work before the main function
  exits" ([dev.to, *Preventing Goroutine Leaks with Context, Timeout & Cancellation*](https://dev.to/serifcolakel/go-concurrency-mastery-preventing-goroutine-leaks-with-context-timeout-cancellation-best-1lg0), accessed 2026-06-28). The concurrency-critic
  is explicitly tasked with finding "goroutine leaks on peer disconnect"
  (plan/agent_roster.md), so this is a graded invariant, not a nicety.
- **How tested:** a leak test that connects/disconnects N peers and asserts
  `runtime.NumGoroutine()` returns to baseline after `Wait`; `-race` on all
  integration runs.

---

## GR-4 — The three listeners are independent goroutines coordinated only by context + channels

The daemon runs three concurrent listeners with different lifecycles:

| listener | shape | shutdown trigger |
|---|---|---|
| UDP discovery | one goroutine reading multicast + one ticker goroutine announcing | `ctx.Done()` → `conn.SetReadDeadline`/close |
| TCP accept | one `Accept` loop goroutine; one reader + one writer goroutine **per** connection | `ctx.Done()` → close listener; per-conn ctx cancel |
| fs watcher | one goroutine draining `watcher.Events`/`watcher.Errors`; a debounce goroutine | `ctx.Done()` → `watcher.Close()` |

- **Rule:** listeners do not call into each other directly. They communicate by
  sending values on channels to the reconcile core (e.g. `peerEvents`,
  `fsChanges`, `inboundMsgs`). The reconcile core is the single consumer that
  mutates tree state. This is the classic "share memory by communicating" shape.
- **Rule:** blocking network reads must be made cancellable — set a read deadline
  driven off `ctx`, or close the connection/socket on cancel so the blocked
  `Read` returns an error. A goroutine parked in `conn.Read` does **not** see
  `ctx.Done()` by itself (GR-2 caveat).
- **Why / pattern source:** the select-on-`Done()` + channel fan-in pattern and
  the "context flows down, WaitGroup gathers up" structure are the consensus
  graceful-shutdown pattern ([goperf.dev, *Efficient Context Management*](https://goperf.dev/01-common-patterns/context/); [oneuptime, *How Context Cancellation Propagates*, 2026-01-23](https://oneuptime.com/blog/post/2026-01-23-go-context-cancellation/view), both accessed 2026-06-28).

---

## GR-5 — One `sync.RWMutex` guards the tree, separating watcher *writes* from sync *reads*

The Merkle tree / last-known state is read constantly (every diff against a peer)
and written occasionally (when the watcher reports a settled local change or a
received file rebuilds the tree).

- **Rule:** guard the in-memory tree with a single `sync.RWMutex`. **Writers**
  (scanner applying a settled change; reconcile applying a received file then
  rebuilding) take `Lock()`. **Readers** (diff/serve-index against a peer) take
  `RLock()`. Hold the lock for the **shortest possible critical section** — copy
  out the `FileInfo`/subtree you need, release, then do I/O. Never perform
  network or disk I/O while holding the lock.
- **Why:** `RWMutex` lets many concurrent peer diffs proceed while still
  serialising the rare writer; the stack is explicitly specified as "sync
  (RWMutex separating watcher-writes from sync-reads)" (plan/README.md, Stack).
  Doing I/O under the lock is the path to the watcher↔sync-write deadlock the
  concurrency-critic hunts for ("can the watcher and a sync write deadlock").
- **Rule (lock ordering):** if more than one lock ever exists, define and
  document a total lock order; acquire in that order everywhere. Prefer a single
  lock + immutable snapshots to avoid the problem entirely.
- **How tested:** `go test -race` on a scenario that diffs against a peer while
  the watcher fires writes; a deadlock detector test with a bounded timeout.

---

## GR-6 — Wrap errors with `%w`; sentinel + `errors.Is`/`errors.As` at decision points

- **Rule:** add context as errors cross layers with
  `fmt.Errorf("reconcile %s: %w", path, err)`. Define sentinel errors
  (`var ErrFrameTooLarge = errors.New("frame exceeds max length")`) for
  conditions callers branch on, and inspect with `errors.Is` / `errors.As` — not
  string matching.
- **Why:** "Wrapping an error with %w makes it available to errors.Is and
  errors.As"; use `fmt.Errorf` when "errors propagate through multiple layers";
  reserve `errors.New` for sentinels "checked explicitly within your application
  logic" ([Go blog, *Working with Errors in Go 1.13*](https://go.dev/blog/go1.13-errors); [Datadog static-analysis Go best practices](https://docs.datadoghq.com/security/code_security/static_analysis/static_analysis_rules/go-best-practices/errors-new-errorf/), both accessed 2026-06-28).
- **Caveat (API surface):** "Do not wrap an error when doing so would expose
  implementation details. Wrapping an error makes that error part of your API"
  (Go 1.13 errors blog). At the transport trust boundary, do not leak internal
  paths/stack detail to a peer; wrap richly for our logs, return a coarse error
  on the wire.
- **How tested:** unit tests assert `errors.Is(got, ErrFrameTooLarge)` etc.

---

## GR-7 — `encoding/binary` for the wire frame, never `gob`, never `gob` from the network

- **Rule:** the on-wire frame (`internal/protocol/framing.go`) uses
  `encoding/binary.BigEndian` + `io.ReadFull`. Do **not** decode `encoding/gob`
  (or any self-describing format outside Go's security policy) from a network
  peer.
- **Why:** "The gob package is not designed to be hardened against adversarial
  inputs, and is outside the scope of Go's security policy ... care should be
  taken when decoding gob data from untrusted sources, which may consume
  significant resources" ([encoding/gob docs](https://pkg.go.dev/encoding/gob), accessed 2026-06-28).
  `encoding/binary` "favors simplicity over efficiency" ([encoding/binary docs](https://pkg.go.dev/encoding/binary),
  accessed 2026-06-28) — simplicity is the right default at the trust boundary.
  `gob` is acceptable only for **local** on-disk state we wrote ourselves (e.g. a
  cached tree snapshot), never for bytes a peer sent. Full rationale and the
  `[4-byte len][1-byte type][payload]` format: `docs/audit/decisions/phase0/framing-format.md`.

---

## GR-8 — Framed reads use `io.ReadFull` and a max-length guard; never a bare `Read`

- **Rule:** to read a frame: `io.ReadFull(r, lenbuf[:4])`, decode `uint32`
  big-endian, **reject `L==0 || L > MaxFrameLen` before allocating**, then
  `io.ReadFull(r, body[:L])`. A single `conn.Read` may return a partial message;
  treating its return as a whole message is a stream-desync bug.
- **Why:** length-prefix readers "can be given a huge message size ... [causing]
  an OutOfMemoryException, so one must include a maximum message size 'sanity
  check' in the socket reading code" ([Stephen Cleary, *Message Framing*](https://blog.stephencleary.com/2009/04/message-framing.html), accessed 2026-06-28). See SR-12.
- **How tested:** feed a frame through `iotest.OneByteReader` (forces partial
  reads) and assert correct reassembly; feed an oversized length and assert
  `ErrFrameTooLarge` with no large allocation.

---

## GR-9 — fsnotify is advisory: not recursive, drops watches, coalesces/drops events

fsnotify's own documentation pins these realities (all quotes
[pkg.go.dev/github.com/fsnotify/fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify), accessed 2026-06-28):

- **Not recursive:** "Subdirectories are not watched (i.e. it's non-recursive)."
  → **Rule:** walk the tree and `Add` a watch per directory; on a `Create` of a
  directory, `Add` a watch for it; on remove/rename of a directory, `Remove` its
  watch to avoid leaks.
- **Watches dropped on delete/rename:** "A watch will be automatically removed if
  the watched path is deleted or renamed. The exception is the Windows backend,
  which doesn't remove the watcher on renames." → **Rule:** treat the watch set
  as mutable; reconcile it against the tree on every settled change and on
  periodic rescan. Do **not** assume a watch you added still exists.
- **Watch directories, not files:** "Watching individual files ... is generally
  not recommended as many programs ... update files atomically: it will write to
  a temporary file which is then moved to a destination ... The watcher on the
  original file is now lost." → **Rule:** watch parent directories and filter by
  `Event.Name`. (This is also why *we* write atomically — SR-1.)
- **Events coalesce / multiply:** "A single 'write action' ... may show up as one
  or multiple writes ... you may want to wait until you've stopped receiving
  them." → **Rule:** debounce events per path (~150 ms quiet window) before
  acting; see GR-10.
- **Events can be silently dropped under load:** `ErrEventOverflow` — on Windows
  "The buffer size is too small; WithBufferSize() can be used to increase it";
  the default `ReadDirectoryChangesW()` buffer is 64K. On Linux, inotify
  `IN_Q_OVERFLOW`. → **Rule:** set `WithBufferSize` larger than default on
  Windows, **monitor the `Errors` channel**, and on any overflow **trigger a full
  rescan** — events are hints, the rescan is the source of truth (SR-11).
- **Network/virtual FS unsupported:** "Notifications on network filesystems (NFS,
  SMB, FUSE, etc.) ... generally don't work." → **Rule:** if the synced folder is
  on such a mount, fall back to periodic full rescan only; detect and warn.

---

## GR-10 — Debounce filesystem events behind a quiet-window timer

- **Rule:** funnel raw fsnotify events into a per-path debounce: restart a
  ~150 ms timer on each event for a path; only when the timer fires (the path has
  been quiet) do you hash/diff it. Coalesce bursts; never act on the first of N
  rapid writes.
- **Why:** editors and large writes emit "hundreds of Write events" for one
  logical save (GR-9 citation); acting per-event causes partial-file hashing and
  redundant work. The roster pins "debounce ~150ms" (plan/agent_roster.md,
  crossplatform-researcher).
- **How tested:** inject a burst of synthetic events for one path within the
  window and assert exactly one hash/diff fires.

---

## GR-11 — Stdlib-first; minimal dependencies; `crypto/tls` from the standard library

- **Rule:** prefer the standard library: `net` (TCP/UDP), `crypto/sha256`,
  `crypto/tls`, `crypto/x509`, `encoding/binary`, `sync`, `context`,
  `os`/`io`/`path`. The one expected third-party dependency is
  `github.com/fsnotify/fsnotify` (cross-platform watching). Any further dependency
  needs a logged decision.
- **Why:** the project Stack is explicitly stdlib-centric (plan/README.md), the
  trust boundary should minimise third-party parsers (GR-7), and fewer deps means
  the `GOOS=windows` cross-compile and the CI matrix stay simple.

---

## GR-12 — Use `path` (forward-slash) for canonical keys; `filepath` only at the OS boundary

- **Rule:** all tree keys, wire paths, and map keys are forward-slash relative
  paths manipulated with the `path` package. Convert to/from OS-native form with
  `filepath.FromSlash` / `filepath.ToSlash` **only** at the moment you touch the
  real filesystem. Never store an OS-specific separator in the tree or on the
  wire.
- **Why:** the canonical-path invariant is a project hard rule ("Canonical paths
  are forward-slash relative; never store OS-specific separators"); on Windows
  `filepath` uses `\`, which would poison cross-OS hashes. Detail and the
  Unicode/case normalisation that rides alongside: `docs/audit/rules/crossplatform-rules.md`.

---

## GR-13 — Concurrency hygiene: `-race` is mandatory; channels have owners; no naked `time.Sleep` for synchronisation

- **Rule:** `go test ./... -race -count=1` is the gate for every workstream
  (already wired in `.github/workflows/ci.yml`). A channel has exactly one closer
  (the sender side); never close from the receiver. Do not use `time.Sleep` to
  "wait for" a goroutine — wait on a channel or `WaitGroup`.
- **Why:** the data this program guards is the user's files; a race that
  double-applies or drops a change is data loss, not a flake. The race detector
  is the cheapest defence and is already in CI.

---

## Quick-reference checklist (for implementers and the concurrency-critic)

- [ ] Root `ctx` from `signal.NotifyContext`; threaded everywhere; no deep `Background()`.
- [ ] Every goroutine: `select`s on `ctx.Done()` **and** has a `WaitGroup` owner.
- [ ] Blocking network reads cancelled via deadline/close, not hope.
- [ ] Tree guarded by one `RWMutex`; **zero I/O under the lock**; documented lock order.
- [ ] Errors wrapped with `%w`; sentinels + `errors.Is/As`; nothing internal leaked to peers.
- [ ] Frame I/O: `io.ReadFull` + big-endian + max-length guard; no `gob` from the network.
- [ ] fsnotify treated as advisory: per-dir watches, watch-set reconciled, `Errors`→rescan, debounce ~150 ms.
- [ ] Canonical forward-slash paths via `path`; `filepath` only at the FS boundary.
- [ ] `-race` green; goroutine count returns to baseline after peer churn.
