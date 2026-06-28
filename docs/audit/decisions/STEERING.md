# STEERING — human-in-the-loop hardening after Phase 2

- Author: orchestrator (human-in-the-loop), reviewing Segment A (Phases 0–2) output on 2026-06-28.
- Status: **strong priors for Phase 3 (critics) and Phase 4 (planner)**. These are
  defaults, not gag orders: a critic/planner MAY overturn any item below by citing
  evidence that beats the rationale given. Silence = adopt as written.
- This is the README's "tweak loop": read `findings/crossplatform/` + `findings/protocol/`
  + the `synthesis/problem-space-map.md` open-question/risk registers, then harden.

## A. Locked decisions (do NOT relitigate without new evidence)

These three are decided, evidence-backed, and binding on the plan:

1. **Chunking = fixed 32 KiB content-addressed blocks for v1; CDC deferred** behind a
   fail-closed `algo_version` field. Keep the streaming-chunk vs dedup-block
   distinction. (`decisions/merkle/chunking-fixed-32kib-vs-cdc.md`)
2. **Unicode canonical = NFC**, normalised at scan time AND on receive; keep the raw
   `os.ReadDir` name alongside the NFC key and use the raw name for all filesystem
   I/O. `golang.org/x/text/unicode/norm` is an approved dependency.
   (`decisions/crossplatform/unicode-canonical-form.md`)
3. **Transport = TLS 1.3 + trust-on-first-use device identity** (`DeviceID =
   SHA-256(cert DER)`, pinned in `VerifyConnection`). No plaintext mode.
   (`decisions/phase0/transport-security-tofu-vs-plaintext.md`)

## B. Harden these to MUST-HAVE (the top convergence/data-loss risks)

The synthesis flags these as the highest-likelihood/-impact gaps (R-1, R-5). Treat
them as required v1 design, not "nice to have":

1. **R-1 — Deterministic structural serialization (OQ-4): ADOPT RFC-6962 domain
   separation.** Pin ONE byte-exact recipe for the structural hash, identical on Mac
   and Windows: prefix `0x00` before a leaf and `0x01` before a node before hashing;
   forward-slash **NFC** path components, length-prefixed; fixed-width big-endian
   integers; children sorted by canonical name; structural hash includes
   {name, content_hash, mode, deleted, version_vector} and EXCLUDES {mtime, size}.
   Acceptance: a Mac→wire→Windows→wire→Mac round-trip test yields a bit-identical
   root. This is the single highest-probability convergence bug — make it a WS-1
   acceptance gate.
2. **R-5 — Deletion-across-restart (OQ-5): REQUIRE a persisted local-only
   last-synced tree snapshot.** Persist the tree to local state (gob is fine for
   local-only state) and load on startup so the engine can distinguish "deleted
   while the daemon was down" from "never existed." Do NOT reintroduce a
   multi-device index DB (that's the deliberately-deferred N4). Owner: tree-critic →
   WS-1/WS-4.

## C. Resolve these open questions with the synthesis lean (challengeable)

1. **OQ-2 VV counter seeding:** prefer a **pure logical `prev+1`** counter (keeps
   mtime strictly a tiebreaker, SR-4) WITH re-seed from the peer's vector via `Merge`
   on reconnect to prevent rollback after a state wipe (cheap for a 2-device LAN). If
   that re-seed guarantee can't be made to hold, adopt the hybrid `max(prev+1, now)`
   floor knowingly and document the skew caveat. Owner: protocol-critic / planner.
2. **OQ-6 tombstone retention/GC:** 2-device rule — retain a tombstone until BOTH
   peers have acknowledged it, then GC; never GC while a live peer could still carry
   a pre-delete version (the Syncthing #10590 resurrection/conflict-storm class).
3. **OQ-3 VV pruning / device-counter cleanup:** design `DropCounter`/`Compact` from
   day one, **ack-gated**, never blind time/size pruning.

## D. Scope reminder (binding deferral list)

No relays, no global/cross-subnet discovery, no GUI/REST, no multi-device index DB,
no N-device cluster, no at-rest encryption, no wire compression, no rsync rolling
delta, no CDC in v1. (`decisions/phase1/scope-boundary-vs-syncthing.md`, synthesis §2.)
The planner's deferral list must match this.
