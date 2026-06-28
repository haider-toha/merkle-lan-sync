# Proposed code structure — internal/ + cmd/ + test/

Phase 0 proposed layout. For **each file**: a one-line purpose and the
**workstream / finding / decision** that creates it. The planner (Phase 4)
refines acceptance criteria; implementers (Phase 5) may split a file when it
grows, but the package boundaries and dependency direction below are the
contract.

Workstreams (plan/agent_roster.md): **WS-1** Merkle tree + scanner + pathnorm ·
**WS-2** Transport (TCP framing + TLS) · **WS-3** Discovery (UDP multicast) ·
**WS-4** Reconciliation (diff + chunk stream + conflict). Module:
`github.com/haider-toha/merkle-sync` (`go 1.23`).

---

## Dependency DAG (acyclic — enforced)

```
pathnorm ─┐
          ├─► merkle ─► protocol(codec users) ─┐
protocol ─┘                                     │
   ▲  ▲                                          ├─► reconcile ─► cmd/msync
   │  └──────────── transport ──────────────────┤
   └───────────────── discovery ────────────────┘
```

- **`internal/protocol`** is a foundational leaf (stdlib only). It owns the
  **wire framing**, the **message-type catalogue**, and the two shared identity
  types — **`VersionVector`** and **`DeviceID`** — because both are needed by the
  state model (`merkle`) *and* the network (`transport`, `discovery`). The roster
  pins "version-vector types" into `protocol`; keeping `DeviceID` and `VersionVector`
  here (and `FileInfo` in `merkle`, which imports `protocol`) is the placement that
  keeps the graph acyclic: `protocol` never imports `merkle`. This is the one
  structural refinement of the roster's rough sketch, logged inline here.
- **`internal/pathnorm`** is the other leaf (canonical path/Unicode/case rules).
- Everything else layers upward; **`reconcile`** is the only package that mutates
  tree state and is the single writer behind the `RWMutex` (GR-5).

Cross-cutting rules these files must honour live in
`docs/audit/rules/{go-rules,sync-rules,crossplatform-rules}.md`. Phase 0
decisions they implement live in `docs/audit/decisions/phase0/`.

---

## internal/protocol/ — wire framing + identity types (foundational)

| file | purpose | created by |
|---|---|---|
| `doc.go` | package overview; framing + identity contract | WS-2 |
| `framing.go` | `WriteFrame`/`ReadFrame`: `[4-byte BE len][1-byte type][payload]`, `MaxFrameLen` guard, `io.ReadFull` | WS-2 · decisions/phase0/framing-format.md · SR-12 · GR-8 |
| `framing_test.go` | partial-read reassembly (`iotest.OneByteReader`), oversized-length rejection, header fuzz | WS-2 |
| `messages.go` | message-type constants + envelope structs (HELLO, INDEX, INDEX_UPDATE, REQUEST, RESPONSE, PING, CLOSE) | WS-2 · protocol-researcher (Phase 2 finalises the ~7-type catalogue) |
| `messages_test.go` | per-type encode/decode round-trip | WS-2 |
| `versionvector.go` | `VersionVector` type + `Bump`, `Compare` (dominates / dominated / concurrent), `Merge` | WS-1 (needed early) · literature/version-vectors · decisions/phase0/merkle-leaf-shape.md · SR-4 |
| `versionvector_test.go` | table-driven causal vs concurrent vs dominated | WS-1 |
| `deviceid.go` | `DeviceID` type + `DeviceIDFromCert` (SHA-256 of cert DER) + human encoding | WS-2 · decisions/phase0/transport-security-tofu-vs-plaintext.md |
| `deviceid_test.go` | derivation determinism + encoding round-trip | WS-2 |

## internal/pathnorm/ — canonical path / Unicode / case (foundational)

