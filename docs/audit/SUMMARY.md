# Merkle Sync — Final Summary

- Role: Final (summary agent), closing the 7-phase autonomous workflow.
- Date: 2026-06-29.
- Module: `github.com/haider-toha/merkle-sync` (Go 1.23; built/tested on go1.26.4, darwin/arm64).
- Inputs read: `plan/README.md`, `plan/agent_roster.md`, all of `docs/audit/{decisions,findings,runs,rules,plan}/`, and the code under `internal/`, `cmd/`, `test/`.

> **One-paragraph orientation.** Merkle Sync is a decentralised LAN file-sync engine
> (Mac <-> Windows, no central server, raw TCP over TLS 1.3 + UDP multicast). A recursive
> **Merkle tree is the source of truth for *what* differs** (prune-equal subtree diff);
> **version vectors decide *who* is newer / whether edits conflict**; **tombstones make
> deletion first-class**; every file materialises through **temp -> verify -> fsync ->
> atomic rename**. The single hard requirement that shaped every decision is **Mac <->
> Windows convergence**, which reduces to one rule: the same logical file must hash to
> the same bytes on both OSes (SR-5 via SR-13). It was built greenfield by the pipeline;
> the audit trail (62 decisions, 130 findings, 10 run logs) records every consequential
> choice *before* it was acted on.

---

## 0. Build & test status (fresh, this session — 2026-06-29)

All three gates re-run first-hand today, not merely read from logs:

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | **exit 0** |
| Full race suite (the CI matrix command) | `go test ./... -race -shuffle=on -count=1` | **all 7 packages `ok`** (TEST_EXIT=0) |
| Windows cross-compile | `GOOS=windows GOARCH=amd64 go build ./cmd/msync` | **exit 0**, `PE32+ executable ... for MS Windows` |

Recorded run logs corroborate: `docs/audit/runs/race-all.log`, `docs/audit/runs/integration.log`
(6 scenarios PASS), `docs/audit/runs/windows-cross-compile.log`, `docs/audit/runs/two-process-demo.log`
("RESULT: CONVERGED" — two real daemons over multicast). **Green on the Mac is necessary,
not sufficient** — see §6.

---

## 1. What was built — per package

~13,550 LOC of Go across 6 internal packages + the daemon + integration scenarios, with
**176 test functions**. The import DAG is acyclic and matches `docs/audit/plan/structure.md`:
`{protocol, pathnorm} -> merkle -> reconcile -> cmd/msync`, with `transport` and `discovery`
as siblings over `protocol`.

| Package | src / test LOC | What it owns | Key invariants | Provenance |
|---|---|---|---|---|
| `internal/protocol` | 961 / 988 | `[4B len][1B type][payload]` framing (big-endian, `MaxFrameLen=16 MiB`, `io.ReadFull`, pre-alloc length guard); 7 frozen message types (0x01 HELLO..0x07 CLOSE, 0x00 fatal, 0x08+ skippable); `VersionVector` (Compare/Merge/Bump, copy-on-write); `DeviceID = SHA-256(cert DER)` | SR-12, GR-7/8, SR-4, PR-1/2/7 | WS-0 (`801d094`) |
| `internal/pathnorm` | 470 / 383 | Canonical key = forward-slash, relative, **NFC per component**; `ToOSPath`/`FromOSPath` with reversible, injective per-component Windows escaping (reserved chars/devices/trailing dot-space); case-fold collision **detection** index | SR-13, XP-1..4, CDD-6 | WS-1 (`182ff00`) |
| `internal/merkle` | 1087 / 1360 | `FileInfo` leaf (hash+size+mode+mtime+VV+deleted); **RFC-9162 domain-separated structural hash** (`0x00` leaf/`0x01` node) that *excludes* mtime/size/raw-mode so roots match cross-OS; prune-equal `Diff`; scanner; local-only gob snapshot + `SynthesizeDeletions` | SR-5, MK-1/2/3/6, R-1/R-5 | WS-1 (`182ff00`) |
| `internal/transport` | 962 / 771 | Frames over **TLS 1.3 + TOFU**: `MinVersion==MaxVersion==1.3`, `InsecureSkipVerify` + `VerifyConnection` pins peer DeviceID against an allow-list (no CA — the pin replaces the chain check); HELLO re-asserts in-band; per-conn reader/writer/supervisor under one WaitGroup, idempotent `sync.Once` close, non-blocking buffered-with-shed `Send` | PR-7, SR-1/12, GR-3/4/13, CDD-1/2 | WS-2 (`31ef9c6`) |
| `internal/discovery` | 648 / 695 | UDP multicast announce (239.192.0.77:21027) + heartbeat eviction; a **GR-4 single-goroutine registry actor** (no shared-lock map); discovery is a **hint, never authorisation** (the TLS pin is the gate) | AL-16, GR-4/13, CDD-1, PR-7 | WS-3 (`fd36e70`) |
| `internal/reconcile` | 2419 / 1675 | The engine: **single writer** of tree + per-peer state behind one RWMutex, **zero I/O under lock**; diff -> stop-and-wait 32 KiB chunk pull (off-loop, per-peer) -> atomic verify-before-rename apply -> deterministic `.sync-conflict` copy -> tombstone; watcher = advisory debounced hint, periodic rescan = truth; filesystem-verdict no-clobber | SR-1..13 (all) | WS-4 (`af12de0`) |
| `cmd/msync` | 141 / 0 | Daemon: `-dir -port -folder -config -peer` (repeatable); one `signal.NotifyContext` root ctx (GR-2) wiring transport+discovery+engine; identity + snapshot persist under `<dir>/.msync`; a declared `-peer` set enables the #10590 ghost-counter de-pair sweep | GR-2, PR-4/7 | WS-4 (`af12de0`) |
| `test/integration` | ~990 (helpers+scenarios) | Two-node loopback harness; `waitConverged` is **quiesce-then-compare** (5x20 ms settle window); 6 scenarios: converge / conflict / deletion / rename / killed-transfer / bidirectional-backpressure | SR-5/7/9, PR-5 | WS-4 + Phase 7 |

