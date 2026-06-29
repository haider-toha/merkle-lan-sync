# PR-6 — The sync-loop invariant: only broadcast after a confirmed local change

- Phase / role: Phase 2 — protocol-researcher
- Severity: **high** (a sync loop is a self-sustaining CPU/network storm that also
  spawns repeated spurious conflict copies — R-2 in the synthesis risk register)
- Status: **fixed** (WS-4) — the three guards are implemented: bump+broadcast only on
  confirmed local authorship (`engine.go` `onLocalChange`/`rescan`, `broadcast.go`);
  applying a received file calls `Merge`, never `Bump`, and is filtered by content
  identity PLUS an in-flight-apply guard (`inflightLocked`) so the brief
  rename-before-handleCompletion window is never mistaken for authorship (SR-8);
  idempotent content-addressed apply makes a redelivery a literal no-op (SR-3).
  Verified by `reconcile_test.go` `TestApply_ZeroOutboundBroadcasts` (apply echo ⇒ 0
  outbound, genuine edit ⇒ exactly 1) + `TestApply_IdempotentRedelivery`, and the
  integration convergence tests assert stable equal roots (no ping-pong). Decision
  `docs/audit/decisions/ws4/resolver-totality-conflict-identity-and-sync-loop.md`.
  Commit `af12de099165f38e11556555acc986b9ba385f24`. (Implements SR-6 + SR-8 + SR-3.)
- Reads-first honoured: `sync-rules.md` SR-3/SR-6/SR-8, `go-rules.md` GR-9/GR-10,
  `findings/synthesis/problem-space-map.md` R-2, `findings/codebases/syncthing-source.md`
  §1d (`walk.go:649-657`), SKILL §3/§8.
- Evidence: fsnotify self-event behaviour and the VV-bump-on-local-only rule inherited
  from the cited rules/findings (access date 2026-06-28).

---

## 1. Claim

An outbound hash broadcast (`INDEX_UPDATE`, PR-1) happens **only** when the scanner
confirms a genuine *local authorship* event, at which point — and **only** then — the
device bumps **its own** VV counter. **Applying a received file never bumps our counter
and never broadcasts.** This single rule (plus two defence-in-depth guards) breaks the
A→B→A→… echo loop that otherwise arises because writing a received file makes the
filesystem watcher fire as if the user had edited it.

## 2. The loop, precisely

Without the invariant: peer A writes `f` → A broadcasts → B applies `f` (atomic
write, SR-1) → **B's fsnotify fires** on the temp-create + rename → B's engine reads it
as a new *local* change → B bumps B's counter and broadcasts → A applies → **A's
fsnotify fires** → A bumps and broadcasts → ∞. The storm also mints fresh conflict
copies each lap. fsnotify *will* surface our own atomic write as events ("a single
'write action' … may show up as one or multiple writes" — GR-9, accessed 2026-06-28),
so the guards must hold; the only question is whether they do.

## 3. The invariant + defence in depth

**Primary rule (SR-6):** bump the VV and broadcast **iff** the change is *locally
authored*. A "locally authored" change is a settled watcher event (debounced ~150 ms,
GR-10) **or** a rescan delta whose newly-computed `content_hash` differs from the one
recorded in the tree — i.e. a real user edit, not our own apply. Syncthing encodes
exactly this: the VV is bumped *only* in the scanner's local-change path
(`dst.Version = src.Version.Update(w.ShortID)`, `walk.go:649-657`).

Three guards, used together (defence in depth — SR-6/SR-8/SR-3):

1. **Counter-bump is tied to local authorship only (SR-6).** Applying a received
   `FileInfo` adopts the *origin's* VV via `Merge` (PR-2); it does **not** call `Bump`.
   So the applied leaf already carries a VV that **dominates** ours — re-announcing it
   would be a no-op even if it leaked.
2. **Record the expected `content_hash` on apply (SR-8).** After atomically writing a
   received file, record the hash we just wrote. When the watcher fires and the
   debounce/rescan recomputes that path's hash, it **equals** the recorded one ⇒ "no
   new authorship" ⇒ no bump, no broadcast. (Optionally also suppress watcher events
   for that path during the brief apply window.)
3. **Idempotent, content-addressed apply (SR-3).** Before writing, compare the incoming
   `content_hash`+VV to the local `FileInfo`; if already present, **do nothing** (no
   write, no rename, no event). A redelivered update (reconnect, overlapping index
   exchange) produces zero side effects.

## 4. Proof obligation: apply ⇒ zero outbound broadcasts

State the invariant to be tested as: **"applying any received update produces zero
outbound `INDEX_UPDATE` frames."**

Argument. An `INDEX_UPDATE` is emitted **only** by the broadcast path, which is gated
on `localAuthorship(path)`. `localAuthorship` is true only when a settled scan finds a
`content_hash` differing from the recorded one for a *locally originated* reason. After
an apply:
- guard 3 makes a duplicate apply a literal no-op (no watcher events at all);
- for a non-duplicate apply, guard 2 makes the post-write rescan compute a hash equal to
  the recorded (just-written) hash ⇒ `localAuthorship` is false ⇒ no bump, no broadcast;
- even in the impossible case that a broadcast leaked, guard 1 ensures the leaked VV is
  dominated-or-equal at the peer ⇒ the peer's apply is a no-op (guard 3 again) ⇒ the
  loop cannot sustain.
Therefore the number of outbound broadcasts caused by an apply is **0**. ∎

This is the flow-verifier's system-level oracle: "a received file produced zero
outbound hash broadcasts" (plan/agent_roster.md; SR-6/SR-8).

## 5. Interaction with the watcher model (why guards, not just "ignore the watcher")

We cannot simply ignore watcher events on the synced tree: the watcher is *advisory*
and the periodic full rescan is the source of truth (SR-11, GR-9) — it must still catch
genuine edits we missed (overflow drops). So the apply-echo must be filtered by
*content identity* (guards 2+3), not by blanket event suppression. This is why the
rescan recomputes hashes and compares to recorded state rather than trusting "did an
event fire." A blanket "mute the watcher after every apply" would risk dropping a real
concurrent user edit to the same file during the mute window — the guards avoid that by
keying on hash equality.

## 6. Test obligations

1. Receive a file, let fsnotify fire on the atomic write → assert **zero** outbound
   `INDEX_UPDATE` and the path is not re-queued as a local change (SR-8).
2. Deliver the same `INDEX_UPDATE` twice → assert exactly one write occurs and the
   second produces zero broadcasts (SR-3 idempotence).
3. Two-instance loop test: A edits `f` once → exactly one converged update, **no
   ping-pong** (bounded total broadcast count), trees converge (SR-5).
4. Genuine edit during the apply window → still detected and broadcast (proving the
   guards filter by content, not by muting the watcher).

## 7. Cross-references

- Rules: SR-3 (idempotent content-addressed apply), SR-6 (bump+broadcast on local
  authorship only), SR-8 (received file is not a local change until rescan agrees),
  SR-11/GR-9/GR-10 (watcher advisory + debounce + rescan-as-truth).
- Findings: PR-1 (`INDEX_UPDATE` is the broadcast carrier), PR-2 (`Merge` on apply, no
  `Bump`), PR-4 (delete broadcast also gated on confirmed local change);
  synthesis R-2. Decision: `protocol/vv-counter-seeding.md` (`Bump` = local-only).
- Lands in `internal/reconcile/{broadcast.go, apply.go, scanloop.go}`
  (`structure.md`).
