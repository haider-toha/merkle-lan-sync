# Decision: version-vector counter seeding — pure logical `prev+1` vs hybrid wall-clock floor

- Area: protocol (Phase 2 — protocol-researcher)
- Status: decided (resolves synthesis **OQ-2**, the §6.1 contradiction between the
  `syncthing-bep` and `version-vectors` literature findings)
- Date: 2026-06-28
- Decider: protocol-researcher (Phase 2)

## Context

A version vector is `map[ShortID]uint64`; a device bumps **its own** counter on a
confirmed local change (SR-6). The open question is **what value the bump assigns**.
Syncthing uses a **hybrid logical clock**: `Value = max(prev+1, unixNow)`
(`vector.go:127-149 @2775f424f228`; `version-vectors` §4.4, `syncthing-bep` §4.4).
This is the one place where the two literature findings **directly disagree**
(synthesis §6.1 escalated it to me):

- `syncthing-bep` §4.4/§10.1: prefer a **pure logical counter** (`prev+1`) so mtime
  stays *strictly* a tiebreaker per SR-4; the wall-clock floor "re-imports
  wall-clock into ordering."
- `version-vectors` §8 A5: **adopt the hybrid floor**; it gives strict per-device
  monotonicity and **survives a state reset without counter rollback** (defends
  FM-4), while *not* making wall-clock the ordering authority.

Both agree the **causality math (`Compare`/`Merge`) is correct either way** — this
choice affects skew/robustness, not the correctness of conflict *detection*. SR-4 is
a **hard project rule**: "mtime/wall-clock is never the source of truth for
ordering; version vectors are."

### The failure that decides it — counter rollback (FM-4), worked through

Devices M, P synced; file F has VV `{M:5, P:3}` on both.

- **Pure `prev+1`, naïve restart-at-1:** M's state is wiped, M rescans, edits F →
  assigns `{M:1}`→`{M:2}`. P still holds `{M:5, P:3}`. `Compare({M:2},{M:5,P:3})`:
  M-slot `2<5`, P-slot `0<3` ⇒ **P Dominates** ⇒ P treats M's *genuinely new* edit
  as stale and pushes its old version back; M pulls it, **overwriting M's edit →
  silent data loss.** The conflict path does **not** fire (it looks causal, not
  concurrent). This is the marquee version-vector data-loss trap (`version-vectors`
  FM-4; aphyr "trouble with timestamps").
- **Hybrid `max(prev+1, now)`:** post-wipe M edits F → `{M:~1.75e9}`.
  `Compare({M:1.75e9},{M:5,P:3})`: M-slot greater on M's side, P-slot greater on P's
  side ⇒ **Concurrent** ⇒ **conflict copy** ⇒ both versions preserved. **No silent
  loss** (it degrades to a safe conflict).

So the *naïve* pure counter can silently lose data on a state wipe; the hybrid
turns that into a safe conflict. The pure counter is only safe **if rollback is
independently prevented** — which is exactly the condition the synthesis lean named.

## Options (scored 1–5; axes = correctness / concurrency-safety / testability / cross-platform)

### Option A — pure logical `prev+1`, rollback prevented by persistence + reseed + a conflict backstop (CHOSEN)
`Value = prev + 1` (new counter starts at 1). Rollback is made impossible-to-lose-
data by three guards:
1. **Persist VVs in the last-synced tree snapshot** (OQ-5 / R-5; gob is allowed for
   *local* state, GR-7) and restore on startup ⇒ a normal daemon restart never rolls
   back.
2. **Cold-start reseed:** if no snapshot is found (true wipe / first run), the device
   enters *reseed mode* — on the first `INDEX` exchange it `Merge`s the peer's VV for
   every shared path **before** asserting any local authorship; a local file whose
   `content_hash` differs from the merged-VV version is then bumped **on top of** the
   merged vector (so it Dominates and is accepted, not discarded).
3. **Equal-VV-but-differing-content ⇒ conflict** (never a silent overwrite): a
   defensive backstop so any residual VV anomaly degrades to an SR-7 conflict copy,
   not data loss.
- correctness **5** — honours SR-4 maximally (wall-clock never enters a counter
  value, so it can never enter ordering); the three guards close FM-4 for a 2-device
  tool; worst case degrades to a safe conflict.
