// Package protocol is the stdlib-only foundational leaf of Merkle Sync. It owns
// the on-wire framing, the message-type catalogue and codec, and the two shared
// identity types — VersionVector and DeviceID — that both the state model
// (internal/merkle) and the network layers (internal/transport,
// internal/discovery) depend on. It never imports internal/merkle, which keeps
// the package dependency graph acyclic (see docs/audit/plan/structure.md).
//
// # Framing
//
// Every message travels in a frame:
//
//	+-------------------+--------------+-----------------------+
//	| length: uint32 BE | type: 1 byte | payload: (length-1) B |
//	| = 1 + len(payload)|  (MsgType)   |                       |
//	+-------------------+--------------+-----------------------+
//
// length counts the type byte plus the payload, so an empty-payload PING has
// length == 1. All multi-byte integers are big-endian so Mac and Windows agree
// byte-for-byte. ReadFrame validates 0 < length <= MaxFrameLen BEFORE allocating
// the body and reads both header and body with io.ReadFull, so a partial TCP
// read never desyncs the stream and a bogus length is a typed error
// (ErrFrameTooLarge / ErrZeroLength), never an unbounded allocation. The sender
// asserts the complementary budget: WriteFrame rejects an over-MaxFrameLen frame
// and the RESPONSE builder rejects an over-MaxChunkLen chunk, so a budget bug
// fails loudly on the offender instead of dropping the victim peer (CDD-2). See
// docs/audit/decisions/phase0/framing-format.md, rules SR-12 / GR-7 / GR-8.
//
// # Message catalogue
//
// Seven frozen types (0x01 HELLO .. 0x07 CLOSE); 0x00 is reserved-invalid and
// 0x08+ is unassigned. The unknown-type policy is total and split: 0x00 is fatal
// (drop the connection), a known type is dispatched, and 0x08+ is skipped (the
// length prefix makes the payload safe to discard, preserving forward-compat).
// See docs/audit/decisions/protocol/message-type-enumeration.md and PR-1.
//
// The INDEX / INDEX_UPDATE payload carries the wire FileInfo grammar, which is
// finalized in WS-1 (internal/merkle); this package treats that region as an
// opaque Body so the envelope round-trips without fixing the per-entry grammar.
//
// # Identity types
//
// DeviceID is SHA-256(certificate DER); its high 64 bits, Short(), are the
// version-vector counter key (PR-7). VersionVector is the per-file causal clock:
// Compare classifies two histories as Equal / Dominates / DominatedBy /
// Concurrent, Merge takes the pointwise max, and Bump is a pure prev+1 — all
// copy-on-write so an immutable snapshot can be shared under the reconcile
// RWMutex without aliasing (PR-2, GR-5).
package protocol
