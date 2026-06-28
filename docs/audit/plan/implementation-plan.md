# Implementation plan — Merkle Sync (Phase 4, planner)

- Phase / role: Phase 4 — planner.
- Date / access date: 2026-06-28.
- Module: `github.com/haider-toha/merkle-sync` (Go 1.23).
- Reads-first (all consumed): `docs/audit/findings/design/consolidated/overview.md`
  (CDD-1..CDD-8), `docs/audit/findings/synthesis/problem-space-map.md` (AL/N/OQ/R
  registers, §3 DAG), the Phase 2 findings
  `docs/audit/findings/{merkle/MK-1..6, protocol/PR-1..7, crossplatform/*}`,
  `docs/audit/plan/structure.md`, `docs/audit/decisions/STEERING.md`,
  `docs/audit/rules/{sync,go,crossplatform}-rules.md` (SR/GR/XP IDs),
  `.claude/skills/merkle-sync/SKILL.md`.
- Planner decision logged before this plan:
  `docs/audit/decisions/phase4/workstream-sequencing-and-protocol-leaf.md`
  (adds the foundational **WS-0** beneath the roster's four-workstream spine).

## Method & contract compliance

Every acceptance criterion below is **phrased as a sync invariant**, mapped to a
**hard-rule ID** (`SR-n`/`GR-n`/`XP-n`) and a **named test** an implementer can
make green, and tagged with the consolidated design decisions (`CDD-n`) and Phase 2
findings (`MK-n`/`PR-n`) it discharges. The four workstreams the roster/task mandate
(WS-1..WS-4) keep their acceptance criteria **verbatim**; WS-0 is the additive
foundational leaf the import DAG requires (decision above). Per Phase 5 contract,
each plan item is "done" only when `go build ./... && go test ./... -race` is green
and the cited finding is set `fixed` with the commit SHA.

The honest complexity claim (synthesis §6.2, MK-2): acceptance tests assert the
**prune-equal property** ("one byte ⇒ exactly that leaf's branch + root, nothing
else"), **never** a big-O assertion. The convergence oracle (SR-5, CDD-8) holds **at
quiescence**, so every integration assertion is quiesce-then-compare.

---

## Dependency order

Hard compile DAG (authoritative, = `structure.md` §"Dependency DAG", synthesis §3.A):

```
                WS-0  internal/protocol   (framing, messages, VersionVector, DeviceID)
                  │   internal/pathnorm   (canonical key — co-foundational leaf, built in WS-1)
        ┌─────────┼───────────────┐
        ▼         ▼               ▼
      WS-1      WS-2            WS-3
   merkle +   transport       discovery
   pathnorm   (TLS+framing)   (UDP multicast)
        └─────────┴───────┬───────┘
                          ▼
                        WS-4  reconcile ─► cmd/msync
```

**Recommended implementation sequence (the mandated spine):**
`WS-0 → WS-1 → {WS-2, WS-3} → WS-4`.

- `internal/protocol` and `internal/pathnorm` are the two stdlib-only leaves.
  `protocol` **never imports `merkle`** (keeps the graph acyclic); `merkle` imports
  both `protocol` (FileInfo carries the VV) and `pathnorm`.
- `transport` and `discovery` are **siblings** that import only `protocol` (their
  *minimal* compile dep is WS-0). The roster's "WS-1 → {WS-2, WS-3}" is retained as
  the recommended *sequence* — build and prove the state model + the R-1 convergence
  gate before the network layers — and is encoded in the `deps` below. Phase 5 runs
  sequentially in a single tree, so this conservative ordering costs nothing.
- `reconcile` is the **single writer** behind one `RWMutex` (GR-5); it imports
  `merkle + transport + discovery + protocol`. `cmd/msync` wires everything under one
  `signal.NotifyContext` root (GR-2).
- "framing → transport → discovery" in the roster sketch is a *runtime layering*, not
  an import edge: discovery emits `peerEvents{DeviceID, addr}` → reconcile/cmd →
  `transport.dial(addr)` → TLS pins the DeviceID (synthesis §3.C, GR-4). Listeners
  never call each other; they communicate by channels into the reconcile core.

Build gates between workstreams: `go build ./...`, `go test ./... -race -count=1`,
and `GOOS=windows GOARCH=amd64 go build ./cmd/msync` must stay green at every WS
boundary (integrator/build-verifier, up to 3 logged fix attempts else a halt).

---

## WS-0 — Protocol leaf + concurrency/oracle rule amendments (foundation)

**Packages:** `internal/protocol` (`doc.go`, `framing.go`, `messages.go`,
`versionvector.go`, `deviceid.go` + `_test.go`s). **Rule edits:**
`docs/audit/rules/{go,sync}-rules.md`.
**Consumes:** PR-1 (framing + 7-type catalogue), PR-2 (VV `Compare`/`Merge`/`Bump`,
copy-on-write), PR-7 (`DeviceID = SHA-256(cert DER)`, `Short()` = VV key); decisions
`phase0/framing-format.md`, `phase0/message-type-codes.md`,
`protocol/message-type-enumeration.md`, `protocol/vv-counter-seeding.md`,
`protocol/transport-security-tofu-confirm.md`,
`merkle/leaf-shape-and-structural-hash.md` (VV/codec primitives).
**Folds in:** CDD-1 (rule amendments), CDD-2 (framing size budget), CDD-8 (SR-5
"at quiescence" wording).

**Acceptance criteria (invariants → rule → test):**

1. **A frame survives being split across reads.** `ReadFrame` reassembles a valid
   frame fed one byte at a time. — SR-12, GR-8 — `framing_test.go`
   `TestReadFrame_OneByteReader` (`iotest.OneByteReader`).
2. **A malformed/oversized/zero length is rejected before allocation, with a typed
   sentinel, and never desyncs the stream.** `length == 0 || length > MaxFrameLen`
   ⇒ `ErrFrameTooLarge`/`ErrZeroLength`, no large alloc. — SR-12, GR-8 —
   `framing_test.go` `TestReadFrame_OversizedRejected` (assert allocation bound) +
   `TestReadFrame_ZeroLength`.
3. **The framing size budget is pinned and asserted on the sender.**
   `MaxChunkLen = MaxFrameLen − 1 − ResponseHeaderLen(5)`; `WriteFrame` asserts
   `1 + len(payload) ≤ MaxFrameLen`; the `RESPONSE` builder asserts
   `len(data) ≤ MaxChunkLen` (fail loud on the *sender*, never as a peer-dropping
   error on the victim). — CDD-2, SR-12 — `framing_test.go`
   `TestMaxChunkLen_BoundaryRoundTrips`.
4. **Unknown-type policy is split and total.** `0x00` ⇒ fatal (drop conn); `0x08+`
   ⇒ skip the frame and continue (length prefix makes the payload safe to discard);
   all 7 types round-trip encode/decode. — SR-12 (typed `ErrUnknownMsgType`) —
   `messages_test.go` `TestMsgType_UnknownPolicy` + `TestMessages_RoundTrip`.
5. **`VersionVector.Compare` is antisymmetric and total over the 4 outcomes;
   `Merge` = pointwise max; `Bump` = pure `prev+1`; all ops copy-on-write.**
   `Compare(a,b)=Dominates ⟺ Compare(b,a)=DominatedBy`; `Concurrent⟺Concurrent`;
   `Equal⟺Equal`; a missing entry is read as `0` (so a tombstone's bumped counter
   Dominates an absent stale counter — SR-10 substrate). — SR-4 — `versionvector_test.go`
   `TestCompare_Antisymmetry` (property/table) + `TestMerge_PointwiseMax` +
   `TestOps_CopyOnWrite` (`-race`, assert receiver's backing array unchanged).
6. **`DeviceIDFromCert` is deterministic and `Short()` is the VV counter key.**
   `SHA-256(cert DER)` stable across calls; high 64 bits = `Short()`. — (PR-7) —
   `deviceid_test.go` `TestDeviceIDFromCert_Deterministic` + `TestShort_HighBits`.
7. **Rule amendments landed (doc gate).** GR-5 widened to "tree **plus** per-peer
   ack/last-index state **plus** apply-time expected-hash record"; GR-4 companion
   ("never block on outbound from the select loop; per-conn writer owns outbound;
   bulk RESPONSE in its own GR-3 goroutine"); GR-13 fan-in clarified
   (`inboundMsgs`/`peerEvents` are never closed — shutdown is `ctx`+`WaitGroup`;
   per-conn `conn.Close()` idempotent via `sync.Once`); SR-5 reworded "at
   quiescence." — CDD-1, CDD-8 — verified by review (the tests that *exercise* these
   live in WS-2/WS-3/WS-4).

**Seam:** WS-0 ships the framing + message *envelope/type catalogue* + codec
primitives (len-prefix, big-endian, RFC-6962 `0x00`/`0x01` domain-separation
helpers). The `wireFileInfo` *payload* byte grammar is finalized in WS-1 on top of
these primitives (it depends on WS-1's `FileInfo`). Coordinate this hand-off.

**Risk + mitigation:**
- *R (R-1 substrate): a non-deterministic or non-big-endian wire integer poisons
  every downstream hash/compare.* Mitigate by pinning big-endian + fixed widths +
  length-prefixed names in WS-0 with golden-vector tests, before any consumer exists.
- *R: the copy-on-write VV footgun (Syncthing value-receiver aliasing,
  `version-vectors` §4.7).* Mitigate: every op returns a fresh backing array; assert
  under `-race` (criterion 5).

`deps: []`

---

## WS-1 — Merkle tree + scanner + pathnorm (foundational state model)

**Packages:** `internal/pathnorm` (`pathnorm.go`, `normalize.go`, `windows.go`,
`casefold.go`), `internal/merkle` (`fileinfo.go`, `node.go`, `tree.go`, `codec.go`,
`scanner.go`, `hash.go`, `differ.go`, snapshot persist/load helper).
**Consumes:** MK-1 (domain separation + byte-exact grammar), MK-2 (prune-equal
differ), MK-3 (leaf metadata for two-way sync), MK-6 (persisted snapshot), the six
`crossplatform/*` findings (XP-1..6); decisions
`merkle/leaf-shape-and-structural-hash.md`, `merkle/rename-detection.md`,
`crossplatform/{unicode-canonical-form,illegal-name-strategy,maxpath-longpath-handling,case-and-normalization-collision-policy,mode-symlink-mapping}.md`.
**Folds in:** CDD-6 (one authoritative per-component `ToOSPath`/escaping pipeline),
CDD-7.1 (snapshot durability — distinguish locally-authored vs remotely-applied
deletions), CDD-8 (empty-dir scope statement), STEERING §B.1 (RFC-6962 domain sep),
STEERING §B.2 (persisted snapshot), STEERING §A.2 (NFC canonical).

**Acceptance criteria (the mandated three, kept verbatim + the hardening gates):**

1. **Scanning the same folder twice yields the identical root hash.** Deterministic
   scan → identical `FileInfo` set → bit-identical root. — SR-5 — `merkle_test.go`
   `TestScanTwice_IdenticalRoot`.
2. **pathnorm round-trips a Windows-hostile name set without loss.** Reserved chars
   (`< > : " / \ | ? *`), control chars, reserved device names
   (`CON/PRN/AUX/NUL/COM1..9/LPT1..9`, incl. `NUL.txt`), trailing dot/space, and
   NFD↔NFC variants all survive `Canonicalize → ToOSPath → FromOSPath →
   Canonicalize` with `out == in`; escaping is total/injective (`%`→`%25` first;
   `Unescape(Escape(x))==x`; no two distinct inputs share an output). — SR-13, XP-1,
   XP-2, XP-3, XP-4 — `pathnorm_test.go` `TestWindowsHostileRoundTrip` +
   `TestEscape_Injective`.
3. **One byte changed flips the root and exactly that leaf's branch — nothing
   else.** A single-byte edit re-hashes only the changed leaf and its O(depth)
   ancestors; all off-path sibling hashes are byte-identical; the differ visits only
   that branch and prunes every equal subtree at the top-of-call hash compare. —
   SR-5 (prune-equal property, not O(log n)) — `merkle_test.go`
   `TestOneByteChange_MinimalBranch` + `differ_test.go`
   `TestDiff_PrunesEqualSubtrees` (assert no recursion into equal children).
4. **The structural hash is one byte-exact recipe, identical on Mac and Windows
   (R-1 gate).** RFC-6962 domain separation (`0x00` leaf / `0x01` node); structural
   hash includes `{name(NFC, forward-slash, length-prefixed), content_hash,
   canonical-2-state mode, deleted, version_vector(sorted by DeviceID, big-endian)}`
   and **excludes** `{mtime, size}`; children sorted by canonical NFC name bytes;
   n-ary, never duplicate-last (CVE-2012-2459). A `Mac→wire→Windows→wire→Mac`
   round-trip yields a bit-identical root. — SR-5, SR-13, XP-6 (2-state mode) —
   `merkle_test.go` `TestStructuralHash_GoldenVector` + `TestCrossPlatformRoot_RoundTrip`.
5. **A two-way leaf carries what sync needs; a tombstone hashes differently from
   its pre-delete leaf.** `FileInfo{path, content_hash, size, mode, mtime,
   version_vector, deleted}`; `SetDeleted` zeroes content, flips `deleted`, bumps the
   VV ⇒ the tombstone's structural hash differs (so deletes show in the diff). —
   SR-9, MK-3 — `scanner_test.go` `TestTombstone_DistinctHash`.
6. **A deletion that happened while the daemon was down is recovered on restart
   (R-5).** Persist a local-only snapshot (gob, GR-7-permitted for local state)
   storing each leaf's VV + `deleted`; on startup, diff rescan vs snapshot:
   in-snapshot/absent-on-disk ⇒ synthesize a tombstone (bump VV, mark as
   locally-authored); a missing/corrupt snapshot ⇒ conservatively create-only (no
   synthesized deletions), logged. — SR-9, SR-10, SR-11, GR-7, CDD-7.1 —
   `scanner_test.go` `TestSnapshotDiff_SynthesizesDeletion` +
   `TestSnapshotMissing_CreateOnly`.
7. **The scanner pre-filters cheaply but never trusts mtime/size for identity.**
   `size`+`mtime` may *reject* a re-hash, but a content change is confirmed by
   `content_hash`, never by mtime. — SR-4, AL-11 — `scanner_test.go`
   `TestScanner_SizeMtimePrefilter`.
8. **Empty-directory scope is documented (not a silent bug).** Empty dirs are not
   synced; a directory deletion = deletion of all contained files; the dead
   `childCount(0)` grammar line is removed/marked. — CDD-8 — doc + `differ_test.go`
   `TestEmptyDir_NotEmitted`.

**Risk + mitigation:**
- *R-1 (High/High): cross-platform-divergent structural serialization ⇒ roots never
  converge.* Mitigate: criterion 4's single byte-exact recipe + domain separation +
  the Mac↔Windows round-trip gate; this is the project's #1 risk (synthesis §5,
  STEERING §B.1) — make it a hard WS-1 gate, not a nicety.
- *R-5 (Med/Med-High): deletion-across-restart missed ⇒ resurrection/divergence.*
  Mitigate: criterion 6's persisted snapshot; distinguish locally-authored vs
  remotely-applied deletions so a restart does not re-stamp a peer's tombstone as
  local (CDD-7.1).
- *R (case-collision data loss, Windows/NTFS): cannot be fully verified on the Mac.*
  Mitigate: refuse+flag + fold-and-normalise collision index now (XP-4); the
  filesystem-verdict no-clobber check and the NTFS `$UpCase` matrix are WS-4/Phase-6
  (CDD-5). Flag the Mac-unverifiable parts to `CROSS_PLATFORM_CHECKLIST.md`.

`deps: [WS-0]`

---

## WS-2 — Transport (TCP framing over TLS + pinned device identity)

**Packages:** `internal/transport` (`doc.go`, `identity.go`, `tls.go`, `conn.go`,
`listener.go`, `dial.go` + `transport_test.go`).
**Consumes:** PR-7 (TLS 1.3 + TOFU device-ID pinning), PR-1 (framing runs inside
TLS; HELLO re-asserts identity); decisions
`protocol/transport-security-tofu-confirm.md`,
`phase0/transport-security-tofu-vs-plaintext.md`.
**Folds in:** CDD-1 (writer-owned outbound; transport→engine disconnect event;
idempotent owner-only `conn.Close()`), CDD-2 (REQUEST receipt validation hook —
shared with WS-4), STEERING §A.3 (TLS 1.3 + TOFU, no plaintext).

**Acceptance criteria (the mandated three, kept verbatim + concurrency hardening):**

1. **A message survives being split across TCP reads (through the TLS session).**
   A valid frame fragmented across `tls.Conn` reads reassembles correctly. — SR-12,
   GR-8 — `transport_test.go` `TestConn_SplitFrameSurvives` (reuses WS-0 framing over
   a real loopback `tls.Conn`).
2. **A malformed/oversized length is rejected without corrupting the stream.**
   An oversized length on a live conn ⇒ that peer dropped with the typed error, no
   large alloc, no desync of any other conn. — SR-12 — `transport_test.go`
   `TestConn_MalformedLengthDropsPeerCleanly`.
3. **The TLS handshake pins a device identity.** `MinVersion: VersionTLS13`,
   `InsecureSkipVerify: true` **plus** `VerifyConnection` computing
   `SHA-256(PeerCertificates[0].Raw)` and erroring unless allow-listed; a wrong
   fingerprint fails the handshake **before any frame is read**; HELLO re-asserts
   the DeviceID (drop on mismatch). — (PR-7), SR-1-adjacent (we atomically commit
   only authenticated RESPONSE bytes) — `transport_test.go` `TestTLS_PinsIdentity`
   + `TestTLS_WrongFingerprintRejected` + `TestHELLO_DeviceIDMismatchDropped`.
4. **No goroutine leak on peer disconnect; outbound never blocks the select loop.**
   Per-conn reader+writer both exit on close (close conn → unblock reader → cancel
   writer → `Wait`); a transport-sourced `peerEvents{disconnected, DeviceID}` fires
   immediately (not after discovery eviction); `conn.Close()` is idempotent
   (`sync.Once`); outbound is owned by the per-conn writer (buffered-with-shed),
   never sent from a shared select loop. — GR-3, GR-4, GR-13, CDD-1 —
   `transport_test.go` `TestConnChurn_NoGoroutineLeak` (assert
   `runtime.NumGoroutine()` returns to baseline) + `-race`.

**Risk + mitigation:**
- *R (TOFU first-contact MitM, PR-7 §5).* Mitigate: paired allow-list (DeviceIDs
  exchanged out-of-band before first sync); after first contact every conn is
  cryptographically verified. SAS verification is a deferred enhancement.
- *R (goroutine leak / back-pressure deadlock, concurrency-critic).* Mitigate:
  criterion 4 + the GR-4 companion rule (CDD-1) — outbound off the select loop,
  bulk RESPONSE in its own GR-3 goroutine; the bidirectional back-pressure test
  lands in WS-4 integration.

`deps: [WS-0, WS-1]` (compile-minimal dep is WS-0; WS-1 retained as the mandated
sequence — see dependency-order note).

---

## WS-3 — Discovery (UDP multicast registry)

**Packages:** `internal/discovery` (`doc.go`, `multicast.go`, `announce.go`,
`registry.go`, `discovery.go` + `discovery_test.go`).
**Consumes:** AL-16 (multicast announce + heartbeat eviction); SKILL §7 (discovery
is a *hint*, never authorisation); `structure.md` discovery section.
**Folds in:** CDD-1 (the registry is a GR-4 single-goroutine **actor** emitting
`peerEvents`, not a shared-lock map; `-race` test).

**Acceptance criteria (the mandated two, kept verbatim + concurrency hardening):**

1. **A second instance is discovered within the announce interval.** Two instances
   on loopback multicast: instance B appears in A's registry within one announce
   period and A emits a `peerEvents{discovered, DeviceID, addr}`. — (AL-16) —
   `discovery_test.go` `TestDiscovery_SecondInstanceWithinInterval`.
2. **A silent peer is evicted after the heartbeat timeout.** A peer that stops
   announcing is removed after the eviction window and A emits
   `peerEvents{evicted, DeviceID}`. — (AL-16) — `discovery_test.go`
   `TestDiscovery_SilentPeerEvicted`.
3. **The registry is race-free under concurrent announce/evict/dial.** Registry
   state is owned by one actor goroutine; readers consume `peerEvents`, never the
   map. — GR-4, GR-13, CDD-1 — `discovery_test.go`
   `TestDiscovery_RaceAnnounceEvictDial` (`-race`, announce + eviction + a dial
   consumer concurrently).
4. **Discovery is a hint, never authorisation.** A spoofed/unknown announce only
   points at an address whose TLS identity then fails the WS-2 allow-list; no state
   is trusted from the announce itself. — (PR-7 §3) — `discovery_test.go`
   `TestDiscovery_AnnounceIsNotAuth` (asserts no auth decision is taken in
   discovery; cross-checked by WS-2's spoofed-announce test).

**Risk + mitigation:**
- *R (Windows Firewall / multicast may block discovery): not verifiable on the
  Mac.* Mitigate: keep discovery a pure hint so a discovery failure degrades to
  "no auto-pairing," never to a correctness bug; route the real-network check to
  `CROSS_PLATFORM_CHECKLIST.md` + the CI matrix.
- *R (registry data race).* Mitigate: criterion 3's actor model + `-race` (CDD-1).

`deps: [WS-0, WS-1]` (compile-minimal dep is WS-0; WS-1 retained as the mandated
sequence).

---

## WS-4 — Reconciliation (diff + chunk stream + conflict + tombstone)

**Packages:** `internal/reconcile` (`doc.go`, `engine.go`, `watcher.go`,
`scanloop.go`, `broadcast.go`, `apply.go`, `transfer.go`, `conflict.go`,
`tombstone.go` + `reconcile_test.go`); `cmd/msync/main.go` wiring.
**Consumes:** PR-2 (VV-compare drives every branch), PR-3 (deterministic+symmetric
conflict copy), PR-4 (tombstones + anti-resurrection), PR-5 (rename = delete+create,
create-before-delete + content-addressed copy), PR-6 (sync-loop invariant), MK-2
(differ consumed here), MK-4 (fixed 32 KiB blocks), MK-6 (startup reconcile side);
decisions `merkle/chunking-fixed-32kib-vs-cdc.md`, `protocol/vv-counter-seeding.md`,
`protocol/vv-pruning-counter-cleanup.md`, `protocol/tombstone-retention-gc.md`,
`crossplatform/case-and-normalization-collision-policy.md`.
**Folds in:** CDD-2 (REQUEST validation + clamp), CDD-3 (resolver totality over the
Compare×content matrix), CDD-4 (deterministic conflict-copy identity), CDD-5
(filesystem-verdict no-clobber), CDD-7.2 (ack-gated VV pruning), CDD-7.3 (tombstone
wipe v1 limitation + negative test), STEERING §C.2 (ack-gated tombstone GC),
STEERING §C.3 (ack-gated `DropCounter`).

**Acceptance criteria (the mandated four, kept verbatim + the totality/identity/
no-clobber hardening):**

1. **Two divergent instances converge to identical root hashes.** Diverge two
   folders, let propagation quiesce, assert bit-identical roots; the HELLO root-hash
   short-circuit skips INDEX when already equal. — SR-5 (at quiescence, CDD-8) —
   `reconcile_test.go` `TestTwoNode_Converge` (+ integration `converge_test.go`).
2. **Simultaneous edits to one file produce a `.sync-conflict` copy with neither
   version lost.** `Concurrent` VV + differing content ⇒ keep both: winner stays,
   loser renamed (never deleted) and synced as a normal file; the winner function
   `W` is total + commutative so **both peers pick the same loser** and the copy
   filename is byte-identical (UTC `YYYYMMDD-HHMMSS` from the **loser's mtime
   truncated to whole seconds**, deviceID suffix). — SR-7, SR-4 (mtime = tiebreaker
   only), CDD-4 — `reconcile_test.go` `TestConflict_NeitherVersionLost` +
   `TestConflict_SymmetricCopyName` (incl. differing `TZ`) +
   `TestW_Commutative` (property).
3. **A transfer killed mid-stream leaves no corrupt file.** temp-write → `tmp.Sync()`
   → `os.Rename` → parent-dir `fsync`; **verify-after-reconstruct** (whole-file
   SHA-256 == leaf `content_hash`) *before* the rename; on any error discard the temp
   and leave `dst` untouched; a re-run completes. — SR-1, SR-2, AL-12 —
   `reconcile_test.go` `TestKilledTransfer_NoCorruptNoTemp` (+ integration
   `transfer_test.go`).
4. **Receiving a file does not trigger a re-broadcast loop.** Apply a received file,
   let fsnotify fire on the atomic write, assert **zero** outbound `INDEX_UPDATE`:
   `Bump`/broadcast only on confirmed *local authorship* (never on apply); record
   the applied `content_hash` so the rescan sees no new authorship; idempotent
   content-addressed apply makes a redelivery a literal no-op. — SR-6, SR-8, SR-3 —
   `reconcile_test.go` `TestApply_ZeroOutboundBroadcasts` +
   `TestApply_IdempotentRedelivery` (+ integration `loop_test.go`).
5. **The resolver is total over Compare×content (no silent overwrite, no spurious
   conflict).** `Concurrent` AND equal `content_hash` ⇒ **Merge VVs** (pointwise
   max), keep one file, no copy; `Equal` VV but differing `content_hash` ⇒ treat as
   conflict (never silent overwrite); an advertised tombstone for a locally-unknown
   path ⇒ **no-op** (no create, no re-mint); initial scan with no baseline seeds an
   empty VV `{}` (not `{A:1}`). — SR-3, SR-5, SR-7, CDD-3 — `reconcile_test.go`
   `TestResolver_ConcurrentEqualMerges` + `TestResolver_EqualVVDiffContentConflicts`
   + `TestResolver_UnknownTombstoneNoOp`.
6. **A deletion propagates and a stale peer cannot resurrect it.** A local delete is
   a tombstone (`SetDeleted`, bumped VV); a partitioned peer reconnecting with the
   pre-delete file is `DominatedBy` the tombstone ⇒ deletes locally, does **not**
   re-create on the deleter; tombstones retained until both peers ack, then GC'd
   (never on a timer); a premature-GC negative test proves the ack-gate is
   load-bearing. — SR-9, SR-10, CDD-7.2 — `reconcile_test.go`
   `TestTombstone_NoResurrection` + `TestTombstone_PrematureGC_Negative` (+
   integration `deletion_test.go`).
7. **No-clobber is enforced by the filesystem's own verdict, not the engine fold.**
   Before `os.Rename(tmp, dst)`, stat/list the real target dir; if an entry the OS
   considers equal exists under a different canonical key, **refuse + flag** (never
   rename over it) — so a fold mismatch or mis-probe fails safe to refuse. — SR-7
   (no-data-loss spirit), XP-4, CDD-5 — `reconcile_test.go`
   `TestApply_RefusesCaseClobber` (Mac-runnable APFS case; NTFS `$UpCase` matrix →
   Phase 6).
8. **A rename is a lossless delete+create with zero needless transfer.** Emit
   create-before-delete in the batch and content-address the create (reuse local
   bytes under the same `content_hash`), so the new path is never transiently the
   only-copy-lost and costs zero network when bytes are local; old path left a
   non-resurrecting tombstone. — SR-1, SR-5, SR-10, (PR-5/MK-5) — `reconcile_test.go`
   `TestRename_NoNetworkTransfer` + `TestDirRename_SubtreeReparents`.
9. **REQUEST is validated on receipt; the source declines cleanly.** Reject
   `length == 0 || length > MaxChunkLen` (and `offset+length` beyond advertised
   size); decline with `RESPONSE{errorCode = GENERIC, empty}` and keep the conn
   alive; the puller clamps `REQUEST.length ≤ MaxChunkLen` and splits large ranges.
   — SR-12, CDD-2 — `reconcile_test.go` `TestRequest_OversizeDeclinedConnSurvives`.
10. **Watcher drops are recovered by rescan; debounce coalesces a burst.** A
    synthetic overflow/dropped event is still caught by the periodic rescan
    (rescan is truth); a burst of events for one path within ~150 ms ⇒ exactly one
    hash/diff. — SR-11, GR-9, GR-10 — `reconcile_test.go`
    `TestRescan_RecoversDroppedEvent` + `TestDebounce_CoalescesBurst`.
11. **Bidirectional back-pressure cannot deadlock.** Two instances doing
    simultaneous large transfers over small socket buffers converge within a
    timeout (a hang = the deadlock). — GR-3, GR-5, CDD-1 — integration
    `TestBackpressure_BidirectionalConverges`.

**Tombstone-wipe v1 limitation (CDD-7.3, documented + negative test):** a deleter
wiped before propagation (sole tombstone destroyed) is an **accepted v1 limitation**
(a zero-replica delete is unrecoverable by any decentralised design). The
`delete-on-A → partition-B → wipe-A(drop snapshot) → reconnect` test asserts the
file is never *silently* re-adopted as a clean live file (it is deleted, conflicted,
or flagged) — `reconcile_test.go` `TestWipedDeleter_NoSilentReadoption`. The
rejected "quarantine every peer-only path on reseed" remedy is **not** adopted.

**Risk + mitigation:**
- *R-2 (Med-High/High): sync loop / watcher echo.* Mitigate: criterion 4's three
  guards (SR-6 local-only bump, SR-8 expected-hash record, SR-3 idempotent apply);
  flow-verifier asserts "received file ⇒ zero outbound."
- *R-3 (Med/High): tombstone resurrection / ghost VV counters.* Mitigate: bumped-VV
  dominance (criterion 6), ack-gated retention + ack-gated `DropCounter`
  (CDD-7.2/STEERING §C.3); scope `DropCounter` to "un-pair the last peer" for v1
  (2-device, N6).
- *R-4 (Med/High): non-atomic / interrupted-transfer corruption.* Mitigate:
  criterion 3 (atomic write + verify-after-reconstruct); note Windows `os.Rename` is
  not POSIX-atomic (SR-2 caveat) — use the platform-correct replace path, verified in
  Phase 6 on real Windows.
- *R (case-collision data loss on NTFS): not verifiable on the Mac.* Mitigate:
  criterion 7's filesystem-verdict refuse (CDD-5); the `$UpCase`-divergence matrix +
  mixed-sensitivity tree → Phase 6.

`deps: [WS-1, WS-2, WS-3]`

---

## Per-workstream risk register (rollup)

| Risk (synthesis ID) | Likelihood/Impact | Owner WS | Mitigation (criterion) |
|---|---|---|---|
| R-1 cross-platform structural serialization | High/High | WS-1 (+WS-0 substrate) | byte-exact recipe + RFC-6962 domain sep + Mac↔Windows root round-trip (WS-1 #4) |
| R-2 sync loop / watcher echo | Med-High/High | WS-4 | local-only bump + expected-hash + idempotent apply (WS-4 #4) |
| R-3 tombstone resurrection / ghost counters | Med/High | WS-4 | bumped-VV dominance + ack-gated retention/DropCounter (WS-4 #6, CDD-7.2/.3) |
| R-4 interrupted-transfer corruption | Med/High | WS-4 | atomic write + verify-after-reconstruct (WS-4 #3) |
| R-5 deletion-across-restart | Med/Med-High | WS-1 (+WS-4 startup) | persisted local snapshot + rescan diff (WS-1 #6, CDD-7.1) |
| Framing off-by-one / oversized length | Low (well-mitigated) | WS-0/WS-2 | max-length guard + `io.ReadFull` + split/oversize tests (WS-0 #1-3, WS-2 #1-2) |
| Goroutine leak / back-pressure deadlock | Low (well-mitigated) | WS-2/WS-3/WS-4 | GR-3/4/5 + CDD-1 + churn + bidirectional back-pressure tests |
| Case-collision data loss (NTFS) | Windows-only | WS-1/WS-4 + Phase 6 | refuse+flag + filesystem-verdict no-clobber (WS-4 #7, CDD-5) → CI matrix + checklist |
| Clock-skew via VV seeding (OQ-2) | Low | WS-0 | pure `prev+1` + cold-start reseed + Equal-VV-diff-content backstop (decided) |

---

## Deferral list (out of v1 scope — justified)

Binding against synthesis §2.2 (`decisions/phase1/scope-boundary-vs-syncthing.md`,
Option B) and STEERING §D. Each is justified, not dropped; an owner is named for any
future revisit.

| # | Deferred | Why out of v1 scope | Future owner |
|---|---|---|---|
| N1 | **Global / cross-subnet discovery (relay/announce server)** | Both devices are on one LAN; UDP multicast suffices. A discovery server reintroduces the central-server dependency the project exists to avoid. | out of scope (revisit only if multi-site) |
| N2 | **NAT traversal / relays** | Same LAN ⇒ no traversal; relays are a Syncthing surface (N2) we deliberately cut. | out of scope |
| N3 | **GUI / web UI / REST API** | Headless `cmd/msync` daemon; no GUI consumes the device-ID human encoding (N15). | out of scope |
| N4 | **Persistent multi-device index DB** | Replaced by one in-memory Merkle tree under one RWMutex + a *local-only* snapshot (WS-1 #6). The snapshot is **not** N4 (it is single-local last-synced state, not a multi-device global-version DB). | (gap closed by WS-1 snapshot) |
| N5 | **Delta indexes (`index_id`/`sequence`)** | The Merkle root/subtree diff subsumes them (synthesis §2.1); optionally one last-synced root per peer. | merkle-researcher |
| N6 | **N-device cluster / introducer / multi-connection** | 2-device, single conn; `global = WinsConflict(local, remote)`. `DropCounter` scoped to "un-pair the last peer" for v1. | out of scope |
| N7 | **At-rest / untrusted-peer encryption (`RECEIVE_ENCRYPTED`)** | TLS 1.3 in transit; LAN peers are trusted/paired. | out of scope |
| N8 | **LZ4 wire compression** | Fast LAN; framing stays forward-compatible via `featureFlags`. | protocol-researcher |
| N9 | **`DownloadProgress` swarm fetch** | 2-peer convergence needs no swarm. | out of scope |
| N10 | **Send-only / receive-only folder modes** | Send-receive only. | out of scope |
| N11 | **Protobuf wire format** | Hand-rolled `[len][type][payload]` (GR-7); no proto dep. | — |
| N12 | **rsync rolling-search delta codec** | LAN ⇒ rsync's own authors default to whole-file; fixed content-addressed blocks + tree-of-block-hashes is the truth (AL-10 REJECT). | — |
| N13 | **Content-defined chunking (CDC) in v1** | Fixed 32 KiB blocks (MK-4, STEERING §A.1) behind a fail-closed `algo_version`/`featureFlags`; CDC is the adapt-later path and MUST use a fixed shared table (never restic's randomized polynomial). | merkle-researcher |
| N14 | **`PlatformData` ownership / xattr sync** | `mode` canonicalised to a portable 2-state `{exec, fileType}`; raw xattrs out of scope (XP-6). | crossplatform-researcher |
| N15 | **Human device-ID encoding flourish (Luhn/base32 chunking)** | Plain hex/base32; no GUI consumes it. | — |
| — | **`MOVE` message / hash-match rename detection** | v1 rename = delete+create (create-before-delete + content-addressed copy), correct and lossless (MK-5/PR-5); reserve `MOVE` as a future `0x08+` type behind `featureFlags`, never renumber. | merkle-researcher (OQ-7) |
| — | **`previous_blocks_hash` content-causality fast-forward** | v1 treats any `Concurrent` VV as a conflict (eager but never lossy); refine only if spurious conflict copies are measured (OQ-8). | merkle-researcher |
| — | **SAS (short-authentication-string) verification for TOFU first-contact** | Paired allow-list (out-of-band DeviceID exchange) already strengthens first contact beyond blind accept-on-sight (PR-7 §5). | protocol-researcher |

---

## Cross-OS verification hand-off (to Phase 6)

"Green on the Mac" is necessary, not sufficient (README). The following acceptance
items have a Mac-runnable part **and** a real-Windows/NTFS tail that Phase 6 closes
via `.github/workflows/ci.yml` (ubuntu/macos/windows matrix) and
`docs/audit/CROSS_PLATFORM_CHECKLIST.md`:

- WS-1 #2/#4 (Windows-hostile names, reserved names/ADS/trailing-dot/>260, deep-tree
  Mac→Windows→Mac round-trip, prefix stripping) — XP-1/XP-3.
- WS-4 #7 (NTFS `$UpCase`-divergence case-collision matrix; case-sensitive subdir in a
  case-insensitive root and vice versa) — CDD-5, XP-4.
- WS-4 #3 (Windows non-POSIX `os.Rename` replace semantics under kill-9) — SR-2.
- WS-3 (Windows Firewall / multicast actually permitting discovery) — AL-16.
- WS-4 #10 (real `ReadDirectoryChangesW` overflow/drop, exact `WithBufferSize`,
  rename-watch cleanup under load) — XP-5, GR-9.
- CDD-7 (kill-9-between-snapshots; `delete → partition → wipe → reconnect`) — SR-10.

The flow-verifier asserts the system-level invariants **at quiescence** (CDD-8):
eventual consistency (equal root), no data loss (every conflict left a recoverable
copy), no sync loop (a received file produced zero outbound broadcasts), clean
goroutine shutdown on peer loss.