- concurrency **5** — counters are copy-on-write (`version-vectors` §8 A4); no time
  dependence to race on.
- testability **5** — **deterministic**: no clock to inject; table-driven
  dominates/dominated/concurrent tests and a scripted wipe→reseed test are exact and
  reproducible (this is the decisive axis vs Option B).
- cross-platform **5** — pure integer math, identical Mac/Windows.

### Option B — hybrid `max(prev+1, unixNow)` (Syncthing's choice)
- correctness **4** — robust against rollback *without* a reseed protocol, and
  conflict detection stays correct; **but** it seeds an ordering-relevant counter
  value from the wall clock, bending SR-4's intent, and a badly-skewed (far-future)
  clock inflates that device's counters for a long time (`syncthing-bep` §10.1).
- concurrency **5**.
- testability **3** — non-deterministic unless you thread an injectable `now`
  (Syncthing added `updateWithNow(id, now)` *specifically* to make it testable —
  `vector.go:127`); every VV test must control the clock.
- cross-platform **5** (integer), but couples behaviour to each host's clock.
- **Not chosen:** loses on testability and on SR-4-cleanliness. Kept as the
  documented fallback **iff** the cold-start reseed (Option A guard 2) proves too
  costly to make correct in WS-4 — adopt the floor *knowingly*, documenting the skew
  caveat, per the synthesis lean's second branch.

### Option C — pure `prev+1`, no special handling (naïve)
- correctness **2** — the FM-4 worked example above is a direct silent-data-loss
  path on any state wipe followed by a local edit. Rejected.
- (other axes irrelevant given the correctness failure).

## Decision

Adopt **Option A**: **pure logical `prev+1`**, with rollback neutralised by
(1) persisting VVs in the last-synced snapshot, (2) a cold-start reseed that merges
the peer's vectors before asserting local authorship, and (3) an
equal-VV-differing-content ⇒ conflict backstop. The hybrid floor (Option B) is the
documented fallback if guard (2) cannot be made correct cheaply in WS-4.

## Rationale

- SR-4 is a **hard rule**; pure `prev+1` is the only option that keeps wall-clock
  entirely out of counter *values* (and therefore out of ordering), so the rule holds
  by construction rather than "in spirit."
- **Testability is a graded acceptance axis** (plan/agent_roster.md); deterministic
  VV math with no injected clock is materially easier to make and trust for code that
  guards user files.
- The only thing the hybrid bought — rollback resistance — is recoverable for a
  *2-device LAN tool* via the persisted snapshot + reseed, which we need *anyway* for
  deletion-across-restart (OQ-5/R-5). We reuse that mechanism instead of importing the
  wall clock.
- The conflict backstop converts any residual anomaly into the SR-7 no-data-loss path,
  which is the prime directive.

## Consequences

- Drives `internal/protocol/versionvector.go` (`Bump` = `prev+1`, copy-on-write) and
  binds WS-4 to implement the **cold-start reseed** (a protocol-layer obligation: a
  device with no snapshot must `Merge` peer VVs on first `INDEX` before broadcasting
  any local-authored `INDEX_UPDATE`) and the **equal-VV-differing-content ⇒ conflict**
  rule in `apply.go`/`conflict.go`.
- **Hard dependency on OQ-5/R-5** (persisted last-synced snapshot, owned by
  tree-critic → WS-1/WS-4). If that snapshot is not delivered, the rollback guarantee
  weakens to "reseed-on-connect only," and the §"fallback" hybrid floor must be
  reconsidered — flagged for the Phase 3 protocol-critic and concurrency-critic.
- **Test obligations:** dominates/dominated/concurrent table (SR-4); a scripted
  wipe→edit→reconnect scenario asserting the edit is preserved (as a normal apply
  after reseed, or as a conflict copy — never lost); `-race` on copy-on-write `Bump`.
- Cross-references: SR-4, SR-6, SR-7; `version-vectors` §4.4/§8 A5/FM-4;
  `syncthing-bep` §4.4/§10.1; synthesis §6.1 / OQ-2; OQ-5 (persisted snapshot);
  `vv-pruning-counter-cleanup.md` (the sibling state-cleanup decision).
