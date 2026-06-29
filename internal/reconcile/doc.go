// Package reconcile is the Merkle Sync engine: it is the single writer of the
// in-memory tree state and drives diff -> transfer -> apply -> conflict -> tombstone
// to converge two peers' folders to identical Merkle roots (SR-5) with no data loss.
//
// # Concurrency model (GR-5 / CDD-1)
//
// One goroutine (Engine.Run's loop) owns every mutation of the reconcile core's
// in-memory state — the FileInfo set + cached tree, the per-peer last-index/ack
// state, and the apply-time expected-hash record — behind one sync.RWMutex, and does
// ZERO network/disk I/O while the lock is held (the watcher<->apply deadlock GR-5
// forbids). Outbound control messages use the transport's non-blocking, buffered-
// with-shed Conn.Send, so the loop never blocks on a peer. Bulk chunk transfer runs
// OFF the loop in per-peer puller + server goroutines (GR-3, owned and reaped on
// disconnect); the pull protocol is stop-and-wait (<=1 outstanding REQUEST per peer),
// which bounds the outbound queue so Send never sheds under normal flow and two peers
// pulling large files from each other cannot deadlock (WS-4 #11). See
// docs/audit/decisions/ws4/engine-architecture-and-backpressure.md.
//
// # Invariant index (the rules this package makes true)
//
//   - SR-1/SR-2 — atomic apply: temp -> verify whole-file SHA-256 == content_hash ->
//     fsync -> os.Rename -> parent-dir fsync; discard the temp on any error, never
//     touch dst until verify passes (transfer.go; WS-4 #3).
//   - SR-3 — idempotent content-addressed apply: a redelivered update is a literal
//     no-op (apply.go; WS-4 #4).
//   - SR-4/SR-7 — version vectors order edits; mtime is the conflict tiebreaker only;
//     the loser is renamed to a deterministic .sync-conflict copy, never deleted
//     (conflict.go; WS-4 #2/#5).
//   - SR-5 — convergence at quiescence: bit-identical roots once propagation settles.
//   - SR-6/SR-8 — bump the VV + broadcast only on confirmed LOCAL authorship; a
//     received file is filtered by content identity, never re-broadcast (broadcast.go,
//     apply.go; WS-4 #4).
//   - SR-9/SR-10 — deletions are tombstones whose bumped VV dominates a stale absent
//     counter; tombstones are GC'd only after the peer acks (tombstone.go; WS-4 #6).
//   - SR-11/GR-9/GR-10 — the watcher is an advisory debounced hint; the periodic full
//     rescan is the source of truth, also run on watcher overflow (scanloop.go,
//     watcher.go; WS-4 #10).
//   - SR-12/CDD-2 — REQUEST is validated on receipt and declined cleanly; the puller
//     clamps length <= MaxChunkLen (transfer.go; WS-4 #9).
//   - SR-13/XP-4/CDD-5 — no-clobber is enforced by the filesystem's own directory
//     listing verdict; a case/normalisation collision is refused + flagged, never
//     clobbered (transfer.go; WS-4 #7).
//
// Decisions: docs/audit/decisions/ws4/*.md. The state model + differ live in
// internal/merkle; the wire framing + version vectors in internal/protocol; the
// network layers in internal/transport (TLS) and internal/discovery (multicast).
package reconcile
