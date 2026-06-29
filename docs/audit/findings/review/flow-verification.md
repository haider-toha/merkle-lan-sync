# Flow verification — end-to-end system invariants (Phase 6)

- Verifier: flow-verifier (Phase 6, whole-system oracle)
- Date: 2026-06-29
- Toolchain: go1.26.4, darwin/arm64 (module declares go 1.23.0)
- Method: quiesce-then-compare (CDD-8 — the equal-root oracle holds AT quiescence,
  never at every instant; sync-rules.md SR-5 amendment)
- Disposition rationale: `docs/audit/decisions/phase6/flow-verification-disposition.md`
- Scope: the four end-to-end invariants in `.claude/agents/flow-verifier.md`. This is
  the SYSTEM-level oracle; unit-level proofs are the implementers' job and are cited
  here as supporting, not primary, evidence.

## Verdict summary

| # | Invariant | Rules | Verdict |
|---|---|---|---|
| 1 | Eventual consistency — both trees expose the identical root hash at quiescence | SR-5 | **PASS** |
| 2 | No data loss — every conflict left a recoverable copy; loser renamed, never deleted | SR-7, SR-9 | **PASS** |
| 3 | No sync loop — a received file produced zero outbound hash broadcasts | SR-6, SR-8 | **PASS** |
| 4 | Clean goroutine shutdown on peer loss / context cancel — no leak | GR-3 | **PASS** |

**All four PASS. No release blocker.** Two non-blocking defense-in-depth
recommendations for Phase 7 are recorded at the end (R1, R2).

Every verdict cites: (a) the production mechanism by `file:line`, (b) a green
`-race` test, (c) an evidence-generator run log, and (d) a fresh live re-run in this
session. All live re-runs below were executed under `-race -count=1` on 2026-06-29.

---

## Invariant 1 — Eventual consistency (SR-5): identical root hash at quiescence

**Why "equal root ⇔ converged" is a sound oracle.** The structural (tree) hash
commits to exactly the replicated fields and excludes machine-local ones. Leaf
encoding = `ContentHash || canonicalModeByte || deleted || versionVector`
(`internal/merkle/codec.go:30-42`); raw `mtime` and `size` are deliberately
**excluded**. A directory node hashes the name-sorted `(name, childHash)` list
(`codec.go:56-74`, `internal/merkle/node.go:56-74`). Consequently two peers holding
identical `FileInfo` sets (identical VVs and tombstones) produce **bit-identical**
roots, and the VV-in-hash means equal bytes with *different history* are correctly
NOT reported converged until the vectors merge. This is the foundation that makes
the oracle meaningful (SKILL §1).

**Convergence machinery.** `Engine.RootHash()` is the oracle accessor
(`internal/reconcile/engine.go:257-262`). The diff prunes equal subtrees and
recurses only mismatching branches (`reconcileWithPeer`, `engine.go:632-655`;
`merkle.Diff`). On connect, when the peer's HELLO root already equals ours the
INDEX exchange is skipped (`onPeerConnected`, `engine.go:428-429` — the SR-5
short-circuit). The total, pure resolver drives both peers to the same verdict for
the same inputs (`internal/reconcile/apply.go:51-84`).

**Oracle discipline (CDD-8).** `waitConverged` polls until the roots are
bit-identical AND stay identical across a 5x20ms settle window before passing
(`test/integration/helpers.go:124-145`) — a transient crossing does not satisfy it,
and a genuine non-quiescence (e.g. a sync loop) times out.

**Evidence.**
- Test: `TestTwoNode_Converge` — two divergent folders converge to equal roots, every
  file present byte-exact on both sides (`test/integration/sync_test.go:14-42`).