| file | purpose | created by |
|---|---|---|
| `doc.go` | package overview; canonical-form contract | WS-1 |
| `pathnorm.go` | `Canonicalize` (forward-slash, relative), `ToOSPath`/`FromOSPath` boundary conversion | WS-1 · crossplatform-rules XP-1 · GR-12 |
| `normalize.go` | Unicode NFC normalisation (`golang.org/x/text/unicode/norm`) | WS-1 · XP-2 · crossplatform-researcher (Phase 2 confirms NFC) |
| `windows.go` | Windows-unsafe detection + escape/reject: reserved chars/names, ctrl chars, trailing dot/space, MAX_PATH | WS-1 (stub) · XP-3 (hardened Phase 2) |
| `casefold.go` | case-folding + collision-index helpers (`File.txt` vs `file.txt`) | WS-1 · XP-4 (hardened Phase 2) |
| `pathnorm_test.go` | table-driven **Windows-hostile name set** round-trip without loss | WS-1 · plan acceptance · SR-13 |

## internal/merkle/ — state model: leaves, tree, scanner, diff

| file | purpose | created by |
|---|---|---|
| `doc.go` | package overview; tree-as-source-of-truth | WS-1 |
| `fileinfo.go` | `FileInfo{path, content_hash, size, mode, mtime, version_vector, deleted}` | WS-1 · decisions/phase0/merkle-leaf-shape.md |
| `node.go` | `Node` (file/dir) + structural-hash composition (content_hash+mode+deleted+VV; **excludes** raw mtime/size) | WS-1 · merkle-leaf-shape decision · merkle-researcher (Phase 2) |
| `tree.go` | build tree from `FileInfo` set; `RootHash`; lookup; canonical serialisation for hashing | WS-1 |
| `scanner.go` | walk root, SHA-256 each file, produce `FileInfo` set; per-dir walk feeding watch-set | WS-1 · SR-11 · GR-9 |
| `differ.go` | diff two trees; recurse only into mismatching branches (O(log n)) | WS-1 (consumed by WS-4) · merkle-researcher (Phase 2) |
| `hash.go` | content-hash helper (SHA-256, buffered read) | WS-1 |
| `codec.go` | deterministic `FileInfo` ⇄ bytes (big-endian, forward-slash, fixed widths) — identical on Mac/Windows | WS-1/WS-2 · merkle-leaf-shape consequences |
| `merkle_test.go` | same folder twice ⇒ identical root; one byte changed flips exactly that leaf's branch + root | WS-1 · plan acceptance · SR-5 |
| `scanner_test.go` | scanner determinism; tombstone-on-delete; mode capture | WS-1 |
| `differ_test.go` | diff correctness + minimal-recursion property | WS-1 |

## internal/transport/ — TLS + framed TCP connections

| file | purpose | created by |
|---|---|---|
| `doc.go` | package overview; TLS-TOFU contract | WS-2 |
| `identity.go` | generate/load per-device keypair + self-signed cert; persist under config dir | WS-2 · transport-security decision |
| `tls.go` | `tls.Config` (TLS 1.3, self-signed) + `VerifyConnection` pinning `DeviceID` against allow-list | WS-2 · transport-security decision |
| `conn.go` | wrap `tls.Conn`; per-conn reader + writer goroutines; frame loop via `protocol`; ctx-cancel/close | WS-2 · GR-3 · GR-4 · GR-8 |
| `listener.go` | TCP `Accept` loop; spawn per-conn handlers; `WaitGroup`; graceful shutdown on ctx | WS-2 · GR-3 · GR-4 |
| `dial.go` | dial a discovered peer; handshake; pin identity | WS-2 |
| `transport_test.go` | split-frame survival; malformed length dropped without corrupting stream; TLS handshake pins identity; wrong fingerprint rejected; **goroutine-leak-on-disconnect** | WS-2 · plan acceptance · GR-3 |

## internal/discovery/ — UDP multicast peer registry

| file | purpose | created by |
|---|---|---|
| `doc.go` | package overview; discovery-as-hint contract | WS-3 |
| `multicast.go` | UDP multicast socket (join group), send/recv | WS-3 |
| `announce.go` | periodic announce `{DeviceID, addr, port}` ticker goroutine | WS-3 |
| `registry.go` | peer registry: add on announce; heartbeat eviction timeout | WS-3 · plan acceptance |
| `discovery.go` | orchestrator: listener + announce goroutines, ctx shutdown, emits `peerEvents` channel | WS-3 · GR-4 |
| `discovery_test.go` | second instance discovered within announce interval; silent peer evicted after heartbeat timeout | WS-3 · plan acceptance |

