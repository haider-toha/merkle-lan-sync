# Decision: REV-FLAKE-1 real root cause — torn (Size, ContentHash) leaf from a non-atomic scan

- Area: phase7 / fix-agent (REV-FLAKE-1, fixed-claim-refuted)
- Date: 2026-06-29
- Status: accepted — implemented this round (see commit trailer in the finding)
- Supersedes the diagnosis in `docs/audit/decisions/phase6/convergence-timeout-deflake.md`
  (which framed the flake as a pure CPU-starvation / timeout-tuning artifact). The two
  Phase-6 skeptic votes (`REV-FLAKE-1.skeptic1.md`, `REV-FLAKE-1.skeptic-2.vote.md`)
  REFUTED the "FIXED" verdict, predicting the root cause persisted. They were right.

## Context

The Phase-6 finding (`phase6-convergence-timeout-flake.md`) claimed the integration
suite's `waitConverged` timeouts were a liveness artifact of the aggressive TEST config
(`RescanInterval 40ms`), fixable with larger budgets + relaxed rescan on the two
large-file scenarios. Both skeptics refuted "FIXED": the tiny-file
`TestConflict_NeitherVersionLostSymmetricName` still stalled at the *new* 30s budget
under oversubscription (skeptic #2 reproduced 5/12).

### What the reproduction actually showed (evidence)

Reproduced the skeptics' setup — 12-24 parallel `GOMAXPROCS=2` race binaries — and
instrumented the engine (`onIndex`/`resolve`/`materialise` Size+hash logging,
since removed). The failing node's trace:

```
DBG onIndex recv path="f.txt" size=0 hash=30542475 deleted=false
DBG resolve path="f.txt" kind=conflict winnerOrInstallSize=0
DBG materialise-fetch path="f.txt" size=0 hash=30542475 nblocks=0   (× ~78k, infinite retry)
fetch "f.txt" ... reconstructed content hash mismatch: got e3b0c442… (SHA-256 of "") want 30542475…
```

The peer advertised a leaf with **Size=0 but ContentHash = hash("v2-from-B")** — an
internally impossible leaf. The receiver computes `numBlocks(0)=0`, reconstructs the
empty file, fails the verify-before-rename, and retries forever ⇒ the pair never
converges ⇒ the 30s "timeout." It is NOT slowness; it is a permanently-stuck state a
bigger budget can never clear (exactly why 20s→30s did not help — skeptic #2 §1).

### Mechanism (the real defect)

1. `merkle.Scan` (scanner.go) and `Engine.scanOne` (engine.go) build a leaf with
   `ContentHash` from `HashFile` (a full read pass) and `Size` from a SEPARATE
   `DirEntry.Info()` / `os.Lstat` stat. The two reads are not atomic. A file rewritten
   between them yields a torn leaf: Size from one file-state, hash from another. Tests
   `write()` a file while the 40ms rescan runs, so the window is hit under load; in
   production a user saving a file during a rescan hits the same window.
2. The rescan / onLocalChange "unchanged" comparison is `prev.ContentHash == cur.ContentHash
   && prev.Type == cur.Type` (engine.go:1197, :1160) — it ignores Size. So once a torn
   `{Size:0, hash:H}` leaf is recorded, every later rescan sees the same hash, declares
   "unchanged," and NEVER corrects the Size. The bad leaf is broadcast and is permanent.

A standalone deterministic regression test (`internal/merkle/scan_torn_test.go`) flips a
file between a 9-byte and a 64-KiB body while `Scan` runs and catches the torn leaf
within ~10 iterations pre-fix.

A SECOND, independent harness defect surfaced in the same repro: `TestRename_*` fail
FAST (~1.6-2.8s) with "new.txt missing" AFTER `waitConverged` returned. Those tests
mutate node A (`os.Rename`) and immediately call `waitConverged` with no intervening
`waitRootChanged`, so under load — before A's rescan has even detected its own rename —
`waitConverged` observes the STALE pre-mutation converged state (both nodes still at the
old root) and returns a false positive. This is an oracle gap, not an engine bug, but it
must be closed for the suite to be honestly de-flaked.

## Options (scored: correctness / concurrency-safety / testability / cross-platform)

### A. Self-consistent leaf: derive Size from the bytes streamed through the hasher
Add `merkle.HashFileSize` (returns digest + bytes hashed via `io.Copy`'s count) and use
it for regular-file leaves in `Scan` and `scanOne`, so `Size` is ALWAYS the exact byte
count that produced `ContentHash`. Add `Size` to the change-detection "unchanged"
compare (rescan + onLocalChange) so any pre-existing/odd inconsistency self-heals on the
next scan. Fix the harness `waitConverged` TOCTOU by waiting for the mutating node's root
to move (`waitRootChanged`) before asserting convergence in the rename tests.
- correctness: **high** — the leaf becomes internally consistent at construction; a
  concurrent rewrite now yields a consistent (possibly-stale) snapshot that the hash-based
  change detection corrects on the next scan. Transfer can never see Size⊥hash again.
- concurrency-safety: **high** — no new shared state, no lock changes; size now comes
  from the same read as the hash (one `os.Open`), eliminating the stat/read race.
- testability: **high** — deterministic merkle stress guard reproduces the torn leaf
  pre-fix and passes post-fix; the oversubscription integration repro is the end-to-end
  proof.
- cross-platform: **high** — `info.Size()` vs hashed-bytes can also diverge on Windows
  (a writer extends a file between `ReadDirectoryChangesW`-driven stat and the read);
  deriving size from the hashed bytes is OS-independent and never stores anything
  OS-specific.

### B. Concurrent-modification detection (rsync/borg style): stat-before + stat-after
Stat the file, hash it, stat again; if size/mtime changed, the read was torn ⇒ retry a
bounded number of times, else skip the leaf this scan.
- correctness: medium-high — also catches torn *content* (not just torn size). But a
  steadily-rewritten file could exhaust retries and be skipped, delaying convergence; and
  it does not by itself make Size==hashed-bytes, so the change-detection Size-blind spot
  (defect #2) remains unless also fixed.
- concurrency-safety: high. testability: medium (retry timing is itself racy to test).
  cross-platform: high. More code, more corner cases than A; A subsumes the observed bug.
  Kept as a noted future hardening for torn-*content* reads, not needed for this defect.

### C. Engine-only self-heal: add Size to the change compare, leave Scan as-is
- correctness: **low/medium** — a torn leaf is still broadcast and served at least once,
  and self-heal depends on a later scan happening to read consistently; under a steady
  writer it can stay torn. Does not fix the construction defect. Rejected as the primary.
  (Adopted only as the cheap defense-in-depth half of A.)

### D. Test-only de-flake: relax RescanInterval to 1s for all tests + raise budgets
This is the Phase-6 approach the skeptics already refuted. It hides a real engine
data-/liveness bug behind test config and a bigger timeout; production (a user saving a
file during a rescan) still mints an un-transferable leaf that never self-corrects.
- correctness: **unacceptable** — papers over a production defect. Rejected.

## Decision

**Option A** (with C as its defense-in-depth second half). Concretely:

1. `internal/merkle/hash.go`: add `HashFileSize(osPath) (digest [32]byte, size int64, err error)`
   that returns the byte count `io.Copy` streamed through SHA-256. Keep `HashFile` for the
   hash-only caller (`localSource`).
2. `internal/merkle/scanner.go` (`Scan`) and `internal/reconcile/engine.go` (`scanOne`):
   set a regular file's `Size` from `HashFileSize`'s count, NOT from `info.Size()`.
3. `internal/reconcile/engine.go`: include `Size` in the "unchanged" comparison in
   `rescan` and `onLocalChange`, so any inconsistent leaf is re-hashed + re-broadcast.
4. `test/integration/sync_test.go`: in the rename tests, capture A's pre-rename root and
   `waitRootChanged` before `waitConverged`, closing the stale-state oracle TOCTOU.
5. Re-run the 12-24× `GOMAXPROCS=2` race oversubscription repro as end-to-end evidence.
   Only if a *genuine* (non-stuck) slowness remains will the small-file rescan be relaxed;
   the root-cause fix is expected to remove the stuck state that no budget could clear.

Production defaults are unchanged. No OS-specific data is stored; canonical leaves stay
forward-slash + content-hash addressed.

## Rationale

The skeptics were correct: the finding mislabeled a real bug as a tuning artifact. The
true defect is a non-atomic (Size, ContentHash) construction plus a Size-blind change
filter, which together can mint a permanently un-transferable leaf — a convergence
(liveness) failure and, in the limit, an advertised file a peer can never reconstruct.
Deriving Size from the hashed bytes makes the leaf self-consistent by construction and is
the smallest change that removes the stuck state at its source rather than widening the
budget around it. The harness TOCTOU is a separate, genuine oracle gap the same repro
exposed; fixing it is required for an honest "the suite is de-flaked" claim.

## Consequences

- A leaf's `Size` is now guaranteed to equal the byte length of the content that hashes
  to its `ContentHash`. `numBlocks(Size)` and the transfer range math are always valid.
- A file changed mid-scan yields a consistent stale snapshot (old or new), corrected on
  the next scan via the now-Size-aware change detection — never a stuck hybrid.
- Symlink leaves are unaffected (their Size already equals the hashed target length).
- The merkle regression guard documents the invariant; the integration repro is the
  liveness proof. If residual genuine slowness is observed, the small-file rescan relax is
  the documented follow-up (not expected to be needed once the stuck state is gone).

---

## SECOND root cause (the large-file backpressure residual): a lost-on-connect INDEX permanently wedges

After the torn-scan fix, the small-file flakes vanished but the large-file
`TestBackpressure_BidirectionalConverges` still failed ~1/12-1/24 binaries at its 60s
budget. This is the residual the Phase-6 finding disclosed and the skeptics flagged
(skeptic #1 §1: "a known residual stall reproduces"). It is NOT slowness.

### Evidence (goroutine dump + index-exchange trace under 24× GOMAXPROCS=2)

Captured a wedge with a 300s-budget debug variant + `runtime.Stack` dump:

- ALL goroutines parked in `[select]` (both engines' Run loops, both pullLoops, both
  serveLoops, transport read/write loops). No lock contention, no blocked send, no
  in-flight fetch. The system is QUIESCENT but DIVERGED — A is missing bigB, nothing is
  retrying.
- Per-rescan reconcile trace: `reconcile peer=<B> peerIndexSize=0 localSize=1 diffs=1`
  repeated every second — A's stored index of B is EMPTY, so A never plans a bigB fetch.
- Index-exchange trace: both engines logged `onPeerConnected SEND-INDEX` (both SENT their
  INDEX), but only ONE `onIndex` was received (B got A's). A never received B's INDEX, yet
  the connection stayed alive.

### Mechanism

`Conn.newConn` (conn.go) starts `readLoop` immediately; `register` (transport.go) emits
`PeerConnected` only AFTERWARD. Both `deliver` (a PeerMessage) and `emit` (PeerConnected)
feed the same fan-in `events` channel from different goroutines. Under load, A's readLoop
can `deliver` B's INDEX onto `events` BEFORE A's `register` emits `PeerConnected`. The
engine's `handleMessage` does `ps := e.peerByDevice(id); if ps == nil { return }` — so an
INDEX that arrives before the peer is registered (addPeer runs in onPeerConnected) is
SILENTLY DROPPED. INDEX is exchanged only ONCE on connect with no re-exchange, so A's view
of B stays empty forever ⇒ A never fetches bigB ⇒ permanent one-directional wedge that
looks like a timeout. (Both directions are symmetric; whichever side loses the race wedges.)

### Options (scored: correctness / concurrency-safety / testability / cross-platform)

#### A. Transport ordering gate: no PeerMessage delivered before PeerConnected is emitted
Add `Conn.registered chan struct{}`, closed by `register` right after it emits
`PeerConnected`; `deliver` waits on it (or `closed`/`t.closed`) before pushing a
PeerMessage to `events`. Guarantees the engine registers the peer before any of that
peer's messages arrive — the race cannot occur.
- correctness: **high** — eliminates the drop at its source; FIFO `events` + emit-then-close
  ordering makes PeerConnected strictly precede every PeerMessage from that conn.
- concurrency-safety: **high** — no goroutine-lifecycle reorder (readLoop/writeLoop/supervisor
  unchanged, wg accounting unchanged); deliver also selects on `closed`/`t.closed` so a
  conn that errors before register never hangs the reader.
- testability: **high** — the 24× oversubscription repro reproduced the wedge pre-fix; it
  must be clean post-fix.
- cross-platform: **high** — pure Go channel ordering, no OS specifics.

#### B. Engine-side lazy register: in handleMessage, addPeer from ev.Conn if peerByDevice==nil
- correctness: medium — onPeerConnected does more than addPeer (SR-5 short-circuit + send
  INDEX); duplicating/reordering that on a message path is error-prone and easy to drift
  from the canonical connect path. Rejected.

#### C. Periodic INDEX re-exchange on rescan (self-heal ANY index loss)
Re-send the local INDEX to each connected peer on the rescan tick. INDEX is idempotent
(onIndex updates ps.index + reconciles; it is NOT a broadcast, so the PR-6
OutboundIndexUpdates oracle is unaffected). Heals a lost INDEX within one rescan interval.
- correctness: high (self-healing), but it is a periodic band-aid OVER the race rather than
  a fix AT the source, and adds steady-state INDEX bandwidth. Kept as a noted defense-in-
  depth follow-up; not required once A removes the race deterministically.

### Decision

**Option A** — the transport ordering gate. It deterministically removes the
message-before-PeerConnected race that drops the INDEX, which is the actual defect. It is
minimal, has no steady-state cost, and is verified by the same oversubscription repro that
exposed the wedge. Option C is recorded as an available hardening if a future loss cause
(e.g. an outbound shed that closes the conn with no reconnect) is observed, but is not
implemented now to avoid steady-state index churn and keep the change surgical.

### Consequences

- The engine is guaranteed to see `PeerConnected` (and register the peer) before any
  `PeerMessage` from that connection, so the one-shot INDEX is never dropped as
  "unknown peer." The bidirectional backpressure transfer can no longer wedge from a
  lost-on-connect INDEX.
- `deliver` now blocks until the conn is registered or closed; a conn closed before
  register (transport shutting down) unblocks the reader via `closed`, so no reader hangs.
- No protocol/wire change; no OS-specific behavior; the PR-6 broadcast-count oracle is
  untouched (INDEX is not an INDEX_UPDATE).
