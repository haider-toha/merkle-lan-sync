# Phase 3 — Consolidated design review

- Role: design-consolidator
- Date: 2026-06-28
- Inputs: 16 design findings (tree / protocol / concurrency / crossplatform critics) +
  48 skeptic votes (3 per finding).
- Disposition method logged in
  `docs/audit/decisions/phase3/consolidation-disposition-of-refuted-findings.md`.

---

## A. Method & verdict

**Keep rule:** a finding is VERIFIED only when **>=2 of 3 skeptics failed to refute it**
(i.e. `refuted <= 1`); otherwise REJECTED.

I read every finding and every vote and counted the explicit `Vote:` line in each vote
file. The outcome is uniform:

| Finding | Filed sev | REFUTED votes | Verdict |
|---|---|---|---|
| tree-critic-1 — directories not first-class | high | 3 / 3 | REJECTED |
| tree-critic-2 — snapshot not crash-safe → VV rollback | high | 3 / 3 | REJECTED |
| tree-critic-3 — VV bootstrap + concurrent-equal-content | high | 3 / 3 | REJECTED |
| tree-critic-4 — tombstone GC breaks root-equality oracle | medium | 3 / 3 | REJECTED |
| protocol-critic-1 — framing MaxFrameLen accounting | high | 3 / 3 | REJECTED |
| protocol-critic-2 — VV pruning signal unsound | medium | 3 / 3 | REJECTED |
| protocol-critic-3 — conflict-copy non-deterministic identity | high | 3 / 3 | REJECTED |
| protocol-critic-4 — tombstone wipe resurrection / reseed | high | 3 / 3 | REJECTED |
| concurrency-critic-1 — RWMutex boundary incomplete | high | 3 / 3 | REJECTED |
| concurrency-critic-2 — back-pressure deadlock | high | 3 / 3 | REJECTED |
| concurrency-critic-3 — peer-disconnect goroutine leak | high | 3 / 3 | REJECTED |
| concurrency-critic-4 — fan-in channel close / cancel | medium | 3 / 3 | REJECTED |
| crossplatform-critic-1 — ToOSPath pipeline contradictory | high | 3 / 3 | REJECTED |
| crossplatform-critic-2 — collision fold vs NTFS $UpCase | high | 3 / 3 | REJECTED |
| crossplatform-critic-3 — predicate-gated escape non-injective | medium | 3 / 3 | REJECTED |
| crossplatform-critic-4 — per-root case probe mis-classifies | medium | 3 / 3 | REJECTED |

**0 verified, 16 rejected.** All 16 finding files have been set to `status: rejected`.

The rejections are **not** flat dismissals. In almost every case all three skeptics
**conceded the technical core is real**, refuted on **severity / framing / remedy**, and
**explicitly told the consolidator to retain a downgraded kernel**. Per the logged
disposition decision (Option B — *reject-and-distil*), those unanimously-conceded kernels
are carried forward, de-duplicated and severity-corrected, as the **Consolidated Design
Decisions** in section D. That is the actionable output Phase 4 (planner) consumes — not
the findings at their filed severity.

---

## B. Verified design decisions

**None.** No finding met the `>=2/3 failed-to-refute` bar; every finding drew a unanimous
3/3 REFUTED. There are therefore no verified findings to promote at their filed severity.

The carry-forward value of Phase 3 lives entirely in section D (merged decisions): the
low/medium kernels the skeptics agreed are worth doing. Treat section D, not the rejected
findings, as the design contract going into Phase 4.

---

## C. Rejected findings — why (the load-bearing refutation)

Each line is the consensus reason the finding failed at its filed severity. Full
reasoning is in the per-finding `votes/` files.

- **tree-critic-1 (directories not first-class).** Refuted 3/3. The headline "permanent
  SR-5 non-convergence" is self-contradictory: an empty dir produces no `FileInfo`, so it
  is absent from *both* trees symmetrically (roots stay equal). Populated-dir deletion is
  conceded to be handled by contained-file tombstones. The recommended fix (first-class
  directory `FileInfo`, Syncthing `Type=DIRECTORY`) is shown ineffective by the finding's
  *own* citation (syncthing #9371 has that design and still resurrects empty dirs).
  Residue = a one-paragraph scope statement → **CDD-8**.
- **tree-critic-2 (stale snapshot → VV rollback).** Refuted 3/3. The worked example omits
  MK-6's own startup rescan-vs-snapshot bump (a stale VV implies stale snapshot *content*,
  which the startup diff detects), so the presented case lands in a guard-3 conflict copy
  or a same-content no-op, not silent loss. "Every crash" exposure is actually a narrow
  same-file-before-reconnect race; the documented Option B hybrid floor already neutralises
  it. Residue = gate guard-2 on "missing **or** provably behind" + a kill-9 test → **CDD-7**.