## internal/reconcile/ — the engine (diff → transfer → conflict → tombstone)

| file | purpose | created by |
|---|---|---|
| `doc.go` | package overview; invariant index (SR-1..SR-13) | WS-4 |
| `engine.go` | core loop: owns `RWMutex` + tree; single writer; consumes `fsChanges`/`peerEvents`/`inboundMsgs` | WS-4 · GR-5 |
| `watcher.go` | fsnotify wrapper: per-dir watches, watch-set reconcile, `Errors`→rescan | WS-4 · GR-9 |
| `scanloop.go` | debounce events (~150 ms), settled-change detection, periodic rescan | WS-4 · GR-10 · SR-11 |
| `broadcast.go` | broadcast hash **only after confirmed local change**; VV bump on local authorship only | WS-4 · SR-6 · SR-8 |
| `apply.go` | apply received `FileInfo`: idempotent, content-addressed; record expected hash to break echo loop | WS-4 · SR-3 · SR-8 |
| `transfer.go` | request/stream chunks; **temp-write → fsync → atomic rename → dir fsync** | WS-4 · SR-1 · SR-2 |
| `conflict.go` | concurrent-edit detection (VV); `.sync-conflict-<date>-<time>-<deviceID>` naming; **loser renamed, never deleted**; mtime→deviceID tiebreak | WS-4 · SR-7 |
| `tombstone.go` | deletions as tombstones; apply remote tombstone; **resist resurrection by stale peer**; retention/GC | WS-4 · SR-9 · SR-10 |
| `reconcile_test.go` | convergence; conflict no-loss; killed-transfer no-corrupt; no echo loop; deletion no-resurrect | WS-4 · plan acceptance |

## cmd/msync/ — daemon entrypoint

| file | purpose | created by |
|---|---|---|
| `main.go` | flags (`-dir`, `-port`); `signal.NotifyContext` root ctx; wire all subsystems; graceful shutdown | exists as pre-flight stub → WS-4 wiring · GR-2 |

## test/integration/ — two-instance, same-machine scenarios

| file | purpose | created by |
|---|---|---|
| `helpers.go` | spin up two in-process instances on loopback with temp dirs; assert-converged helper | Phase 6 (evidence-generator) |
| `converge_test.go` | divergent folders → identical root hashes (`TestTwoNodeConverge`) | Phase 6 · SR-5 |
| `conflict_test.go` | simultaneous edits → `.sync-conflict` copy, neither version lost | Phase 6 · SR-7 |
| `deletion_test.go` | delete propagates as tombstone; stale peer does not resurrect | Phase 6 · SR-9 · SR-10 |
| `transfer_test.go` | transfer killed mid-stream → no corrupt file, temp discarded | Phase 6 · SR-1 |
| `loop_test.go` | received file → **zero** outbound hash broadcasts | Phase 6 · SR-6 · SR-8 |

---

## Phase 0 decisions referenced by this layout

- `docs/audit/decisions/phase0/framing-format.md` — `[4-byte BE len][1-byte type][payload]` + `MaxFrameLen` guard → `internal/protocol/framing.go`.
- `docs/audit/decisions/phase0/transport-security-tofu-vs-plaintext.md` — TLS 1.3 self-signed + device-ID TOFU pinning → `internal/transport/{tls,identity}.go`, `internal/protocol/deviceid.go`.
- `docs/audit/decisions/phase0/merkle-leaf-shape.md` — `FileInfo` = hash+size+mode+mtime+version-vector+tombstone; structural hash excludes mtime/size → `internal/merkle/{fileinfo,node,tree}.go`, `internal/protocol/versionvector.go`.

## Supporting non-code artifacts (created later, noted for completeness)

`CLAUDE.md` (hard-rule contract + build/test/cross-compile/add-a-feature),
`.claude/skills/merkle-sync/SKILL.md` (distilled diff algorithm + VV scheme +
framing spec + path rules), and `.claude/agents/*.md` (per-role contracts) are
named in plan/agent_roster.md. They are documentation, not part of the
`internal/`+`cmd/`+`test/` code layout, and are out of scope for this file.
`.github/workflows/ci.yml` already exists (ubuntu/macos/windows matrix);
Phase 6 refines it.