- Run log: `docs/audit/runs/scenario-convergence.log` (PASS); real two-daemon
  multicast convergence in `docs/audit/runs/two-process-demo.log` ("RESULT:
  CONVERGED").
- Live re-run (this session): `go test ./test/integration -run ^TestTwoNode_Converge$
  -race` → `--- PASS (0.18s)`; re-run `-count=3` of the convergence+conflict pair →
  `ok ... 2.626s` (no flake).
- Flake disposition: `docs/audit/findings/review/REV-FLAKE-1.md` — the only observed
  flake was a TEST-harness CPU-starvation timeout, NOT a correctness/liveness defect;
  the correctness oracle is timeout-independent and was never affected.

**Verdict: PASS.** Convergence to an identical root hash at quiescence is proven by a
green `-race` two-engine test, an independent two-daemon multicast run, and the
hash-composition that makes the oracle exact.

---

## Invariant 2 — No data loss on conflict (SR-7, SR-9): loser renamed, never deleted

**Mechanism.** A concurrent edit with differing content resolves to `planConflict`
(`apply.go:76-83`). `conflictPlan` keeps BOTH: the winner stays at the path with the
merged VV; the loser becomes a deterministic `.sync-conflict-<UTC-date>-<UTC-time>-
<authorHex>.<ext>` copy that then syncs as a normal file
(`internal/reconcile/conflict.go:86-93`, `apply.go:93-113`). The winner/loser choice
is a TOTAL, COMMUTATIVE function of intrinsic replicated fields only
(`aWins`/`winner`/`loserOf`, `conflict.go:30-56`) — never "local vs remote" — so both
peers independently mint the byte-identical copy name (timestamp truncated to whole
seconds for Mac-ns/Windows-FAT agreement, `conflict.go:90`). Crucially, the engine
enqueues the **loser copy BEFORE the winner** through the FIFO per-peer puller
(`execute`, `engine.go:662-686`), so the loser's still-on-disk bytes are copied to
the conflict path *before* the winner overwrites the original path — no window where
the loser's bytes are unreachable. Deletes are tombstones, not silent absence
(`applyTombstone`, `engine.go:737-750`; `SetDeleted`), and a delete-vs-edit that the
delete loses preserves the edited file as a conflict copy (SR-9 via the same
`planConflict`).

**Evidence.**
- Test: `TestConflict_NeitherVersionLostSymmetricName` — after concurrent divergent
  edits, BOTH peers hold exactly `f.txt` (winner) + one identically-named
  `.sync-conflict` copy (loser); the union of bytes equals both original versions;
  the copy filename is byte-identical across peers (`sync_test.go:46-87`).
- Test: `TestDeletion_NoResurrection` — tombstone propagates; a stale peer applies the
  delete and the deleter does not resurrect (`sync_test.go:92-119`).
- Test: `TestRename_PropagatesNoLoss` — rename = create-new + delete-old, ordered
  creates-before-deletes (`broadcast.go:42-49`), both peers converge to the new path
  with original bytes, old path gone, nothing lost (`sync_test.go:160-191`).
- Run logs: `scenario-conflict.log`, `scenario-deletion.log`, `scenario-rename.log`
  (all PASS).
- Live re-run: integration suite `-race` → `TestConflict_... --- PASS (0.24s)`,
  `TestDeletion_... --- PASS (0.21s)`, `TestRename_... --- PASS (0.33s)`.

**Verdict: PASS.** Every conflict leaves a recoverable, deterministically-named copy
on both peers; the loser is renamed, never deleted; deletions are versioned
tombstones that resist resurrection.

---

## Invariant 3 — No sync loop (SR-6, SR-8): a received file ⇒ zero outbound broadcasts

**Mechanism.** `broadcastUpdate` is the ONLY outbound-index path and is called only
from confirmed-local-authorship sites — `onLocalChange` and `rescan`
(`broadcast.go:51-78`; call sites `engine.go:876` and `engine.go:934`). Applying a
received file goes through `handleCompletion`, which updates `files` + `expected` and
rebuilds but **does not broadcast** (`engine.go:793-817`). Three independent guards
break the watcher echo:
1. **Content-identity filter** — a re-hash equal to the recorded content is an apply
   echo, not authorship: `onLocalChange` returns without bump/broadcast
   (`engine.go:861-869`); same in `rescan` (`engine.go:899-905`).
2. **In-flight-apply guard** — during the brief atomic-rename→completion window
   `inflightLocked` suppresses authorship (`engine.go:693-700`, consulted at
   `engine.go:837-839` and `engine.go:906-908`) — SR-8 guard (c).
3. **VV authorship rule** — receiving uses `Merge`, never `Bump`; only a genuine
   local edit bumps our counter (`onLocalChange`, `engine.go:870`), so even a leaked
   re-announce of a received leaf is `Equal`/idempotent (SR-3, defense in depth).

**Bounded, loop-free exception (documented, not a violation).** A received
**conflict-copy loser** is re-advertised exactly once
(`handleCompletion` `c.advertise`, `engine.go:804-806`), and only by the side that
already holds the loser's bytes (`execute` conflict branch, `engine.go:677-679`).
This is required by SR-7 ("the copy syncs as a normal file") and is authorship of a
NEW path carrying a fixed VV, so the peer's apply is `Equal`/idempotent and never
re-broadcasts — it cannot sustain a loop. The plain-received-file case (the
invariant's subject) advertises nothing.

**Evidence.**
- Test: `TestApply_ZeroOutboundBroadcasts` — an apply echo on a recorded path produces
  **0** outbound `INDEX_UPDATE`; a genuine differing edit produces **exactly 1** and
  bumps our counter on top of the peer's history (`reconcile_test.go:444-477`). This
  asserts BOTH halves in one place, so a naive "mute the watcher after every apply"
  would fail the second assertion.
- Test: `TestApply_IdempotentRedelivery` — re-materialising present content sends 0
  REQUESTs (`reconcile_test.go:479-504`).
- System-level corroboration: a sync loop is non-quiescence; `waitConverged`'s settle
  window (`helpers.go:130-140`) would never be satisfied and every convergence
  scenario would time out. They all pass and quiesce in <1s — no loop.
- Prior reviewer verdict: `docs/audit/findings/review/PR-6.md` (FIXED).
- Live re-run: `TestApply_ZeroOutboundBroadcasts --- PASS`,
  `TestApply_IdempotentRedelivery --- PASS` under `-race`.

**Verdict: PASS.** A received file produces zero outbound hash broadcasts; the single
conflict-copy advertisement is a bounded, intentional, loop-free exception.

---

## Invariant 4 — Clean goroutine shutdown on peer loss / context cancel (GR-3)

**Mechanism (transport).** Each `Conn` owns exactly two goroutines (reader + writer)
under a `sync.WaitGroup`, torn down by an idempotent `sync.Once` close that both
unblocks the reader (`tlsConn.Close()`) and stops the writer (`close(c.closed)`)
(`internal/transport/conn.go:71-93`). `superviseConn` `Wait`s both then emits
`PeerDisconnected` and deregisters per-peer state IMMEDIATELY on a single-peer drop
(`transport.go:250-255`) — not only on global shutdown. `Send` never blocks
(buffered-with-shed), so a stuck peer cannot wedge a writer or the engine loop
(`conn.go:55-65`). Global teardown cancels ctx, closes listeners, and closes every
conn, then `wg.Wait()` (`transport.go:184-211`).

**Mechanism (engine).** Each peer gets a child context derived from `runCtx` with its
own `pullLoop`/`serveLoop` under `ps.wg` (`addPeer`, `engine.go:439-459`). On
`PeerDisconnected` the engine `removePeer`s: `ps.cancel()` then reaps `ps.wg.Wait()`
OFF the loop (`engine.go:461-474`); both loops also select on `ps.conn.Done()` so a
transport drop unblocks them even before `removePeer`. On context cancel `Engine.Run`
calls `shutdownPeers()`, closes the watcher, and `e.wg.Wait()`s every owned goroutine
(dial/debounce/watcher/peer-reapers) before returning (`engine.go:344-350`).

**Design provenance.** The single-peer-disconnect leak risk was raised as
`concurrency-critic-3` (status: rejected — the design already prescribed the
close→unblock→cancel→Wait handshake in GR-3); the implementation realises exactly
that handshake, verified below.

**Evidence.**
- Test (dedicated, transport): `TestConnChurn_NoGoroutineLeak` — 15 connect/disconnect
  cycles, then `runtime.NumGoroutine()` must return to the pre-churn baseline
  (`transport_test.go:622-691`, helpers `:735-771`). This is the GR-3 leak oracle.
- Test: `TestConn_CtxCancelTearsDown` — context cancel (no explicit Close) reaps to
  baseline (`transport_test.go:710-730`).
- Test: `TestTransport_CloseIsIdempotent` — concurrent multi-Close, no panic
  (`transport_test.go:695-706`).
- System-level corroboration (engine): `startNode` cleanup does `<-n.done`, which
  closes only after `Engine.Run` returns, and `Run` returns only after `e.wg.Wait()`
  (`helpers.go:105-106`, `engine.go:348`). A single leaked engine goroutine ⇒ `Run`
  blocks ⇒ the integration suite hits the test timeout. The suite completes in ~2.7s,
  so no engine goroutine leaks on cancel.
- Run log: `docs/audit/runs/race-all.log` — all packages `ok` under
  `-race -shuffle=on`, including `transport` and `test/integration`.
- Live re-run: `TestConnChurn_NoGoroutineLeak --- PASS (0.23s)`,
  `TestConn_CtxCancelTearsDown --- PASS (0.11s)`,
  `TestTransport_CloseIsIdempotent --- PASS` under `-race`.

**Verdict: PASS.** Per-peer reader/writer goroutines and engine per-peer loops are
reaped on single-peer disconnect and on context cancel; `NumGoroutine` returns to
baseline (dedicated transport test) and the engine's terminating `wg.Wait()` is
confirmed by the integration cleanup that would otherwise hang.

---

## Cross-cutting confirmations

- Full `-race` matrix command (`go test ./... -race -shuffle=on -count=1`) is green:
  `docs/audit/runs/race-all.log` (all 7 packages `ok`).
- Windows cross-compile clean (`GOOS=windows GOARCH=amd64 go build ./cmd/msync`):
  `docs/audit/runs/windows-cross-compile.log` (exit 0, PE32+ artifact). Note: this
  closes only the COMPILE gap; real NTFS case-collision / NFD / reserved-name
  behaviour is owned by the CI `windows-latest` job and
  `docs/audit/CROSS_PLATFORM_CHECKLIST.md`, outside these four invariants.
- Killed-transfer safety (SR-1/SR-2), which underpins "no data loss" during transfer:
  `TestKilledTransfer_NoCorruptFileThenRecovers` (`sync_test.go:198-248`) +
  `atomicWriteVerify` verify-before-rename (`transfer.go:72-114`) — dst never partial,
  no leftover temp, recovers byte-exact. Live re-run PASS.

## Non-blocking recommendations for Phase 7 (defense in depth — NOT release blockers)

These re-cover invariants already proven by other green, `-race`-clean evidence;
they tighten the dedicated coverage at the system layer. Rationale and options:
`docs/audit/decisions/phase6/flow-verification-disposition.md`.

- **R1 (Invariant 4):** add an ENGINE-level `runtime.NumGoroutine()` baseline
  assertion across peer connect/disconnect churn while `Engine.Run` stays live
  (the transport has this; the engine's reaping is currently proven indirectly).
- **R2 (Invariant 3):** lift the zero-outbound assertion to the integration layer —
  drive a real two-engine apply and assert the receiver emits zero `INDEX_UPDATE`
  frames (today the dedicated assertion is the single-engine
  `TestApply_ZeroOutboundBroadcasts`; the two-engine proof is currently the indirect
  quiescence-stability of `waitConverged`).

If either recommendation, once implemented, ever fails, that is a genuinely failing
invariant and a release blocker per the flow-verifier contract.
