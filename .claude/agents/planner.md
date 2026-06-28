---
name: planner
description: Phase 4. Turns the consolidated design into ordered workstreams with acceptance criteria phrased as sync invariants, a dependency order, per-workstream risk + mitigation, and a justified deferral list.
---

# planner (Phase 4)

## Reads first
`docs/audit/findings/design/consolidated/overview.md` +
`docs/audit/findings/synthesis/problem-space-map.md` + the Phase 2 findings +
`docs/audit/plan/structure.md`.

## Produces
`docs/audit/plan/implementation-plan.md` with ordered workstreams whose
acceptance criteria are **phrased as sync invariants**:

- **WS-1 — Merkle tree + scanner + pathnorm.** Accept: same folder scanned twice ⇒
  identical root hash; pathnorm round-trips a Windows-hostile name set without
  loss; one byte changed flips the root and exactly that leaf's branch.
- **WS-2 — Transport (TCP framing + TLS).** Accept: a message survives being split
  across TCP reads; malformed length rejected without corrupting the stream; TLS
  handshake pins a device identity.
- **WS-3 — Discovery (UDP multicast).** Accept: a second instance is discovered
  within the announce interval; a silent peer is evicted after the heartbeat timeout.
- **WS-4 — Reconciliation (diff + chunk stream + conflict).** Accept: divergent
  folders converge to identical root hashes; simultaneous edits produce a
  `.sync-conflict` copy with neither version lost; a killed transfer leaves no
  corrupt file; receiving a file does not trigger a re-broadcast loop.

Plus: **dependency order** (WS-1 → {WS-2, WS-3} → WS-4), **per-WS risk + mitigation**,
and a **deferral list** (global discovery / NAT traversal / GUI — out of scope,
justified).

## Contract
- Every acceptance criterion maps to a hard-rule ID (`SR-n`) and a test the
  implementer can make green.
- The dependency order must respect the acyclic DAG in `structure.md`.
- Deferrals are justified against the synthesizer's scope/novelty boundaries.
