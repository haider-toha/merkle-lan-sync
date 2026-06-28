# Decision: Watcher trust model — events are hints, periodic full rescan is truth, ~150 ms debounce, overflow→rescan

- Area: crossplatform / reconcile (confirms XP-5; cross-platform half of the
  watcher-trust model the README also routes through `decisions/ws1`)
- Status: **decided** (Phase 2 — crossplatform-researcher)
- Date: 2026-06-28
- Decider: crossplatform-researcher
- Confirms: `docs/audit/rules/crossplatform-rules.md` XP-5; cross-refs SR-11
  (rescan is the source of truth), GR-9 (fsnotify is advisory), GR-10 (debounce).

## Context — what every OS watcher actually does (cited)

- **Windows `ReadDirectoryChangesW` silently drops on overflow.** Verbatim: "If
  the buffer overflows, ReadDirectoryChangesW will still return **true**, but the
  entire contents of the buffer are discarded and the *lpBytesReturned* parameter
  will be zero, which indicates that your buffer was too small to hold all of the
  changes that occurred." And the documented recovery: "If the number of bytes
  transferred is zero … you should compute the changes by enumerating the directory
  or subtree", plus "ReadDirectoryChangesW fails with **ERROR_NOTIFY_ENUM_DIR**
  when the system was unable to record all the changes … In this case, you should
  compute the changes by enumerating the directory or subtree."
  ([Microsoft, *ReadDirectoryChangesW*](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-readdirectorychangesw),
  ms.date 2018-12-05, updated 2025-07-01, accessed 2026-06-28.) Buffer nuance: it
  "fails with ERROR_INVALID_PARAMETER when the buffer length is greater than 64 KB
  and the application is monitoring a directory over the network."
- **fsnotify surfaces all of this and adds its own caveats** (v1.10.1, published
  2026-05-04;
  [pkg.go.dev/github.com/fsnotify/fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify),
  accessed 2026-06-28):
  - non-recursive: "Subdirectories are not watched (i.e. it's non-recursive)."
  - watch loss: "A watch will be automatically removed if the watched path is
    deleted or renamed. The exception is the Windows backend, which doesn't remove
    the watcher on renames."
  - overflow: `ErrEventOverflow`; windows "The buffer size is too small;
    WithBufferSize() can be used to increase it"; "The default
    ReadDirectoryChangesW() buffer size is 64K, which is the largest value that is
    guaranteed to work with SMB filesystems." inotify uses `IN_Q_OVERFLOW`.
  - coalescing: "A single 'write action' … may show up as one or multiple
    writes … you may want to wait until you've stopped receiving them."
  - network FS: "Notifications on network filesystems (NFS, SMB, FUSE, etc.) …
    generally don't work."