- **tree-critic-3 (VV bootstrap + concurrent-equal-content).** Refuted 3/3. The
  conflict-storm requires first-scan-bumps, but SR-6 (no delta vs a recorded baseline ⇒ no
  bump) and the cold-start reseed (covers "first run", merges VVs before authorship)
  already route the out-of-band-copy case to `Equal` → no-op. "Silent overwrite" is
  incoherent for byte-identical content. Residue = pin the missing resolver cell →
  **CDD-3**.
- **tree-critic-4 (tombstone GC breaks oracle).** Refuted 3/3. SR-5 is an *eventual*
  (at-quiescence) oracle; GC is just another propagating change, so mid-GC root skew is
  the same transient as any edit. Tombstone state *is* replicated state, so unequal roots
  during GC skew are the *correct* "not yet converged". The post-GC case is already total
  via MK-2 + SR-3 + SR-6. Rec 1a (peer-relative hash) would destroy the deterministic
  pure-function hash. Residue = SR-5 "at quiescence" wording + write the "advertised
  tombstone for unknown path ⇒ no-op" rule down → **CDD-3 / CDD-8**.
- **protocol-critic-1 (framing MaxFrameLen).** Refuted 3/3. The livelock requires a
  non-conformant 16 MiB `REQUEST` *and* a non-validating source; the chunk size is pinned
  at 32 KiB (~512× headroom), `L = 1 + len(payload)` is stated, and an over-large request
  can be declined with the existing `GENERIC` code. Residue = name `MaxChunkLen`, add
  sender asserts + REQUEST validation → **CDD-2**.
