# Sync-engine antipatterns — the anti-slop catalogue (what makes a sync engine SUBTLY LOSE DATA)

> **Role / status.** Produced by the Phase 2 **antipatterns-researcher**
> (anti-slop pass). This is a *checklist of what NOT to do*, consumed by the
> Phase 3 critics and the Phase 5 implementers. Scope is **data integrity only** —
> "wrong or lost data", never merely "slow" (per
> `.claude/agents/antipatterns-researcher.md`). Methodology + the rule-gap
> handling is logged in
> `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.
>
> **Evidence policy.** Every "correct approach" cites a current source; **access
> date for all URLs is 2026-06-28**. In-repo `file:line` / `SR-n` references are
> to this audit tree.

Each antipattern has the four parts the contract demands:
1. **Wrong shape** — the tempting code that looks fine.
2. **Why it LOSES/ corrupts data** — the integrity failure, not a perf cost.
3. **Test** — the failing assertion that catches it.
4. **Correct approach** — with a citation, tied to the hard rule that prevents it.

Each is tagged **[maps SR-n/GR-n/XP-n]** when an existing rule already forbids it,
or **[GAP → PROPOSED SR-x]** when no current rule covers it. Severity is the
data-integrity rubric from the decision file: **critical** (destroys/overwrites
data outside one file's expected update, or wipes many files), **high** (silently
corrupts/loses one file's content or edit), **medium/low** (narrow or already
strongly mitigated).

---

## Proposed new rules (PROPOSED — Phase 3 ratifies; not yet binding)

The research found five severe data-loss modes no current rule names. They are
proposed here as **SR-14..SR-17** and flagged for the rules set (the contract:
"if no rule covers it, that is a gap — flag it"). They are **PROPOSED**, routed
through the normal Phase 3 critique + skeptic vote; the consolidator folds the
survivors into `sync-rules.md`.

- **SR-14 (PROPOSED) — A received path is materialised only if it resolves
  strictly inside the sync root.** Reject (and flag) any received name that is
  absolute, contains a `..` component, is non-canonical (`path.Clean(name) !=
  name`), or whose final on-disk target would be reached *through an existing
  symlink*. Never `open`/`rename` to a path the peer chose without this check.
  Prevents AP-20, AP-21. Evidence: AP-20/AP-21 below.
- **SR-15 (PROPOSED) — A bulk deletion is never derived from an empty or
  unverified scan.** Before emitting tombstones, require (a) a present root/folder
  marker, (b) a persisted last-synced baseline to diff against, and (c) a
  bulk-delete sanity gate (if "everything vanished", treat the folder as
  *unavailable*, not *emptied*, and stop — do not propagate). Prevents AP-15.
- **SR-16 (PROPOSED) — Verify-after-reconstruct before the atomic rename.**
  Recompute the whole-file `content_hash` over the finished temp file and assert
  it equals the expected `content_hash`; on mismatch discard the temp and **do
  not** rename. Prevents AP-05/AP-06. (This is synthesis AL-12 promoted to a
  rule.)
- **SR-17 (PROPOSED) — Detect change-during-hash and change-during-serve.**
  Capture a stat fingerprint (`size`, `mtime`, and where available `ctime`/inode)
  *before* hashing or streaming a file and re-check it *after*; if it changed,
  mark the file dirty and re-hash — never publish an index entry, and never serve
  bytes, for a file that mutated under you. Prevents AP-10; hardens AP-09/AP-11.

---

## A. Materialisation & durability (writing a received file to disk)

### AP-01 — Writing the destination file in place (truncate + stream into it) · **high** · [maps SR-1]
**Wrong shape**
```go
f, _ := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
for chunk := range chunks { f.Write(chunk) }   // dst is now live + half-written
```
**Why it loses data:** the moment `O_TRUNC` runs, the user's old file is gone; any
crash, disconnect, or bad chunk between the first and last write leaves a
truncated/garbage `dst` — the old content is unrecoverable and the new content is
incomplete. "If there's a crash during the write, we could get `a boo`, `a far`,
or any other combination" ([danluu, *Files are
hard*](https://danluu.com/file-consistency/)). A concurrent reader also sees the
half-written file. Real cascade: a 35 KB config "progressively corrupted to 9 KB,
1.5 KB, then 77 bytes — a classic race-condition signature" under concurrent
writes + a sync client ([claude-code #29153](https://github.com/anthropics/claude-code/issues/29153)).
**Test:** kill the transfer after K bytes; assert `dst` is either absent or the
*previous complete* version, never partial; re-run completes.
**Correct approach:** write to a temp file in the **same directory**, then
`os.Rename` it over `dst`; on any pre-rename error delete the temp and leave
`dst` untouched ([Stapelberg, *Atomically writing files in
Go*](https://michael.stapelberg.ch/posts/2017-01-28-golang_atomically_writing/);
[google/renameio](https://pkg.go.dev/github.com/google/renameio)). This is **SR-1**.

### AP-02 — Rename without fsync, or fsync in the wrong order (0-byte / lost file on crash) · **high** · [maps SR-2]
**Wrong shape**
```go
os.WriteFile(tmp, data, 0o644)   // no Sync
os.Rename(tmp, dst)              // metadata renamed, data may not be on disk; parent dir never fsync'd
```
**Why it loses data:** "rename isn't atomic on crash. POSIX says that rename is
atomic, but this only applies to normal operation, not to crashes"
([danluu](https://danluu.com/file-consistency/)). If power fails after the rename
is committed but before the data reaches disk you get **a zero-length file instead
of either the old or the new content** — the exact ext4-vs-ext3 regression that
surprised everyone ([Red Hat, *Possible data loss on ext4 after power
loss*](https://access.redhat.com/solutions/369383)). And the rename itself can be
lost: "when you first create a file, you need to call fsync on the directory that
contains it, otherwise it might not exist after a failure"
([evanjones, *Durability: Linux File
APIs*](https://www.evanjones.ca/durability-filesystem.html)). The two most common
real bugs are "incorrectly assuming ordering between syscalls" and "assuming that
syscalls were atomic" ([danluu](https://danluu.com/file-consistency/)).
**Test:** with an injectable FS spy, assert the call order is
`write → tmp.Sync() → Rename → parentDir.Sync()`; a power-cut integration test
asserts `dst` is never 0-byte.
**Correct approach:** temp → `tmp.Sync()` → `os.Rename` → **fsync the parent
directory** → close. This is **SR-2**.

### AP-03 — Assuming `os.Rename` atomically replaces an existing file on Windows · **high** · [maps SR-1/SR-2; cross-platform GAP] → finding `windows-rename-not-atomic`
**Wrong shape**
```go
os.Rename(tmp, dst) // works on macOS; on Windows fails or is non-atomic when dst exists
```
**Why it loses data:** "In Posix-compliant OSes, the os.Rename() function works
properly (and atomically), including when replacing an existing file. In Windows
this does not work correctly as designed and fails when replacing an existing
file" ([golang/go #8914, *os: make Rename atomic on
Windows*](https://github.com/golang/go/issues/8914)). On Windows the replace must
go through `ReplaceFileW`/`MoveFileEx`, and even that is atomic only within one
NTFS volume — never across volumes or on FAT/network shares. Merkle Sync's entire
reason to exist is Mac↔Windows; a naive rename means the **Windows** side either
errors out (update silently never applied → stale/lost edit) or leaves a corrupt
`dst`.
**Test (cross-compile + windows-latest CI):** on Windows, repeatedly replace an
existing `dst` under simulated interruption; assert no error path drops the
update and `dst` is never partial.
**Correct approach:** use the platform replace primitive (Windows
`ReplaceFileW`/`MoveFileEx(MOVEFILE_REPLACE_EXISTING|WRITE_THROUGH)`), keep temp
and dst on the same volume, and treat Go's "Rename is not atomic on non-Unix
platforms" caveat as load-bearing ([natefinch/atomic](https://github.com/natefinch/atomic);
[golang/go #8914](https://github.com/golang/go/issues/8914)). Hardens **SR-1/SR-2**.

### AP-04 — Temp file on a different filesystem than the destination · **medium** · [maps SR-1]
**Wrong shape**
```go
tmp := filepath.Join(os.TempDir(), "msync-xxxx")  // /tmp may be a different FS than the sync root
io.Copy(tmpFile, src); os.Rename(tmp, dst)        // EXDEV → fallback copy → non-atomic
```
**Why it loses data:** "You can't do an atomic rename across filesystem
boundaries" ([Stapelberg](https://michael.stapelberg.ch/posts/2017-01-28-golang_atomically_writing/)).
A cross-device rename returns `EXDEV`; code that "helpfully" falls back to
copy+delete reintroduces the in-place-write window (AP-01) — a crash mid-copy
corrupts `dst`.
**Test:** put the sync root on a loopback/secondary FS; assert the temp is created
in `filepath.Dir(dst)` (same FS) and the rename never degrades to a copy.
**Correct approach:** always create the temp in the destination's own directory.
This is the "same directory ensures same filesystem" half of **SR-1**.

### AP-05 — Renaming a reassembled file into place without re-hashing it · **high** · [GAP → PROPOSED SR-16] → finding `no-verify-after-reconstruct`
**Wrong shape**
```go
for _, blk := range plan { writeAt(tmp, blk.offset, recv(blk)) }
os.Rename(tmp, dst) // trusted blindly: wrong order, a dropped block, or a hash collision = silent corruption
```
**Why it loses data:** a chunked transfer can reassemble *wrongly* and still look
"complete" — a misordered write, an off-by-one offset, a duplicated/missed block,
or a per-block hash that matched the wrong bytes. The result is a full-size file
of **wrong content** that the engine then treats as converged (its leaf hash may
even be recomputed from the corrupt bytes and broadcast as truth). rsync guards
exactly this: "rsync always verifies that each transferred file was correctly
reconstructed on the receiving side by checking a whole-file checksum that is
generated as the file is transferred"
([rsync(1)](https://man7.org/linux/man-pages/man1/rsync.1.html)).
**Test:** inject a corrupted/misordered block; assert the temp is **discarded**,
no rename happens, and `dst` keeps its previous content.
**Correct approach:** before the atomic rename, recompute the whole-file SHA-256
of the temp and assert it equals the expected `content_hash`; mismatch ⇒ discard
+ refetch. Proposed **SR-16** (= synthesis AL-12); composes with **SR-1**.

### AP-06 — Resuming a kept partial temp without re-verifying it · **medium** · [maps SR-1; PROPOSED SR-16]
**Wrong shape:** keep `tmp` after an interrupted transfer (good — saves re-fetch)
but on resume, append and rename **without** re-validating the bytes already on
disk.
**Why it loses data:** the retained partial may have been written by a *different*
version of the file (the source changed between attempts) or partially clobbered
on disk; appending to it yields a Frankenstein file. Syncthing keeps the temp "for
up to a day ... to avoid needlessly requesting data" but **re-validates every
block's hash before reuse** ([Understanding
Synchronization](https://docs.syncthing.net/users/syncing.html);
`lib/scanner/blocks.go:124-131` `Validate`, @v2.1.1).
**Test:** corrupt a retained temp between attempts; assert resume re-validates and
re-fetches the bad region, and the final file passes the whole-file check (AP-05).
**Correct approach:** treat a retained temp as *untrusted cache*: re-hash blocks
before reuse, and always run the AP-05 whole-file verify before rename.

---

## B. Change detection & scanning (deciding *what* changed locally)

### AP-07 — Watcher-only change detection (no periodic / no overflow rescan) · **high** · [maps SR-11, GR-9]
**Wrong shape**
```go
for ev := range watcher.Events { enqueue(ev.Name) } // and nothing else
```
**Why it loses data:** OS watchers **silently drop events under load**. fsnotify
surfaces `ErrEventOverflow` — on Linux inotify returns `IN_Q_OVERFLOW` when
`fs.inotify.max_queued_events` is exceeded ([pkg.go.dev/.../fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify)).
In practice "under spiky situations with 10k+ files ... around 600 files [were]
dropped" ([fsnotify #148, *Failed to pick up create event under heavy
load*](https://github.com/fsnotify/fsnotify/issues/148)), and after an overflow
"the cache state in the application is no longer synchronized with the filesystem
state" ([tilt #1772](https://github.com/tilt-dev/tilt/issues/1772)). A dropped
create/modify that is never rescanned is **a change that never syncs** — silent
divergence/loss.
**Test:** force an overflow (or drop a synthetic event); assert the periodic /
on-overflow rescan still detects and converges the missed change.
**Correct approach:** watcher events are *hints*; a full rescan + tree rebuild
(periodic and **on every `Errors`/overflow signal**) is the source of truth. This
is **SR-11** / **GR-9**.

### AP-08 — Watching individual files instead of their directories · **medium** · [maps GR-9]
**Wrong shape:** `watcher.Add("notes.txt")`.
**Why it loses data:** atomic-saving editors (and *our own* SR-1 writer) replace a
file by renaming a temp over it; "the watcher on the original file is now lost"
([pkg.go.dev/.../fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify)), so
every subsequent edit is invisible → changes stop propagating with no error.
**Test:** atomically replace a watched file; assert further edits are still
detected (because the *directory* is watched).
**Correct approach:** watch parent directories, filter by `Event.Name`; reconcile
the watch-set on every settled change. **GR-9**.

### AP-09 — Acting on the first of N rapid write events (no debounce) · **high** · [maps GR-10; hardened by PROPOSED SR-17]
**Wrong shape:** hash + index the file on the first `Write` event.
**Why it loses data:** "a single write action ... may show up as one or multiple
writes" ([fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify)); acting on
the first event hashes a **half-written file** and may broadcast/serve that torn
content as if it were a real version (see AP-10).
**Test:** emit a burst of writes for one path inside the window; assert exactly
one hash fires, after the path goes quiet.
**Correct approach:** per-path debounce (~150 ms quiet window) before hashing.
**GR-10**.

### AP-10 — Hashing or streaming a file that is still being modified (content TOCTOU) · **high** · [GAP → PROPOSED SR-17] → finding `change-during-hash-transfer`
**Wrong shape**
```go
fi, _ := os.Stat(p); h := sha256file(p)         // file keeps being written during the read
publishIndex(p, h, fi.Size())                    // hash/size describe bytes that no longer exist as a unit
// ... later, on a peer's REQUEST, we re-open and stream current bytes != what we indexed
```
**Why it loses data:** this is a *content-level* time-of-check/time-of-use race
distinct from the dropped-event case. Between stat, hash, and serve the file's
bytes change, so (a) we index a hash that matches no on-disk state, or (b) we
stream bytes inconsistent with the advertised `content_hash`/blocks. Syncthing
hits exactly this: sync fails when "a file has changed, but still has the same
timestamp and size as when syncthing already created the hash" ([syncthing
#2414](https://github.com/syncthing/syncthing/issues/2414)), surfaced to users as
"file changed during hashing" ([Syncthing
forum](https://forum.syncthing.net/t/file-changed-during-hashing/18046)). TOCTOU
is a recognised class: "the more things happening simultaneously, the greater the
chance that the state of a resource will change between the time-of-check and the
time-of-use" ([Wikipedia,
*TOCTOU*](https://en.wikipedia.org/wiki/Time-of-check_to_time-of-use); [CERT
FIO45-C](https://wiki.sei.cmu.edu/confluence/display/c/FIO45-C.+Avoid+TOCTOU+race+conditions+while+accessing+files)).
The receiver may even verify each block hash against a *stale* expectation and
write torn data that passes per-block but is globally inconsistent.
**Test:** rewrite a file continuously while it is scanned/served; assert the
engine never publishes an index entry or serves bytes for a file whose stat
fingerprint changed during the operation (it re-hashes instead).
**Correct approach:** snapshot a stat fingerprint (`size`,`mtime`, ideally
`ctime`/inode) before hashing and re-check after; if it changed, mark dirty and
re-hash, and never serve a file mid-change. Proposed **SR-17**; AP-05's
whole-file verify on the receiver is the second line of defence.

### AP-11 — Trusting `size`+`mtime` to SKIP rehashing (missed in-place edit) · **high** · [refines SR-11] → finding `mtime-size-skip-rehash`
**Wrong shape**
```go
if fi.Size()==prev.Size && fi.ModTime()==prev.ModTime { return prev.Hash } // assume unchanged, skip hashing
```
**Why it loses data:** `size`+`mtime` are a fine *cheap-reject* prefilter, but
using them as the *authority to skip* hashing means an in-place edit that keeps
the same length and restores/preserves the mtime is **never detected** — so the
real change is never broadcast, and the two peers silently diverge (one holds
content the other will never receive). rsync's default "quick check ... looks for
files that have changed in size or in last-modified time"
([rsync(1)](https://man7.org/linux/man-pages/man1/rsync.1.html)); rsync ships
`-c/--checksum` *precisely because* the quick check misses content changes when
size+mtime are unchanged. Same root cause as AP-10's "same timestamp and size"
report ([syncthing #2414](https://github.com/syncthing/syncthing/issues/2414)).
mtime can also be deliberately or incidentally reset (archives, `touch -r`,
editors that preserve mtime).
**Test:** modify a file's bytes in place keeping identical size and mtime; assert
a full rescan still detects the change and converges it.
**Correct approach:** size+mtime may gate *whether to bother re-reading* on a hot
path, but **the periodic rescan computes the content hash unconditionally** and is
the source of truth (the rsync `--checksum` posture). Refines **SR-11** (rescan is
truth) and **AL-11** (two-tier gating is an optimisation, never the change
oracle).

---

## C. Ordering, conflict, and deletion (deciding *who wins* and *what is gone*)

### AP-12 — Using mtime / wall-clock as the ordering authority (last-write-wins) · **high** · [maps SR-4]
**Wrong shape**
```go
if remote.ModTime.After(local.ModTime) { overwrite(local, remote) } // LWW by clock
```
**Why it loses data:** two laptops' clocks skew and NTP steps backwards; "LWW
silently overwrites legitimate concurrent updates" ([oneuptime, *Last-Write-Wins*](https://oneuptime.com/blog/post/2026-01-30-last-write-wins/view)),
and "when clocks were skewed ... the incorrect document was declared as the
winner" ([systemdr, *The Clock Skew
Conflict*](https://systemdr.substack.com/p/the-clock-skew-conflict-when-time)).
A concurrent edit on the "older-clock" side is discarded with no conflict copy —
gone. The canonical warning: timestamp ordering is a "trouble with timestamps"
trap ([aphyr](https://aphyr.com/posts/299-the-trouble-with-timestamps)).
**Test:** apply a causally-newer edit that carries an *older* mtime; assert it
still wins (because version vectors order it), and that a truly concurrent pair
produces a conflict copy, not an overwrite.
**Correct approach:** **version vectors** decide ordering; mtime is *only* the
deterministic tiebreaker once VVs say "concurrent". This is **SR-4** (+ SR-7).

### AP-13 — Re-broadcasting / bumping the version on applying a received write (sync loop) · **high** · [maps SR-6, SR-8]
**Wrong shape**
```go
onTreeChanged(func(p string){ vv.Bump(self); broadcastIndex(p) }) // fires for OUR OWN applied writes too
```
**Why it loses/corrupts data:** applying a received file makes fsnotify fire; if
that self-induced event is treated as a new local authorship, peer A→B→A→…
ping-pong floods the network and **mints spurious conflict copies** of unchanged
files. The general pattern is well known: "the issue typically occurs when a
watcher's own writes re-trigger the watcher" ([KeePass, *Cloud sync with triggers
creates an infinite
loop*](https://sourceforge.net/p/keepass/discussion/329220/thread/a9aab281bd/);
[kestra #6847](https://github.com/kestra-io/kestra/issues/6847)).
**Test:** apply a received file, let the watcher fire; assert **zero** outbound
broadcasts and no VV bump.
**Correct approach:** bump the VV / broadcast **only on confirmed local
authorship**, never on apply; after apply, record the expected `content_hash` so
the rescan sees "no new authorship". **SR-6** + **SR-8** (defence-in-depth with
SR-3 idempotency).

### AP-14 — Conflict loser overwritten or deleted instead of renamed · **high** · [maps SR-7]
**Wrong shape**
```go
if conflict(local, remote) { write(path, winner); /* loser bytes gone */ }
```
**Why it loses data:** the whole no-data-loss contract is that a concurrent edit's
loser is *kept*. Real-time sync without this is exactly the "save over something
by mistake, and that new version replaces the old one across the board ... there's
no safety net" failure ([howtogeek, *This common file-syncing mistake can cost you
your data*](https://www.howtogeek.com/this-common-file-syncing-mistake-can-cost-you-your-data/)).
**Test:** concurrently edit one file on two instances with differing content;
assert convergence to **two** files (winner + one `.sync-conflict-*`), both
versions present, on **both** peers.
**Correct approach:** loser is **renamed** to
`<name>.sync-conflict-<UTC>-<deviceID>.<ext>` and synced as a normal file; never
deleted. Deterministic winner (older-mtime-loses, then larger-DeviceID-loses).
**SR-7** (Syncthing `moveForConflict`, `folder_sendrecv.go:1863-1906` @v2.1.1).

### AP-15 — Treating an empty / unavailable scan as deletions → mass-delete · **critical** · [GAP → PROPOSED SR-15] → finding `mass-delete-empty-scan`
**Wrong shape**
```go
prev := lastKnownFiles            // or: nil, after a restart with no persisted baseline
cur  := scan(root)                // root unmounted/empty/locked ⇒ cur == {} 
for f := range prev { if !cur[f] { tombstone(f); broadcast(f) } } // "everything was deleted" → peer wipes its copy
```
**Why it loses data:** absence is ambiguous — "deleted here" vs "the folder isn't
really there right now". If the root is briefly unavailable (unmounted removable
drive, network mount not up, permissions blip, or a restart before the baseline
loads) the scan reads zero files and the engine concludes *the user deleted
everything* and propagates a **mass deletion** that wipes the peer. This is a
documented Syncthing failure: "there is the directory, it is accessible, but there
are no files in it, which means the user has deleted all the files ... these files
were reported as deleted ... and the other machine acted accordingly"; and the
folder marker exists exactly as the guard — "the marker going missing is the
feature that tells you it's about to nuke everything" ([Syncthing forum, *Folder
marker missing ... file mass
deletion*](https://forum.syncthing.net/t/folder-marker-missing-re-created-it-file-mass-deletion/14346);
related [syncthing #9371](https://github.com/syncthing/syncthing/issues/9371)).
This is the inverse data-loss of resurrection and is the **highest-impact** mode
because one bad scan deletes the whole folder.
**Test:** point the root at an empty/temporarily-unreadable dir while a baseline
exists; assert **no** tombstones are emitted and **no** deletion is broadcast
(the folder is declared unavailable instead).
**Correct approach:** require a present root marker before any work; persist a
last-synced baseline and only derive deletions by diffing a *successful, verified*
scan against it; gate bulk deletes ("if ~all files vanished at once, stop and flag,
don't propagate"). Proposed **SR-15**; ties to synthesis **R-5** (persisted-state
gap) and **OQ-5**.

### AP-16 — Tombstone resurrection (premature GC or non-dominating delete) · **high** · [maps SR-10]
**Wrong shape:** represent a delete as plain absence, or GC the tombstone while a
live peer may still hold a pre-delete version.
**Why it loses data (inverse):** a stale peer reconnecting with the old file
**re-creates** it everywhere — the file you deleted comes back. Syncthing's
marquee long-lived bug: removed-device "ghost" counters mean "neither vector can
dominate ... the file persists clean rather than deleted", with a reporter seeing
**8,591 conflicts** ([syncthing
#10590](https://github.com/syncthing/syncthing/issues/10590)).
**Test:** partition a peer, delete on the other, reconnect; assert the file is
deleted on the stale peer and **not** resurrected on the deleter; a
premature-GC test asserts no resurrection.
**Correct approach:** a delete is a tombstone = `deleted=true` + a **bumped VV
that dominates** any stale pre-delete VV; retain until both peers ack, then GC
(ack-gated, never blind). **SR-9/SR-10**.

---

## D. Wire / framing (receiving bytes from a peer)

### AP-17 — Treating one `conn.Read` as a whole message / no max-length guard · **medium** · [maps SR-12, GR-8]
**Wrong shape**
```go
buf := make([]byte, hdr.Len) // hdr.Len read with a bare Read; attacker sets Len=4 GiB
n, _ := conn.Read(buf)       // assumes n == hdr.Len and one Read == one frame
handle(buf[:n])              // partial frame → every later message is misaligned
```
**Why it loses/corrupts data:** "a single read from a TCP stream can return any
number of bytes from 1 to the size of the buffer" ([openmymind, *Reading from TCP
streams*](https://www.openmymind.net/2012/1/12/Reading-From-TCP-Streams/)); a
length-prefix reader without a sanity bound "can be given a huge message size ...
[causing] an OutOfMemoryException, so one must include a maximum message size
'sanity check'" ([Stephen Cleary, *Message
Framing*](https://blog.stephencleary.com/2009/04/message-framing.html)). A
mis-sized read desynchronises the stream so every subsequent frame is parsed from
the wrong offset — silently corrupting all later index/transfer data — or OOM-kills
the daemon.
**Test:** feed a frame through `iotest.OneByteReader` (forces partial reads),
assert correct reassembly; feed an oversized length, assert `ErrFrameTooLarge` +
connection dropped + **no** large allocation.
**Correct approach:** `io.ReadFull` for header and body; validate
`0 < L <= MaxFrameLen` **before** allocating. **SR-12** / **GR-8**.

---

## E. Cross-platform identity & path safety (Mac ↔ Windows)

### AP-18 — Not normalising Unicode (NFD vs NFC) → same file becomes two leaves, or a collision · **high** · [maps XP-2, SR-13]
**Wrong shape:** key the tree by the raw on-disk filename bytes.
**Why it loses/corrupts data:** "A file created on macOS with an accented name
arrives on Linux in NFD form, and if your code expects NFC, the filename will not
match" ([nicolasbouliane, *File names, unicode normalization
problems*](https://nicolasbouliane.com/blog/unicode-normalization)). macOS stores
NFD, Windows/Linux expect NFC ([Eclectic Light,
*Unicode normalization and APFS*](https://eclecticlight.co/2021/05/08/explainer-unicode-normalization-and-apfs/)).
Two effects: (a) `résumé.pdf` hashes as two different leaves → **roots never
converge** (SR-5 broken); (b) if both forms land on one volume, the second write
can **collide and overwrite** the first.
**Test:** round-trip a name set Mac→wire→Windows→wire→Mac; assert identical
canonical keys and identical subtree hashes.
**Correct approach:** normalise to **NFC** at the boundary (scan-time and on
receive); keep the on-disk byte form only to re-open on macOS. **XP-2** / **SR-13**.

### AP-19 — Case-insensitive collision clobber (`File.txt` vs `file.txt`) · **high** · [maps XP-4]
**Wrong shape:** apply each received name verbatim on a case-insensitive target.
**Why it loses data:** on Windows (and the macOS default volume) `OSCAR`,
`Oscar`, `oscar` "are the same" ([Microsoft, *Naming
Files*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)).
Two case-variant files from a case-sensitive source then map to one path; writing
the second **silently overwrites** the first. The hazard is academically
catalogued: "case-sensitivity-induced name collisions ... Utilities handle many
name collision scenarios unsafely" ([*Unsafe at Any Copy: Name Collisions from
Mixing Case Sensitivities*, arXiv:2211.16735](https://arxiv.org/abs/2211.16735),
which also grounds the git CVE-2021-21300 case-collision RCE).
**Test:** deliver `File.txt` then `file.txt` to a case-insensitive target; assert
the second is **refused + flagged**, both retained in the tree, neither clobbered.
**Correct approach:** detect via a case-folded collision index; **refuse + flag,
never clobber** (Syncthing posture, `lib/fs/casefs.go` @v2.1.1). **XP-4**.

### AP-20 — Materialising a received path without constraining it to the sync root (path traversal) · **critical** · [GAP → PROPOSED SR-14] → finding `path-traversal-received-path`
**Wrong shape**
```go
dst := filepath.Join(root, recvName)  // recvName = "../../.ssh/authorized_keys" or "/etc/cron.d/x" or "C:\\Windows\\..."
writeAtomic(dst, bytes)               // escapes root; overwrites/destroys files anywhere writable
```
**Why it loses/corrupts data:** the peer chooses the filename; a name with `..`
components, an absolute path, or a drive/UNC prefix escapes the synced folder and
**overwrites or destroys arbitrary files** outside it — the classic "zip slip".
"The code joins the destination path with the raw ... entry filename ... without
validating that the filename doesn't contain `../` ... [so] files escape the
intended directory" ([Zed
GHSA-v385-xh3h-rrfr](https://github.com/zed-industries/zed/security/advisories/GHSA-v385-xh3h-rrfr);
[Android, *Zip Path
Traversal*](https://developer.android.com/privacy-and-security/risks/zip-path-traversal)).
Even with TLS-trusted LAN peers this is a data-integrity must: one buggy/old peer,
or a name crafted on a case/normalisation-fuzzed FS, silently writes outside the
tree. Syncthing treats it as fatal: `checkFilename` rejects non-canonical /
absolute / traversal names and "a filename failing this test is grounds for
disconnecting the device" (`lib/protocol/protocol.go:646-670` @v2.1.1).
**Test:** feed received names `../x`, `a/../../x`, `/abs/x`, `C:\x`,
`a/./b`; assert each is rejected (and the peer flagged) and **nothing** is written
outside `root`.
**Correct approach:** validate every received path is canonical
(`path.Clean(name)==name`), relative, `..`-free, and that the *resolved* target
stays within `root` (`starts_with(canonical(root))`) before any FS call. Proposed
**SR-14**.

### AP-21 — Following an existing symlink at the destination when applying · **high** · [GAP → PROPOSED SR-14 (sibling)] → finding `symlink-following-on-apply`
**Wrong shape**
```go
// dst already exists on disk as a symlink (synced, or pre-existing) pointing outside root
os.Rename(tmp, dst)          // or OpenFile(dst, O_TRUNC): writes THROUGH the link to the target
```
**Why it loses data:** if `dst` (or any parent component) is a symlink pointing
outside the tree, a naive write/rename follows it and **overwrites the link's
target** — destroying data outside the synced folder. This is the textbook symlink
attack: "CVE-2000-1178 involved a text editor that followed symbolic links when
creating a rescue copy ... enabling local users to overwrite files belonging to
other users" ([Twingate, *What is a symlink
attack*](https://www.twingate.com/blog/glossary/symlink-attack); [LWN, *Exploiting
symlinks and tmpfiles*](https://lwn.net/Articles/250468/)). Combined with AP-20 it
is the second escape route out of the root.
**Test:** place a symlink at `dst` pointing to a sentinel file outside `root`;
apply an update; assert the sentinel is untouched and the engine wrote inside
`root` (or refused + flagged).
**Correct approach:** never write through a symlink for a regular-file apply —
`lstat` the target and parents, refuse if any component is an out-of-tree symlink
(or use `O_NOFOLLOW`/openat-relative semantics); treat synced symlinks as their own
typed, contained entity. Sibling of proposed **SR-14**; cross-ref **XP-6** (symlink
mapping is lossy on Windows).

---

## Antipattern → rule map (quick index)

| AP | Antipattern (one line) | Severity | Rule |
|---|---|---|---|
| AP-01 | Write dst in place (truncate + stream) | high | SR-1 |
| AP-02 | Rename without/with-wrong fsync (0-byte) | high | SR-2 |
| AP-03 | Assume `os.Rename` atomic-replaces on Windows | high | SR-1/SR-2 (**finding**) |
| AP-04 | Temp on a different filesystem (EXDEV → copy) | medium | SR-1 |
| AP-05 | Rename reassembled file without re-hash | high | **GAP → SR-16 (finding)** |
| AP-06 | Resume kept partial temp without re-verify | medium | SR-1 / SR-16 |
| AP-07 | Watcher-only, no rescan (dropped events) | high | SR-11, GR-9 |
| AP-08 | Watch files not directories (atomic-save) | medium | GR-9 |
| AP-09 | No debounce → hash half-written file | high | GR-10 |
| AP-10 | Hash/serve a file changing under you (TOCTOU) | high | **GAP → SR-17 (finding)** |
| AP-11 | size+mtime to SKIP rehash → missed edit | high | refines SR-11 (**finding**) |
| AP-12 | mtime/wall-clock as ordering (LWW) | high | SR-4 |
| AP-13 | Re-broadcast/bump on apply (sync loop) | high | SR-6, SR-8 |
| AP-14 | Conflict loser overwritten/deleted | high | SR-7 |
| AP-15 | Empty/unavailable scan → mass-delete | critical | **GAP → SR-15 (finding)** |
| AP-16 | Tombstone resurrection (premature GC) | high | SR-9, SR-10 |
| AP-17 | One `Read` == one frame / no len guard | medium | SR-12, GR-8 |
| AP-18 | No NFC normalisation (two leaves / collision) | high | XP-2, SR-13 |
| AP-19 | Case-collision clobber | high | XP-4 |
| AP-20 | Received path escapes root (traversal) | critical | **GAP → SR-14 (finding)** |
| AP-21 | Follow symlink at dst on apply | high | **GAP → SR-14 sibling (finding)** |

**Individual findings spun out (SEVERE):** AP-03, AP-05, AP-10, AP-11, AP-15,
AP-20, AP-21 → `docs/audit/findings/antipatterns/*.md` (`status: open`). The rest
are fully prevented by an existing *tested* rule and live here only.

---

## Sources (all accessed 2026-06-28)

Durability / atomic write:
- danluu, *Files are hard* — https://danluu.com/file-consistency/
- Stapelberg, *Atomically writing files in Go* — https://michael.stapelberg.ch/posts/2017-01-28-golang_atomically_writing/
- evanjones, *Durability: Linux File APIs* — https://www.evanjones.ca/durability-filesystem.html
- Red Hat, *Possible data loss on ext4 after power loss* — https://access.redhat.com/solutions/369383
- google/renameio — https://pkg.go.dev/github.com/google/renameio · natefinch/atomic — https://github.com/natefinch/atomic
- golang/go #8914, *os: make Rename atomic on Windows* — https://github.com/golang/go/issues/8914
- anthropics/claude-code #29153 (OneDrive concurrent-write corruption) — https://github.com/anthropics/claude-code/issues/29153

Watching / scanning / TOCTOU:
- fsnotify — https://pkg.go.dev/github.com/fsnotify/fsnotify · #148 — https://github.com/fsnotify/fsnotify/issues/148
- tilt #1772 (overflow remediation) — https://github.com/tilt-dev/tilt/issues/1772
- rsync(1) (quick check, --checksum, transfer verify) — https://man7.org/linux/man-pages/man1/rsync.1.html
- syncthing #2414 (same size+mtime, content changed) — https://github.com/syncthing/syncthing/issues/2414
- Syncthing forum, *file changed during hashing* — https://forum.syncthing.net/t/file-changed-during-hashing/18046
- TOCTOU — https://en.wikipedia.org/wiki/Time-of-check_to_time-of-use · CERT FIO45-C — https://wiki.sei.cmu.edu/confluence/display/c/FIO45-C.+Avoid+TOCTOU+race+conditions+while+accessing+files

Ordering / conflict / deletion:
- aphyr, *The trouble with timestamps* — https://aphyr.com/posts/299-the-trouble-with-timestamps
- oneuptime, *Last-Write-Wins* — https://oneuptime.com/blog/post/2026-01-30-last-write-wins/view
- systemdr, *The Clock Skew Conflict* — https://systemdr.substack.com/p/the-clock-skew-conflict-when-time
- howtogeek, *This common file-syncing mistake...* — https://www.howtogeek.com/this-common-file-syncing-mistake-can-cost-you-your-data/
- KeePass infinite sync loop — https://sourceforge.net/p/keepass/discussion/329220/thread/a9aab281bd/ · kestra #6847 — https://github.com/kestra-io/kestra/issues/6847
- Syncthing forum, *Folder marker missing ... mass deletion* — https://forum.syncthing.net/t/folder-marker-missing-re-created-it-file-mass-deletion/14346 · syncthing #9371 — https://github.com/syncthing/syncthing/issues/9371
- syncthing #10590 (ghost counters / resurrection) — https://github.com/syncthing/syncthing/issues/10590
- Syncthing, *Understanding Synchronization* — https://docs.syncthing.net/users/syncing.html

Framing:
- Stephen Cleary, *Message Framing* — https://blog.stephencleary.com/2009/04/message-framing.html
- openmymind, *Reading from TCP streams* — https://www.openmymind.net/2012/1/12/Reading-From-TCP-Streams/

Cross-platform identity / path safety:
- Microsoft, *Naming Files, Paths, and Namespaces* — https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file
- nicolasbouliane, *File names, unicode normalization problems* — https://nicolasbouliane.com/blog/unicode-normalization
- Eclectic Light, *Unicode normalization and APFS* — https://eclecticlight.co/2021/05/08/explainer-unicode-normalization-and-apfs/
- *Unsafe at Any Copy: Name Collisions from Mixing Case Sensitivities*, arXiv:2211.16735 — https://arxiv.org/abs/2211.16735
- Zed GHSA-v385-xh3h-rrfr (zip slip) — https://github.com/zed-industries/zed/security/advisories/GHSA-v385-xh3h-rrfr · Android Zip Path Traversal — https://developer.android.com/privacy-and-security/risks/zip-path-traversal
- Twingate, *What is a symlink attack* — https://www.twingate.com/blog/glossary/symlink-attack · LWN, *Exploiting symlinks and tmpfiles* — https://lwn.net/Articles/250468/

In-repo:
- `docs/audit/rules/{sync-rules,go-rules,crossplatform-rules}.md` (SR/GR/XP)
- `docs/audit/findings/synthesis/problem-space-map.md` (R-1..R-5, AL-12)
- `docs/audit/findings/codebases/syncthing-source.md` (`checkFilename`, `moveForConflict`, `Validate` @v2.1.1)
- `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md` (this pass's method)
