# Sync-engine invariants (HARD constraints)

These are the **non-negotiable** correctness rules of the reconciliation engine.
Each is stated as **Rule / Why / How tested**. They exist because the project's
contract is *no data loss, eventual convergence, atomic transfer, no sync loop*
(plan/README.md, plan/agent_roster.md). Violating any one is a data-integrity
bug, not a performance issue. The Phase 6 flow-verifier checks the system-level
versions of these; implementers must make each true at the unit level first.

IDs are stable (`SR-n`) so decisions, findings, and tests can cite them.

---

## SR-1 — Never write a destination file in place; temp-write then atomic rename

- **Rule:** to materialise a received file at `dst`, write the bytes to a
  temporary file **in the same directory / on the same filesystem** as `dst`,
  `fsync` it, then `os.Rename(tmp, dst)`. Never `os.OpenFile(dst, ...TRUNC...)`
  and stream into it. On any error before the rename, delete the temp and leave
  `dst` untouched.
- **Why:** `os.Rename` is the atomic swap primitive — a reader of `dst` sees
  either the complete old file or the complete new file, never a half-written
  one, and a crash mid-transfer cannot corrupt `dst`. The temp **must** be on the
  same filesystem: "You can't do an atomic rename across filesystem boundaries;
  using the same directory ensures both files are on the same filesystem"
  ([Michael Stapelberg, *Atomically writing files in Go*](https://michael.stapelberg.ch/posts/2017-01-28-golang_atomically_writing/), accessed 2026-06-28; see also [google/renameio](https://pkg.go.dev/github.com/google/renameio) and [natefinch/atomic](https://github.com/natefinch/atomic), accessed 2026-06-28).
- **How tested:** kill the transfer mid-stream (close the conn / `panic` after K
  bytes) and assert (a) `dst` is either absent or the previous complete version,
  never partial; (b) no leftover temp files; (c) a re-run completes the transfer.
  This is the roster's "a transfer killed mid-stream leaves no corrupt file (temp
  discarded)" acceptance.

## SR-2 — Durability ordering: fsync the temp file, rename, then fsync the parent directory

- **Rule:** sequence is: write temp → `tmp.Sync()` (flush file data+metadata) →
  `os.Rename(tmp, dst)` → `fsync` the **parent directory** (flush the rename
  itself). Close descriptors after their fsync.
- **Why:** "on POSIX, fsync is invoked on the temporary file after it is written
  ... and on the parent directory after the file is moved (to flush filename)";
  without it "the os.Rename() call [may] result in a 0-byte file" after a crash
  ([Stapelberg](https://michael.stapelberg.ch/posts/2017-01-28-golang_atomically_writing/), accessed 2026-06-28). Note Go's own caveat that "even within the
  same directory, on non-Unix platforms Rename is not an atomic operation" — so on
  Windows the rename-replace path needs the platform-appropriate replace
  semantics (handled in transport/reconcile; flagged in crossplatform-rules).
- **How tested:** unit test asserts the call order (via an injectable filesystem
  interface / spy); integration crash test as in SR-1.

## SR-3 — Apply is idempotent and content-addressed; re-applying the same update is a no-op

- **Rule:** before writing a received file, compare the incoming `content_hash`
  (and version vector) against the local `FileInfo`. If the local tree already
  has that exact content+version, **do nothing** (no write, no rename, no event).
  Applying the same update twice must converge to the same state with no side
  effects.
- **Why:** retries, reconnects, and overlapping index exchanges will redeliver
  updates. A non-idempotent apply re-writes files, which re-triggers the watcher
  (SR-8) and can ping-pong. Content-addressing makes "already have it" cheap and
  exact.
- **How tested:** deliver the same INDEX/RESPONSE twice; assert exactly one write
  occurs and zero outbound broadcasts result from the second.

## SR-4 — mtime/wall-clock is never the source of truth for ordering; version vectors are

- **Rule:** decide "who is newer / did these edits conflict" using **version
  vectors**, not mtimes. `mtime` is used **only** as a deterministic tiebreaker
  when version vectors say two edits are concurrent (SR-7).
- **Why:** version vectors "allow the participants to determine if one update
  preceded another (happened-before), followed it, or if the two updates happened
  concurrently" without a shared clock ([Version vector, Wikipedia](https://en.wikipedia.org/wiki/Version_vector), accessed 2026-06-28). Wall clocks on two
  laptops skew; trusting mtime for ordering is a classic sync data-loss source
  (the antipatterns-researcher track covers "clock skew sync conflict
  resolution"). Leaf shape: `docs/audit/decisions/phase0/merkle-leaf-shape.md`.
- **How tested:** table-driven VV tests: `dominates`, `dominated-by`,
  `concurrent`; assert a causally-newer edit with an *older* mtime still wins.

## SR-5 — Convergence: after changes settle, both peers expose the identical Merkle root hash

- **Rule:** the reconciliation algorithm must drive any two connected peers to
  **bit-identical root hashes** once propagation quiesces. The structural hash
  commits to content_hash + mode + deleted + version-vector (not raw mtime), so
  "converged" and "equal root" are the same statement (see leaf-shape decision).
- **Why:** this is the eventual-consistency oracle ("after a change settles, both
  trees expose the identical root hash" — plan/agent_roster.md, flow-verifier).
  Without it the engine cannot even *tell* it is done.
- **How tested:** the two-instance integration scenario: diverge two folders,
  let them sync, assert equal roots; also assert the O(log n) diff property —
  one byte changed flips exactly that leaf's branch and the root, nothing else.

## SR-6 — Only broadcast a hash after a *confirmed local* change

- **Rule:** an outbound index/hash broadcast happens **only** when the scanner
  confirms a genuine local modification (a settled watcher event or a rescan
  delta whose new content_hash differs from the recorded one), at which point the
  device bumps **its own** counter in that file's version vector. Receiving and
  applying a remote file **must not** bump our counter and **must not** broadcast.
- **Why:** this is the load-bearing half of the no-sync-loop invariant. If we
  broadcast on every tree change including remotely-applied ones, peer A's write
  → peer B applies → B broadcasts → A applies → A broadcasts → ∞. Tying the
  counter-bump to *local authorship only* means a received update carries the
  origin's version vector that already dominates ours, so re-announcing it is a
  no-op even if it leaked out (defence in depth with SR-3). Pinned in
  plan/README.md ("Sync-loop invariant: only broadcast hash after a local
  change") and the leaf-shape decision.
- **How tested:** apply a received file and assert **zero** outbound hash
  broadcasts (the flow-verifier's "a received file produced zero outbound hash
  broadcasts").

## SR-7 — No data loss on conflict: the loser is renamed to a conflict copy, never deleted

- **Rule:** when version vectors show two edits to the same path are **concurrent**
  (neither dominates) and the contents differ, keep **both**: the winner stays at
  the path, the loser is renamed to
  `<name>.sync-conflict-<UTC-date>-<UTC-time>-<deviceID>.<ext>` and that conflict
  copy then syncs as a normal file. The tiebreaker for which side is the loser:
  **older mtime loses; if mtimes are equal, the device with the larger device-ID
  value loses** (deterministic, so both peers independently pick the same loser).
- **Why:** "one of the files will be renamed to
  `<filename>.sync-conflict-<date>-<time>-<modifiedBy>.<ext>` ... The file with
  the older modification time will be marked as the conflicting file ... If the
  modification times are equal, the file originating from the device which has the
  larger value of the first 63 bits for its device ID will be marked as the
  conflicting file" — and conflict copies "are treated as normal files after they
  are created, so they are propagated between devices"
  ([Syncthing, *Understanding Synchronization*](https://docs.syncthing.net/users/syncing.html), accessed 2026-06-28). Renaming, not deleting, is what
  makes the no-data-loss contract literally true.
- **How tested:** edit the same file concurrently on two instances with differing
  content; assert convergence to **two** files (winner + one `.sync-conflict-*`),
  neither version's bytes lost, on **both** peers (roster: "simultaneous edits ...
  produce a .sync-conflict copy with neither version lost").

## SR-8 — A received file is not a "local change" until the tree rebuilds; break the watcher echo loop

- **Rule:** writing a received file via SR-1 will make fsnotify fire events for
  that path. The engine **must not** treat those self-induced events as a new
  local change. Mechanisms (use at least two, defence in depth):
  (a) after applying a received update, record the expected content_hash so the
  rescan/debounce sees "matches what I just wrote → no new authorship";
  (b) the rescan recomputes the tree and finds the new leaf already equals the
  received `FileInfo` (same content_hash + version vector), so no local counter
  bump and no broadcast (SR-6);
  (c) optionally suppress watcher events for a path during the brief apply window.
- **Why:** without this, every received file looks like a local edit and gets
  re-broadcast → sync loop / echo. fsnotify *will* surface the temp-create +
  rename as events ("a single write action ... may show up as one or multiple
  writes" — [pkg.go.dev/fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify), accessed 2026-06-28). Pinned in plan/README.md ("A received
  file is not a local change until the tree rebuilds").
- **How tested:** receive a file, let the watcher fire, assert zero outbound
  broadcasts and that the file is not re-queued as a local change.

## SR-9 — Deletions propagate as tombstones, not as silent disappearance

- **Rule:** deleting a path locally produces a **tombstone**: the `FileInfo` is
  retained with `deleted=true` and the deleting device's version-vector counter
  bumped (a delete is a versioned *event*). The tombstone propagates like any
  other update; a peer applying it removes its local copy. Never represent a
  delete as merely "the path is gone from my index" — absence is ambiguous.
- **Why:** "a tombstone ... is a lightweight, timestamped marker inserted to
  represent the deletion ... rather than immediately removing the underlying
  data, which allows for propagation of delete operations across replicas while
  maintaining eventual consistency" ([Streamkap / general tombstone semantics](https://streamkap.com/resources-and-guides/cdc-soft-deletes-tombstones); see also [Riak object deletion](https://docs.riak.com/riak/kv/latest/using/reference/object-deletion/index.html), both accessed 2026-06-28). Without a versioned
  tombstone, a delete cannot be distinguished from "not yet created" and cannot
  win/lose a conflict deterministically.
- **Conflict case:** a delete vs a concurrent modification is resolved by SR-7's
  rules; if the delete loses, the modified file survives; "If the conflict is
  between a modification and a deletion ... and the deletion wins ... the file is
  renamed to a conflict copy" ([Syncthing syncing docs](https://docs.syncthing.net/users/syncing.html), accessed 2026-06-28) — i.e. even a losing modification is
  preserved as a conflict copy (data-loss-free, consistent with SR-7).
- **How tested:** delete a file on instance A; assert B removes it and both
  retain a tombstone; delete-vs-edit concurrent test asserts the edited content
  survives as a conflict copy when the edit wins.

## SR-10 — A stale peer must not resurrect a deleted file

- **Rule:** when a peer that was offline during a deletion reconnects still
  holding the old file, the version-vector comparison must show the tombstone
  **dominates** that peer's stale version, so the file is deleted on the stale
  peer — **not** re-created on everyone else. Tombstones are retained long enough
  that no live peer can carry a pre-delete version the tombstone doesn't dominate
  (retention period is a logged Phase 2 sub-decision; for a 2-device LAN tool,
  retain until both peers have acknowledged, then GC).
- **Why:** "Consumers replaying old messages without applying tombstones can
  reintroduce deleted data"; safe tombstone removal "demands verification that no
  replica lag persists, which could lead to data resurrection if unrepaired nodes
  retain pre-deletion versions" ([Cassandra tombstone management](https://medium.com/@lkalapati.dba/tombstones-66a0d5ab2579), accessed 2026-06-28). This is *the* classic deletion bug:
  the file you deleted mysteriously comes back.
- **How tested:** partition a peer, delete the file on the other, reconnect the
  stale peer; assert the file is deleted on the stale peer and **not** resurrected
  on the deleter. Premature-tombstone-GC test asserts no resurrection.

## SR-11 — Watcher events are hints; periodic full rescan is the source of truth

- **Rule:** the filesystem watcher provides *hints* to act quickly. The authoritative
  state is produced by a full directory rescan + tree rebuild, run periodically
  and **on demand whenever the watcher signals overflow / error** (GR-9). Any
  divergence between watcher-derived state and a rescan is resolved in favour of
  the rescan.
- **Why:** OS watchers silently drop events under load — fsnotify surfaces
  `ErrEventOverflow` (Windows `ReadDirectoryChangesW` 64K buffer; inotify
  `IN_Q_OVERFLOW`) and "Notifications on network filesystems ... generally don't
  work" ([pkg.go.dev/fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify), accessed 2026-06-28). A watcher-only engine loses changes; the
  rescan guarantees we eventually notice everything. Pinned in plan/agent_roster.md
  ("events are hints, periodic full rescan is the source of truth").
- **How tested:** simulate an overflow / drop a synthetic event, then assert the
  periodic rescan still detects and converges the missed change.

## SR-12 — Framing has a hard max-length guard; malformed length never corrupts the stream

- **Rule:** the frame reader validates `0 < L <= MaxFrameLen` (16 MiB, Phase 0)
  **before** allocating, using `io.ReadFull` for both header and body. A frame
  that violates the bound terminates that peer connection with a typed error; it
  never desynchronises or wedges the reader.
- **Why:** unbounded length-prefix reads are a textbook OOM/DoS, and a
  length-prefix off-by-one "corrupts the stream" for every subsequent message
  (plan/agent_roster.md, protocol-critic). "One must include a maximum message
  size 'sanity check' in the socket reading code" ([Stephen Cleary, *Message Framing*](https://blog.stephencleary.com/2009/04/message-framing.html), accessed 2026-06-28).
  Full design: `docs/audit/decisions/phase0/framing-format.md`; Go idiom: GR-8.
- **How tested:** fuzz the 4-byte length; assert oversized → `ErrFrameTooLarge`
  + connection dropped + no large allocation; split a valid frame across reads
  (`iotest.OneByteReader`) and assert correct reassembly.

## SR-13 — Canonical identity is a forward-slash relative path; no OS separators in tree or wire

- **Rule:** every path stored in the tree, used as a map key, or sent on the wire
  is a forward-slash relative path in the project's canonical Unicode/case form
  (see crossplatform-rules). Convert to OS-native only at the filesystem call.
- **Why:** the same logical file must hash and key identically on Mac and
  Windows; a stored `\` or a denormalised Unicode name makes the same file look
  like two different leaves, breaking SR-5 convergence. Project hard rule
  (plan/README.md autonomy contract) + GR-12.
- **How tested:** round-trip a Windows-style input set Mac→wire→Windows→wire→Mac
  and assert identical canonical keys and identical subtree hashes.

---

## Invariant-to-acceptance map (for the planner & flow-verifier)

| Invariant | WS acceptance phrasing (plan/agent_roster.md) |
|---|---|
| SR-1, SR-2 | "a transfer killed mid-stream leaves no corrupt file (temp discarded)" |
| SR-5 | "two instances with divergent folders converge to identical root hashes" |
| SR-6, SR-8 | "receiving a file does not trigger a re-broadcast loop" |
| SR-7, SR-9 | "simultaneous edits to one file produce a .sync-conflict copy with neither version lost" |
| SR-10 | deletion propagation without resurrection (deletion scenario) |
| SR-11 | watcher-drop recovered by rescan (robustness scenario) |
| SR-12 | "malformed length is rejected without corrupting the stream" |
| SR-13 | "pathnorm round-trips a Windows-hostile name set without loss" |