- **protocol-critic-2 (VV pruning signal).** Refuted 3/3. The unsound case needs ≥3
  identities (self + ghost D + surviving peer E) — out of scope per N6 (2-device). The
  decision is already ack-gated/symmetric ("complete only once both live peers applied
  it"); the worked sequence performs the unilateral shrink the ack-gate forbids. The
  reserved `0x08+`/`featureFlags` slot already exists for a future explicit signal.
  Residue = tighten wording (ack-gate is the mechanism, never the literal shrunk INDEX) +
  scope to last-peer for v1 → **CDD-7**.
- **protocol-critic-3 (conflict-copy identity).** Refuted 3/3 — but this carries the most
  real residue. The `time.Now()` charge is a strawman (PR-3 §6 mandates a deterministic
  UTC name and §7 test #3 asserts identical filenames on both peers); the divergent-VV
  state is the normal pre-convergence state of any file and resolves via concurrent+equal
  ⇒ merge. Residue = write the timestamp source (loser mtime) and the copy-VV rule into
  PR-3 prose → **CDD-4**.
- **protocol-critic-4 (tombstone wipe resurrection).** Refuted 3/3. The mechanism is real
  but the "false-mitigation" headline misreads PR-4 §5.1 (whose mitigation is scoped to
  *counter rollback of an existing tombstone*, not sole-tombstone destruction — the case
  is *omitted*, not *falsely claimed*). A zero-replica delete is information-theoretically
  unrecoverable by any design. Rec #2 (quarantine every peer-only path) would conflict-copy
  the whole tree on every first sync / reinstall — **harmful, not carried**. Residue =
  document the accepted v1 limitation + a negative test → **CDD-7**.
- **concurrency-critic-1 (RWMutex boundary).** Refuted 3/3. The "three-writer registry
  race" miscounts: the announce ticker does not touch the registry, and `dial.go` consumes
  the `peerEvents` channel, not the map. GR-4 ("share by communicating") + the
  `discovery.go` actor already own non-tree state; the finding's preferred fix (single-
  goroutine actor) *is* the documented design. Residue = widen GR-5 wording + cross-ref
  GR-4 + add the discovery `-race` test → **CDD-1**.
- **concurrency-critic-2 (back-pressure deadlock).** Refuted 3/3. The cycle requires the
  engine to *block* on outbound from its select loop — an assumption the design does not
  make: `conn.go` has a per-conn writer goroutine that owns `conn.Write`, and bulk
  streaming lives in a separate `transfer.go` under GR-3. Residue = add the symmetric
  "never block on outbound from the select loop" rule + a bidirectional back-pressure
  integration test → **CDD-1**.
- **concurrency-critic-3 (peer-disconnect leak).** Refuted 3/3. The close handshake the
  finding calls missing is GR-3 verbatim ("close the conn → unblock the reader → cancel
  the writer → Wait"), and `conn.go` is explicitly bound to GR-3. Per-peer engine state is
  reclaimed via discovery heartbeat-eviction → `peerEvents` → engine dereg. Residue = add
  a *transport*-sourced disconnect event (faster than eviction) + assert the per-peer map
  returns to baseline in the leak test → **CDD-1**.
- **concurrency-critic-4 (fan-in close / cancel).** Refuted 3/3. GR-13 ("exactly one
  closer, the sender side") constrains *who may* close, it does not mandate that every
  channel *be* closed — so "never close the fan-in channel" is fully GR-13-compliant. The
  "double-close panic" claim is factually wrong for `net.Conn` (a second `Close()` returns
  an error; `Close` concurrent with `Write` is supported). Residue = a one-line GR-13
  clarification for fan-in + `sync.Once` owner-only conn close → **CDD-1**.
- **crossplatform-critic-1 (ToOSPath pipeline).** Refuted 3/3. "Corrupts every Windows
  path" rests on reading one summary sentence as whole-string escaping; the authoritative
  decision escapes **per component** (`IsWindowsUnsafe(component)`, never touches `absRoot`
  or separators), and `\`/`:` are in the unsafe set, escaped *before* `FromSlash`. The
  "only correct pipeline" the finding asks for already exists, distributed across two
  decisions. Residue = consolidate the ordered pipeline in one place + fix the loose
  wording + a `ToOSPath` test → **CDD-6**.
- **crossplatform-critic-2 (fold vs NTFS $UpCase).** Refuted 3/3. The soundness gap (the
  engine's `SimpleFold` ≠ NTFS `$UpCase`) is conceded as real but the *data-loss*
  direction is never exemplified, and the finding's own divergence evidence (newer Unicode
  folds *more*; `$UpCase` is frozen older) points to the *safe* (over-refuse) direction.
  The NTFS side is already deferred to Phase 6. Residue = a defensive pre-rename
  directory-listing existence check (filesystem's own verdict gates the write) +
  Phase-6 NTFS matrix → **CDD-5** (this is the one medium-severity carry-forward).
- **crossplatform-critic-3 (predicate-gated escape).** Refuted 3/3 (high confidence). The
  "non-injective clobber" rests on misreading "only on the platform that rejects the name"
  (platform-gated: *always on Windows targets*) as per-name gating. The scheme escapes
  `%`→`%25` first ("total/reversible"), so `a:b`→`a%3Ab` while `a%3Ab`→`a%253Ab` — distinct
  outputs. Round-trip `unescape(escape(x))==x` already entails injectivity. Residue =
  clarify the wording is platform-gated/total + optional explicit injectivity test →
  **CDD-6**.
- **crossplatform-critic-4 (per-root case probe).** Refuted 3/3. Facts true, but the
  data-loss direction needs a doubly-deliberate exotic config (case-sensitive root with a
  case-insensitive subtree); the realistic mis-probe is the *safe* over-refuse direction;
  the only safety fix duplicates crossplatform-critic-2's filesystem-verdict check; and
  per-folder granularity matches the Syncthing reference. Residue folds entirely into
  **CDD-5**.

**Remedies explicitly rejected and NOT carried** (skeptics showed them harmful or wrong):
protocol-critic-4 rec #2 (quarantine every peer-only path on reseed — breaks first sync
and wipe-recovery); the high-severity "corrupts every path / silent clobber" framings of
crossplatform-critic-1/3 (based on misreadings of the per-component, total escaping spec);
tree-critic-4 rec 1a (peer-relative root hash — destroys the deterministic hash); and the
hot-path per-broadcast fsync of tree-critic-2 rec #1 (Option B hybrid floor is the cheaper
documented alternative).

---

## D. Consolidated design decisions (merged carry-forward)

Eight de-duplicated decisions distilled from the 16 rejected findings' conceded kernels.
Each is the action >=3 skeptics endorsed, at the severity they assigned, tied to a
workstream, with its test obligation and provenance. **These supersede the findings.**

### CDD-1 — Complete the engine concurrency contract (rules + WS-2/WS-3/WS-4)
*Severity: low (doc + tests). Sources: concurrency-critic-1, -2, -3, -4.*

The single-`RWMutex`/`GR-4`-actor model is sound; what is missing is explicitness. Amend
the rules and add the tests so the mandatory `-race` gate (GR-13) can actually catch a
regression:
1. **Widen GR-5 wording** from "guards the tree" to "guards the reconcile core's in-memory
   state — the tree **plus** per-peer ack/last-index state **plus** the apply-time
   expected-hash record," and add a one-line cross-ref: non-tree shared state (the
   discovery registry, scanloop debounce) is owned by a **GR-4 single-goroutine actor** that
   emits `peerEvents`/`fsChanges`, not by a shared lock. (cc-1)
2. **Add a GR-4 companion rule (symmetric to the existing read-cancellability rule):** *the
   reconcile core must never perform a blocking send to a peer from its main select loop;
   outbound is owned by per-conn writer goroutines and is buffered-with-shed (or the peer
   is dropped); bulk `RESPONSE` streaming runs in its own GR-3 spawn-and-own goroutine, not
   on the select loop.* (cc-2)
3. **Add a transport→engine `peerEvents{disconnected, DeviceID}` event** so a dropped peer
   is deregistered immediately (release its outbound channel + ack/last-index state) instead
   of waiting for the discovery heartbeat-eviction window. The per-conn close handshake
   itself is already GR-3 (close-once → unblock reader → cancel writer → Wait). (cc-3)
4. **Clarify GR-13 for fan-in:** a fan-in channel (many senders, one receiver:
   `inboundMsgs`, `peerEvents`) is **never closed** — shutdown is `ctx` + a `WaitGroup`
   over senders; "exactly one closer (sender side)" applies only to single-sender channels
   (e.g. a per-conn outbound channel). Make per-conn `conn.Close()` idempotent
   (`sync.Once`), owner-only. (cc-4) *(Note: the finding's "double-close panic" claim was
   factually wrong for `net.Conn`; only the wording clarification is carried.)*
- **Tests:** discovery `-race` test running announce + eviction + dial concurrently;
  reconcile `-race` test (watcher burst while apply writes and a peer diff RLocks);
  connect/disconnect-churn leak test asserting both `runtime.NumGoroutine()` **and** the
  engine per-peer map return to baseline; a two-instance bidirectional back-pressure test
  (small socket buffers, simultaneous large transfers) asserting convergence within a
  timeout (a hang = the deadlock).

### CDD-2 — Pin the framing size budget and validate REQUEST (WS-2 transport, WS-4 reconcile)
*Severity: low/medium (hardening). Source: protocol-critic-1.*
1. Define `MaxChunkLen = MaxFrameLen - FrameTypeLen(1) - ResponseHeaderLen(5)` in
   `internal/protocol`. `WriteFrame` MUST assert `1 + len(payload) <= MaxFrameLen`; the
   `RESPONSE` builder MUST assert `len(data) <= MaxChunkLen` — fail loudly on the *sender*,
   never as a peer-dropping `ErrFrameTooLarge` on the victim.
2. Validate `REQUEST` on receipt: reject `length == 0 || length > MaxChunkLen` (and
   `offset+length` beyond advertised size) and **decline cleanly** with `RESPONSE{errorCode
   = GENERIC, data = empty}` (the existing code suffices; a dedicated `INVALID_REQUEST` at
   `0x08+` is optional polish). The puller clamps `REQUEST.length <= MaxChunkLen` and splits
   large ranges.
- **Tests:** golden test that `MaxChunkLen` round-trips at the exact boundary; a test that
  `REQUEST{length = MaxFrameLen}` is declined by the *source* with the typed code and the
  connection survives (not rejected by the puller's `ReadFrame`).

### CDD-3 — Make the resolver total over the Compare × content matrix (WS-4; PR-2 decision)
*Severity: low (completeness). Sources: tree-critic-3, tree-critic-4.*
1. Add the missing cell: **`Concurrent` AND `content_hash` equal ⇒ `Merge` the version
   vectors (pointwise max), keep the single file, create no conflict copy.** Both peers
   compute the same merge → roots converge; nothing is duplicated.
2. Add the single-sided/tombstone cell: **an advertised tombstone (`deleted=1`,
   `content_hash=0`) for a path with no local record ⇒ no-op** (already GC'd / already
   absent) — never a create, never a re-mint, never a propagatable delete. State it next to
   MK-2's single-sided rule.
3. Pin **initial scan is not authorship**: a first scan with no recorded baseline seeds an
   empty VV (`{}`), not `{A:1}` (SR-6 as intended; the cold-start reseed then merges the
   peer's VV before any authorship).
- **Tests:** out-of-band-identical-folder pairing converges with **zero** `.sync-conflict`
  copies; two concurrent edits that collide in bytes produce one merged file (no copy, no
  overwrite); a peer advertising a tombstone for a locally-unknown path is a no-op (no
  resurrection, no re-mint).

### CDD-4 — Deterministic conflict-copy identity (WS-4; PR-3 decision)
*Severity: low/medium (doc + test). Sources: protocol-critic-3, tree-critic-4 (rec 4).*
Make the entire conflict-copy leaf a pure function of replicated fields and write it into
PR-3 §6 prose (the symmetric "both peers create it" model depends on this):
1. **Name timestamp = the loser `FileInfo`'s replicated mtime**, formatted UTC
   `YYYYMMDD-HHMMSS`, **truncated to whole seconds** (so Mac-nanosecond vs Windows/FAT
   rounding yield the same string). Never `time.Now()`.
2. **Pin the copy's version vector** explicitly: the copy is a new path that syncs as a
   normal file; when both peers mint it with initially-different VVs over identical bytes it
   resolves via CDD-3's concurrent-equal ⇒ merge cell. Write the chosen rule down so both
   peers compute a byte-identical leaf.
- **Test:** equal-mtime conflict on two instances (incl. different `TZ`) produces the
  identical conflict-copy filename, and the two peers converge to one copy.

### CDD-5 — No-clobber enforced by the filesystem's own verdict, not the engine fold or a global probe (WS-4 apply path; Phase 6)
*Severity: medium (data-safety hardening). Sources: crossplatform-critic-2, -4, -3 (namespace).*
The engine's `SimpleFold(NFC(name))` is not provably equal to the target FS name-equality
(NTFS `$UpCase`, APFS), and case sensitivity varies per-directory (NTFS 1803+) / per-volume
(macOS) — so neither the in-memory fold index nor a single root probe can guarantee
no-clobber. Make the guarantee depend on the filesystem:
1. **Before `os.Rename(tmp, dst)`, stat the real target directory via directory listing.**
   If an entry the OS considers equal already exists under a *different* canonical key,
   **refuse + flag** instead of renaming over it. This is inherently per-directory /
   per-volume and never consults the global probe verdict, so a fold mismatch or a
   mis-probe **fails safe to refuse, never to clobber.**
2. The startup case-sensitivity probe stays as an enforcement *optimisation* only; safety
   does not depend on it. (Optional, low value: re-probe per-directory/per-volume and cache
   — a UX/availability nicety, not a safety fix.)
- **Tests (Phase-6, real NTFS):** a case-collision matrix over characters where
  `unicode.SimpleFold` and NTFS `$UpCase` are known to differ (not only ASCII
  `File.txt`/`file.txt`), asserting zero bytes lost in both mismatch directions; a
  case-sensitive subdir nested in a case-insensitive root (and vice versa) asserting no
  clobber in either configuration.

### CDD-6 — One authoritative, total, per-component ToOSPath / escaping pipeline (WS-1 pathnorm)
*Severity: low (consolidation + tests). Sources: crossplatform-critic-1, -3.*
The per-component, reversible escaping design is correct but split across three documents
and loosely worded in one. Consolidate it in a single authoritative place (SKILL §6) and
make it unambiguous:
1. **`ToOSPath` order:** split the canonical `/`-relative key into components → on a Windows
   target apply `EscapeForWindows` to **every** component (platform-gated, *not* per-name;
   never applied to `absRoot`/the drive colon) → `Join(absRoot, FromSlash(join))` → rely on
   `os.fixLongPath` (never hand-prepend `\\?\`).
2. **Escaping is total/injective:** always escape `%`→`%25` first (so the only `%XX`
   triplets on disk are escapes), then reserved/control/trailing-dot-space/reserved-stem
   cases. Fix the `path-separators.md` summary sentence that lists "FromSlash + escaping …
   in that order" — escaping is per-component and **precedes** `FromSlash`.
- **Tests (Mac-runnable):** drive/root prefix untouched; a component containing a literal
  `\` round-trips (`Unescape` of the last element == `a\b`); a `:`-bearing component never
  yields an OS path with an un-escaped `:`; injectivity over the Windows-hostile table
  **plus** escape-lookalikes (`a%3Ab`, `100%done`, `%43ON`) — no two distinct inputs share
  an output, and `Unescape(Escape(x)) == x`.

### CDD-7 — Version-vector & tombstone lifecycle hardening (WS-1 persistence, WS-4; protocol decisions)
*Severity: low/medium (doc + tests). Sources: tree-critic-2, protocol-critic-2, protocol-critic-4.*
1. **Anti-rollback durability (tree-critic-2):** gate the cold-start reseed (guard 2) on
   "snapshot **missing OR provably behind**", not just "missing"; keep Option B's hybrid
   `max(prev+1, now)` floor as the documented fallback that neutralises rollback for free
   (no per-broadcast fsync). Distinguish locally-authored from remotely-applied deletions in
   the snapshot diff so a restart does not re-stamp a peer-authored tombstone as local.
2. **Pruning signal (protocol-critic-2):** state that the prune mechanism is the **ack-gate**
   (retain a counter until both live peers have applied the drop; never compare a shrunk
   vector against an un-shrunk one) — *not* a literal unilateral shrunk INDEX. For v1
   (2-device, N6) scope `DropCounter` to "un-pair the last peer"; if a wire signal is ever
   needed, use the reserved `0x08+`/`featureFlags` slot. Soften the "defeats FM-3 / kills
   FM-1" claim to the in-scope 2-device case.
3. **Tombstone wipe limit (protocol-critic-4):** add an explicit "deleter wiped before
   propagation (sole tombstone destroyed)" entry to the SR-10 resurrection enumeration,
   documented as an **accepted v1 limitation** (a zero-replica delete is unrecoverable by
   any decentralised design); correct PR-4 §5.1 so its mitigation is clearly scoped to
   *counter rollback*, not sole-tombstone loss. Minimise the window by eager delete
   broadcast + ack-gated retention (already designed). **Do not** adopt the rejected
   quarantine-all-peer-only-paths remedy.
- **Tests:** kill-9-between-snapshots (edit-edit-broadcast, hard-kill, restart, edit again)
  asserting the post-restart edit is preserved and a pending remote tombstone is not turned
  into a local concurrent tombstone; `delete-on-A → partition-B → wipe-A(drop snapshot) →
  reconnect` asserting the file is never *silently* re-adopted as a clean live file (it must
  be deleted, conflicted, or flagged).

### CDD-8 — Empty-directory scope statement + oracle-quiescence wording (WS-1; SKILL/structure)
*Severity: low (doc/scope). Sources: tree-critic-1, tree-critic-4.*
1. **Log the git-model limitation explicitly** in SKILL/structure and a decision:
   *empty directories are not synced; a directory deletion is the deletion of all contained
   files.* Mark the unreachable `nodeEncoding = childCount(0)` empty-dir grammar line dead
   (or remove it). If empty-dir support is ever wanted, that is a separate scoped decision
   (first-class directory `FileInfo`) — explicitly **out of v1 scope**.
2. **Tighten SR-5 wording** to state the oracle holds "**at quiescence** / after a change
   settles," not at every instant — so a consumer never treats an instantaneous root
   inequality (during normal propagation or tombstone-GC skew) as "diverged". The
   flow-verifier must quiesce-then-assert.
- **Test:** a `rm -r` of a populated directory converges on the peer and is not resurrected
  after a partition/reconnect; the flow-verifier samples convergence only after settle.

---

## E. Workstream rollup (for the planner)

| Workstream | Consolidated decisions to fold in |
|---|---|
| **Rules (go-rules / sync-rules)** | CDD-1 (GR-5, GR-4 companion, GR-13 fan-in), CDD-8 (SR-5 quiescence) |
| **WS-1 — Merkle tree + scanner + pathnorm** | CDD-6 (ToOSPath), CDD-7.1 (snapshot durability), CDD-8 (empty-dir scope) |
| **WS-2 — Transport (framing + TLS)** | CDD-1 (writer-owned outbound, disconnect event, conn close once), CDD-2 (framing budget) |
| **WS-3 — Discovery (UDP multicast)** | CDD-1 (registry actor + `-race` test) |
| **WS-4 — Reconciliation** | CDD-2 (REQUEST validation), CDD-3 (resolver totality), CDD-4 (conflict-copy identity), CDD-5 (filesystem-verdict no-clobber), CDD-7.2/.3 (pruning, wipe limit) |
| **Phase 6 — cross-OS verification** | CDD-5 (NTFS collision matrix + mixed-sensitivity tree), CDD-7 (kill-9 + wipe scenarios) |

All carry-forward items are **low severity except CDD-5 (medium)**. None blocks Phase 4;
they are hardening/clarification/test obligations to be threaded into the workstream
acceptance criteria.