- **macOS reality is twofold and worth stating precisely:**
  1. The *native* FSEvents API coalesces and is explicitly advisory: "events may
     be coalesced into a single event … you will receive an event with the
     `kFSEventStreamEventFlagMustScanSubDirs` flag set", and "you should treat the
     events list as advisory rather than a definitive list of all changes … backup
     software should still periodically perform a full sweep"
     ([Apple, *Using the File System Events API*](https://developer.apple.com/library/archive/documentation/Darwin/Conceptual/FSEvents_ProgGuide/UsingtheFSEventsFramework/UsingtheFSEventsFramework.html),
     accessed 2026-06-28).
  2. **But `fsnotify` does NOT use FSEvents on macOS — it uses `kqueue`**
     ("kqueue — BSD, macOS — Supported"), and "kqueue requires opening a file
     descriptor for every file that's being watched … You will run in to your
     system's 'max open files' limit faster" (fsnotify docs, accessed 2026-06-28).
     This corrects the preliminary XP-5 framing, which spoke of "FSEvents
     coalescing" as if it were our backend.

## Options — trust model (scored 1–5)

### Option A — watcher-only (trust the event stream)
- Correctness: **1** — every backend silently drops under load (Windows buffer
  discard; inotify/kqueue overflow), so changes are missed → divergence / data not
  synced. Rejected.

### Option B — rescan-only (poll; ignore the watcher)
- Correctness: **4** — misses nothing eventually. Latency/cost: **2** — slow
  reaction, repeated full hashing. Testability: 5.

### Option C — hybrid: events = hints (fast path) + ~150 ms debounce + periodic full rescan = truth + immediate rescan on overflow/error (PROPOSED)
- Correctness: **5** — the rescan is the safety net the OS docs *themselves*
  prescribe ("compute the changes by enumerating", "perform a full sweep"); events
  only make the common case fast.
- Concurrency-safety: **5** — debounce + rescan both feed the single reconcile
  writer behind the one `RWMutex` (GR-5); the watcher goroutine never mutates the
  tree directly (GR-4).
- Testability: **5** — inject a synthetic overflow / drop an event and assert the
  rescan still converges (SR-11 test); inject an event burst and assert one
  hash/diff (GR-10 test).
- Cross-platform: **5** — same model on all three OSes; only the buffer knob and
  backend differ.

## Options — macOS backend (sub-decision, scored 1–5)

### Option M1 — `fsnotify` everywhere (kqueue on macOS) (PROPOSED for v1)
- Stdlib-aligned (GR-9/GR-11 already pin fsnotify), one dependency, one code path.
- Cost: kqueue needs an FD per watched **directory** (we watch dirs, not files —
  GR-9), so it scales with directory count, not file count; very large trees can
  approach `ulimit -n`. Mitigated by a max-watch-count guard → fall back to
  rescan-only when exceeded.

### Option M2 — `fsnotify` on Windows/Linux + `github.com/fsnotify/fsevents` on macOS
- FSEvents is recursive and has no FD-per-dir cost, but adds a cgo dependency and
  requires handling `MustScanSubDirs` coalescing explicitly. Heavier; defer.

### Option M3 — periodic rescan only on macOS (no watcher)
- Simplest, no FD ceiling, but higher latency for the interactive path.

## Decision

- **Trust model: Option C (hybrid).** Watcher events are **hints** that trigger a
  fast, debounced check; the **periodic full rescan + tree rebuild is the source of
  truth** (SR-11). On **any** `Errors`/overflow (`ErrEventOverflow`,
  `ERROR_NOTIFY_ENUM_DIR`, kqueue/inotify overflow) → trigger an **immediate full
  rescan** and reconcile the watch set against the tree (re-`Add` for the
  Windows-no-remove-on-rename case; `Add` new subdirs since fsnotify is
  non-recursive; `Remove` gone subdirs).
- **Debounce window: ~150 ms** per-path quiet timer (GR-10, roster-pinned).
  Justified by the coalescing docs ("wait until you've stopped receiving them"):
  150 ms is long enough to collapse an editor's burst of writes, short enough to
  feel interactive. It is the *hint*-path latency only; correctness never depends
  on it (the rescan backstops it).
- **Periodic rescan interval:** default ~60 s, configurable; this is the worst-case
  latency for a *silently dropped* event, not for normal edits (those come via the
  hint path in ~150 ms).
- **Windows buffer:** call `WithBufferSize` to raise the `ReadDirectoryChangesW`
  buffer above the 64 KiB default for **local** disk (overflow is rare but
  recoverable). Do **not** exceed 64 KiB when the target may be a network share
  (the API fails with ERROR_INVALID_PARAMETER > 64 KiB over the network, and
  network-FS notifications "generally don't work" anyway → those folders run
  rescan-only). Concretely: 128 KiB–512 KiB on local disk; treat *any* overflow as
  a rescan trigger, never a fatal error. The exact value is tuned on a real Windows
  box (Phase 6).
- **macOS backend: Option M1 (fsnotify/kqueue) for v1**, with the max-watch guard
  → rescan-only fallback. Revisit Option M2 (fsevents) only if the FD ceiling is
  hit in practice (flag to Phase 6).
- **Network/virtual FS:** detect and fall back to **rescan-only** with a warning
  (notifications don't work there).

## Rationale

- The "rescan is truth" rule is not our invention — it is the *documented* recovery
  path in both Microsoft's and Apple's own watcher documentation. Building on it
  makes a dropped event a latency event, never a correctness event.
- fsnotify (kqueue on macOS) keeps the dependency surface minimal (GR-11) and one
  code path; watching directories (not files) bounds the kqueue FD cost to the
  directory count, which the max-watch guard caps.

## Consequences

- Drives `internal/reconcile/watcher.go` (per-dir `Add`/`Remove`, watch-set
  reconcile, `Errors`→rescan, `WithBufferSize` on Windows) and `scanloop.go`
  (150 ms debounce, periodic rescan ticker, settled-change detection).
- `go.mod` gains `github.com/fsnotify/fsnotify` (already the one expected dep per
  GR-11).
- **The Windows overflow/drop behaviour, the exact buffer value, and rename-watch
  cleanup cannot be verified on the Mac** (plan/README:
  "ReadDirectoryChangesW buffer-overflow event drops vs macOS FSEvents
  coalescing") → Phase 6 CI `windows-latest` + a load test in
  `CROSS_PLATFORM_CHECKLIST.md`. The SR-11 "drop a synthetic event → rescan
  recovers" test runs on the Mac.
