# Cross-platform finding — Watcher reality per OS (events are hints; rescan is truth)

- Slug: `watcher-reality` · confirms **XP-5**
- Phase: 2 (crossplatform-researcher, elevated track)
- Reads-first: `docs/audit/rules/crossplatform-rules.md` (XP-5), `go-rules.md`
  (GR-9, GR-10), `sync-rules.md` (SR-11).
- Decision logged before this finding:
  `docs/audit/decisions/crossplatform/watcher-trust-and-debounce.md`.
- Access date for all URLs: **2026-06-28**.
- Severity: **High** — every OS watcher silently drops events under load; without
  the rescan safety net, changes are missed → divergence / data not synced.

## Claim

Filesystem-watcher events are **hints** for a fast, debounced (~150 ms) reaction.
The **periodic full rescan + tree rebuild is the source of truth** (SR-11); on any
overflow/error the engine triggers an **immediate full rescan** and reconciles the
watch set. Correctness never depends on the event stream being complete.

## Evidence (primary, verbatim)

- **Windows `ReadDirectoryChangesW` silently discards on overflow.** "If the buffer
  overflows, ReadDirectoryChangesW will still return **true**, but the entire
  contents of the buffer are discarded and the *lpBytesReturned* parameter will be
  zero, which indicates that your buffer was too small to hold all of the changes
  that occurred." Recovery is a rescan: "If the number of bytes transferred is zero
  … you should compute the changes by enumerating the directory or subtree", and
  "ReadDirectoryChangesW fails with **ERROR_NOTIFY_ENUM_DIR** when the system was
  unable to record all the changes … In this case, you should compute the changes
  by enumerating the directory or subtree." Buffer cap: it "fails with
  ERROR_INVALID_PARAMETER when the buffer length is greater than 64 KB and the
  application is monitoring a directory over the network"
  ([Microsoft, *ReadDirectoryChangesW*](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-readdirectorychangesw),
  ms.date 2018-12-05, page `updated_at` 2025-07-01, accessed 2026-06-28).
- **fsnotify (v1.10.1, 2026-05-04) surfaces all of this** and adds its own caveats
  ([pkg.go.dev/github.com/fsnotify/fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify),
  accessed 2026-06-28), all verbatim:
  - non-recursive: "Subdirectories are not watched (i.e. it's non-recursive)."
  - watch loss: "A watch will be automatically removed if the watched path is
    deleted or renamed. The exception is the Windows backend, which doesn't remove
    the watcher on renames."
  - overflow: `ErrEventOverflow`; windows "The buffer size is too small;
    WithBufferSize() can be used to increase it"; "The default
    ReadDirectoryChangesW() buffer size is 64K, which is the largest value that is
    guaranteed to work with SMB filesystems"; inotify "IN_Q_OVERFLOW".
  - coalescing: "A single 'write action' … may show up as one or multiple writes …
    you may want to wait until you've stopped receiving them."
  - network FS: "Notifications on network filesystems (NFS, SMB, FUSE, etc.) …
    generally don't work."
- **macOS, stated precisely (a correction to the preliminary XP-5 wording):**
  - The *native* FSEvents API coalesces and is advisory: "events may be coalesced
    into a single event … you will receive an event with the
    `kFSEventStreamEventFlagMustScanSubDirs` flag set"; "you should treat the events
    list as advisory rather than a definitive list of all changes … backup software
    should still periodically perform a full sweep"
    ([Apple, *Using the File System Events API*](https://developer.apple.com/library/archive/documentation/Darwin/Conceptual/FSEvents_ProgGuide/UsingtheFSEventsFramework/UsingtheFSEventsFramework.html),
    accessed 2026-06-28).
  - **But fsnotify does NOT use FSEvents on macOS — it uses `kqueue`** ("kqueue —
    BSD, macOS — Supported"), and "kqueue requires opening a file descriptor for
    every file that's being watched … You will run in to your system's 'max open
    files' limit faster" (fsnotify docs, accessed 2026-06-28). XP-5's preliminary
    text spoke of "FSEvents coalescing" as our backend; in fact our chosen library
    is kqueue-based on macOS. Both still demand the rescan backstop, and both are
    advisory — so the rule is unchanged, only the mechanism is named correctly.

## The takeaway

The "rescan is the source of truth" rule is not our invention — it is the
**documented recovery path** in both Microsoft's ("compute the changes by
enumerating the directory or subtree") and Apple's ("perform a full sweep") own
watcher documentation. A dropped event then becomes a *latency* event (bounded by
the rescan interval), never a *correctness* event.

## Decision applied

`decisions/crossplatform/watcher-trust-and-debounce.md`:
- **Hybrid trust model** (events=hints + ~150 ms debounce + periodic full rescan as
  truth + immediate rescan on overflow/error) — Option C.
- **fsnotify everywhere** (kqueue on macOS) for v1 — Option M1; watch
  **directories** not files (GR-9) so the kqueue FD cost scales with directory
  count, capped by a max-watch guard → rescan-only fallback. Revisit
  `github.com/fsnotify/fsevents` only if the FD ceiling bites.
- **Windows buffer:** `WithBufferSize` raised above 64 KiB on **local** disk only
  (overflow is recoverable by rescan); keep ≤ 64 KiB / fall back to rescan-only on
  network shares (where notifications don't work and >64 KiB errors).
- **Watch-set reconcile** on every settled change and rescan: re-`Add` after the
  Windows-no-remove-on-rename case, `Add` new subdirs (non-recursive), `Remove`
  gone subdirs (GR-9).
- **150 ms debounce** justified by the coalescing docs ("wait until you've stopped
  receiving them"); it is hint-path latency only (GR-10).

## Test obligations

- SR-11 (Mac-testable): drop a synthetic event / simulate overflow, assert the
  periodic rescan still detects and converges the missed change.
- GR-10 (Mac-testable): inject a burst of events for one path within the window;
  assert exactly one hash/diff fires.
- Watch-set reconcile: create/rename/delete a subdir; assert watches are added/
  removed and no leak.

## Cannot be verified on the Mac → Phase 6

The **Windows** `ReadDirectoryChangesW` overflow/silent-drop, the exact
`WithBufferSize` value under load, and the rename-watch-cleanup quirk are Windows-
only (plan/README: "ReadDirectoryChangesW buffer-overflow event drops vs macOS
FSEvents coalescing"). Closed by the CI `windows-latest` job + a load test in
`docs/audit/CROSS_PLATFORM_CHECKLIST.md`.