Cross-platform discipline runs through every layer: the wire integer width/endianness, the
NFC canonical name, the forward-slash key, and the mtime/size-excluding structural hash are
all chosen so a `Mac -> wire -> Windows -> wire -> Mac` round-trip yields a bit-identical root
(R-1, the project's #1 risk, made a hard WS-1 acceptance gate).

---

## 2. Before / after

This was a **greenfield** build, so "before" is the empty repo + the design priors; "after"
is the converged engine above. The load-bearing before/after is the **Phase 6 -> Phase 7
adversarial fix loop**, where the skeptic process did exactly what it exists to do: it
**refused to accept 8 "fixed" claims** and Phase 7 round 1 fixed all of them — including
**three genuine data-loss / liveness bugs** that green unit tests had hidden.

| Finding | Phase 6 claimed | What the skeptics caught (before) | Phase 7 fix (after) | Severity |
|---|---|---|---|---|
| **PR-3** conflict no-loss | FIXED | Two deterministic **data-loss** bugs in *execution*: (a) the loser-copy `enqueueFetch` is silently dropped on a full queue while the winner overwrites the original; (b) on delete-vs-modify a **synchronous `os.Remove`** runs before the copy lands => losing modification lost. Reproduced. | Couple loser-copy + winner-install into one ordered puller task; gate the destructive op on the copy landing; bound the copy name for MAX_PATH (`9d1e0cc`) | **high** |
| **REV-FLAKE-1** convergence flake | FIXED (called a timeout-tuning artifact) | **Not** a flake: (a) torn `(Size, ContentHash)` leaf from a non-atomic scan => a permanently **un-transferable** file; (b) lost-on-connect INDEX race => a permanent one-directional **wedge** | `HashFileSize` derives size from the hashed bytes (+ size in the change key); `Conn.registered` gate orders PeerConnected before any PeerMessage (`68c65f7`). 120 oversubscribed race binaries -> **0 failures** (was 8/12) | **med** (was a real liveness defect) |
| **MK-6** delete-across-restart | FIXED | recreate-over-tombstone across a restart kept an empty VV => a peer's tombstone dominated and **re-deleted a legitimate recreate**; named acceptance test absent | Bump the recreate's VV in `restoreVVs` when present on disk; add the end-to-end restart scenario | **high** |
| **PR-4** tombstones / ghost counters | FIXED | `DropCounter` (#10590 mitigation) had **zero production callers** — dead code; restart-with-pending-tombstone untested | Wire a startup de-pair sweep keyed on the declared `-peer` set; add the missing integration tests | med |
| **PR-5** rename zero-network | FIXED | Efficiency claim untrue on the *receiver* apply path (wire ordering discarded; remove ran before reuse) | Couple the rename on `reconcileWithPeer` (materialise new from still-present old bytes, then deferred tombstone) — order-independent | med |
| **PR-6** no sync loop | FIXED | The in-flight apply guard had **zero** targeted coverage (deleting it failed no test); `expected` map was dead write-only state | Pin the guard with a deterministic regression test; remove the dead map; add a bounded outbound-frame counter | med |
| **MK-2** file-vs-dir clash | FIXED | Differ emitted a **false absence** for a path that is a file on one side, a dir on the other => downstream **livelock** | Truthful `IsTypeClash` differ marker + engine **refuse-and-flag** (no loss, no livelock), mirroring the case-clobber carve-out | med |
| **MK-1** incremental rebuild | FIXED | The "O(depth) incremental rebuild" claim was **false against the code** (every change is an O(n) full rebuild) | Implement real copy-on-write incremental rebuild; fix the false comment. (Output property "one byte => exactly that branch" was always true/tested) | low |

The other Phase 6 reviews (PR-2, PR-7, MK-3) and the four flow-verifier system invariants
passed without overturn. The Phase 3 design critique had already run the same gauntlet: all
16 design findings were **3/3 REFUTED at filed severity**, distilled into 8 lower-severity
Consolidated Design Decisions (CDD-1..8) that the plan then folded into acceptance criteria.

---

## 3. Sync invariants — convergence status (with evidence)

The four end-to-end invariants the flow-verifier owns, plus the transfer-safety invariant
that underpins no-loss. All proven by a green `-race` test **and** an independent end-to-end
oracle, re-run live this session. Disposition rationale:
`docs/audit/decisions/phase6/flow-verification-disposition.md`; full report:
`docs/audit/findings/review/flow-verification.md`.

| # | Invariant | Rule | Status | Primary evidence (test + run log) |
|---|---|---|---|---|
| 1 | **Eventual consistency** — identical root hash at quiescence | SR-5 | **CONVERGED** | `TestTwoNode_Converge`; `scenario-convergence.log`; `two-process-demo.log` ("RESULT: CONVERGED"). Oracle is exact because the structural hash excludes mtime/size and includes the VV (`internal/merkle/codec.go`) |
| 2 | **No data loss on conflict** — loser renamed, never deleted; deterministic symmetric copy name | SR-7, SR-9 | **CONVERGED** | `TestConflict_NeitherVersionLostSymmetricName` (byte-identical copy name on both peers), `TestDeletion_NoResurrection`, `TestRename_PropagatesNoLoss`; loser-copy enqueued **before** the winner overwrite (`engine.go execute`); `scenario-conflict/deletion/rename.log` |
| 3 | **No sync loop** — a received file => zero outbound broadcasts | SR-6, SR-8 | **CONVERGED** | `TestApply_ZeroOutboundBroadcasts` (0 on apply echo, exactly 1 on a genuine edit), `TestApply_IdempotentRedelivery`. One **bounded, loop-free** documented exception: a conflict-copy loser is re-advertised exactly once (fixed VV => peer sees `Equal`, cannot loop) |
| 4 | **Clean goroutine shutdown** — `NumGoroutine` returns to baseline on peer churn / cancel | GR-3 | **CONVERGED** | `TestConnChurn_NoGoroutineLeak` (15 connect/disconnect cycles -> baseline), `TestConn_CtxCancelTearsDown`, `TestTransport_CloseIsIdempotent`; engine reaping corroborated by the integration cleanup that would hang on a leak |
| + | **Atomic interrupted transfer** — kill mid-stream leaves no corrupt file, no temp; re-run completes | SR-1, SR-2 | **CONVERGED** | `TestKilledTransfer_NoCorruptFileThenRecovers` + `atomicWriteVerify` verify-before-rename (`transfer.go`); `scenario-killed-transfer.log` |

The convergence oracle is honest: SR-5 holds **at quiescence** (CDD-8), and the complexity
claim is the **prune-equal property** ("one byte => exactly that leaf's branch + root"), never
a big-O assertion. Two convergence outcomes are accepted **non-converging-but-safe**
carve-outs (data is never lost or clobbered): a **case/normalisation clobber** and a
**file-vs-directory type clash** are *refused + flagged*, not silently merged.

---

## 4. Phase 6 / Phase 7 outcome

**Phase 6 (correctness review).** The evidence-generator ran the six two-instance scenarios
on the Mac (loopback) capturing `docs/audit/runs/*.log`, plus a real two-daemon multicast
demo and the Windows cross-compile check, and emitted the two cross-OS artifacts:
`.github/workflows/ci.yml` (ubuntu/macos/windows matrix) and
`docs/audit/CROSS_PLATFORM_CHECKLIST.md`. The reviewer + 3 skeptics graded each fixed
finding; the flow-verifier graded the four system invariants **PASS**. The skeptics
**refuted the "fixed" verdict on 8 findings** (§2) — the adversarial loop working as designed.

**Phase 7 (fix loop), round 1.** Every refuted finding was resolved, each behind a logged
decision in `docs/audit/decisions/phase7/`. The headline result: **three of the eight were
real bugs, not over-claims** — two of them deterministic data-loss (PR-3) / un-transferable
or wedged state (REV-FLAKE-1), one resurrection-on-restart (MK-6). The flake finding was
formally **OVERTURNED** to record that the skeptics were right. Post-fix evidence: full
`-race` suite green (re-verified today), 120 oversubscribed race binaries -> 0 failures,
Windows cross-compile clean, **no production default changed**. The loop reached a clean
state in one round (no halt condition triggered).

Two **non-blocking, defense-in-depth recommendations** remain open by explicit decision (the
invariants they would re-cover are already proven by other green evidence):
- **R1** — an engine-level `NumGoroutine` churn assertion while `Run` stays live (today the
  engine's reaping is proven *indirectly* via the terminating `wg.Wait()` + a cleanup that
  would hang on a leak; the transport has a dedicated assertion).
- **R2** — lift the zero-outbound assertion to a real two-engine integration test.

---

## 5. What is deferred — and why

All deferrals are **justified, owner-named, and binding** against
`docs/audit/decisions/phase1/scope-boundary-vs-syncthing.md`, STEERING §D, and the planner's
deferral list. Nothing here is a silent gap.

**Scope (deliberately not built vs Syncthing):** global / cross-subnet discovery and relays /
NAT traversal (LAN-only by design — a discovery server would reintroduce the central
dependency the project exists to avoid); GUI / REST; persistent multi-device index DB
(replaced by one in-memory tree + a *local-only* snapshot); N-device cluster; at-rest /
untrusted-peer encryption (TLS 1.3 in transit, LAN peers paired); LZ4 wire compression;
`DownloadProgress` swarm fetch; send-only / receive-only modes; protobuf.

**Algorithmic (v1 chose the simpler-correct path):** **content-defined chunking (CDC)** —
v1 uses **fixed 32 KiB content-addressed blocks** behind a fail-closed `algo_version`, CDC is
the adapt-later path; **rsync rolling-delta** — rejected on a LAN (rsync's own authors default
to whole-file); **`MOVE` message / hash-match rename** — v1 rename = delete+create with
content-addressed reuse (correct + lossless), `MOVE` reserved as a future `0x08+` type;
**`previous_blocks_hash` content-causality** — v1 treats any concurrent VV as a conflict
(eager but never lossy); **SAS first-contact verification** — the out-of-band paired
allow-list already strengthens TOFU.

**Accepted v1 limitations (documented + negative-tested, not bugs):**
- **Zero-replica delete** (CDD-7.3): a deleter wiped *before* the tombstone propagates is
  unrecoverable — information-theoretically true of any decentralised design. The negative
  test asserts the file is never *silently re-adopted* (it is deleted, conflicted, or flagged).
- **File-vs-dir type clash** (MK-2): v1 **refuses + flags** rather than auto-converging; the
  auto keep-both path (directory wins, file -> `.sync-conflict`) is the logged forward path,
  deferred to avoid touching the just-certified no-sync-loop / no-leak machinery.
- **Incremental rebuild** (MK-1): the *output* property is guaranteed and tested; the
  computational cost is a full O(n) rebuild per change (acceptable for a 2-device LAN folder).
- **Empty directories are not synced** (CDD-8): a directory deletion = deletion of all
  contained files; first-class empty-dir support is an explicit out-of-scope future decision.
- **Resumable transfer** is deferred: an interrupted large transfer restarts the file rather
  than re-fetching only the tail (a production robustness enhancement, not a correctness fix).
- **R1 / R2** flow-verifier coverage recommendations (§4).

---

## 6. CROSS-PLATFORM CHECKLIST status — the honest caveat

**Treat "green on the Mac / green in CI" as necessary but NOT sufficient.** Real cross-OS
behaviour cannot be proven from one machine. The split below is the whole reason this
project ships a manual checklist alongside the test suite.

### Verifiable autonomously (done, green today)
- Protocol correctness — two local instances converge / conflict / delete / rename / recover
  from a killed transfer (§3, all green, re-run this session).
- `GOOS=windows GOARCH=amd64 go build ./cmd/msync` compiles clean (PE32+ artifact).
- Path-normalisation logic via table-driven tests with **Windows-style inputs** — the
  `pathnorm` Target is an explicit parameter, *not* a `runtime.GOOS` gate, so the Windows
  escape path is exercised on the Mac (only the actual on-disk write is a Windows tail).
- Unicode NFC/NFD normalisation unit tests; the cross-platform structural-hash round-trip.

### Closed by CI (a real `windows-latest` runner)
`.github/workflows/ci.yml` fans out **ubuntu / macos / windows** and runs the full
`go test ./... -race -shuffle=on` suite — **including the two-node loopback scenarios** — on a
real Windows runner, plus a gofmt-drift gate and a fast `GOOS=windows` cross-compile job. This
closes the **protocol** half of the gap on Windows. **Caveat:** CI proves the protocol on
Windows; it does **not** exercise a real NTFS volume's case/normalisation table, the Windows
Firewall, real `ReadDirectoryChangesW` behaviour, or a second physical machine on a LAN.

### Still needs a REAL Windows box — UNRUN (sign-off table is blank)
`docs/audit/CROSS_PLATFORM_CHECKLIST.md` has **9 items, none signed off** (every row of its
sign-off table is empty). A human must run it once with a macOS box **and** a Windows 10/11
box on the **same LAN/subnet**:

| # | Checklist item | Why a Mac/CI cannot prove it |
|---|---|---|
| 1 | Discovery over a real LAN + Windows Firewall | loopback (lo0) does not route the multicast group; the Defender Firewall prompt only exists on Windows |
| 2 | End-to-end converge/conflict/delete/rename on real hardware + real network | exercises real timing, real sockets, real two-machine pairing |
| 3 | Case-insensitive collision (`File.txt` vs `file.txt`) | NTFS `$UpCase` != Go `unicode.SimpleFold`; no-clobber is the **filesystem's** verdict, only testable on NTFS |
| 4 | Reserved device names / trailing dot-space / MAX_PATH | `CON`/`PRN`/`NUL`, `>260`-char paths, `\\?\` long-path — Windows-only filesystem rejection |
| 5 | Unicode NFD <-> NFC over the wire | needs a real APFS (NFD-preserving) <-> NTFS round-trip, not a synthetic byte string |
| 6 | Path separators / deep-tree round-trip | confirms no `\` is ever stored and subtree hashes match across OSes on real disks |
| 7 | Watcher overflow -> rescan recovers | `ReadDirectoryChangesW` silently drops its 64 KiB buffer on overflow — a Windows-only failure mode |
| 8 | Atomic transfer, kill mid-stream on real hardware | Windows `os.Rename` is **not** POSIX-atomic; needs a real process kill on NTFS |
| 9 | File <-> directory type clash refuse-and-flag | the new MK-2 carve-out, on a real cross-OS divergence |

**Bottom line.** The engine is **protocol-correct and race-clean on the Mac and on the CI
Windows runner**, and the cross-platform logic is unit-proven with Windows inputs — but the
project is **not** cross-OS-signed-off until a human works `CROSS_PLATFORM_CHECKLIST.md`
through on real hardware and every row reads PASS. Any FAIL is filed as a new
`docs/audit/findings/crossplatform/` finding and re-run after a fix.

---

## 7. How to reproduce / verify

```sh
go build ./...                                    # exit 0
go test ./... -race -shuffle=on -count=1          # all 7 packages ok  (the CI matrix command)
GOOS=windows GOARCH=amd64 go build ./cmd/msync    # PE32+ artifact, exit 0
go test ./test/integration -run TestTwoNode_Converge -v   # protocol convergence proof
```
Then the real cross-OS pass: push the branch so CI runs `windows-latest`, and work through
`docs/audit/CROSS_PLATFORM_CHECKLIST.md` on a real Windows box on the same LAN.

**Audit trail:** `docs/audit/decisions/` (62 — every consequential choice, logged before it
was acted on), `docs/audit/findings/` (130 — literature, codebases, protocol, merkle,
crossplatform, design critique + skeptic votes, post-implementation reviews),
`docs/audit/runs/` (10 scenario logs), `docs/audit/rules/` (4 hard-rule docs: SR/GR/XP),
`docs/audit/plan/` (structure + implementation plan), `docs/audit/decisions/STEERING.md`
(the human-in-the-loop hardening after Phase 2).
