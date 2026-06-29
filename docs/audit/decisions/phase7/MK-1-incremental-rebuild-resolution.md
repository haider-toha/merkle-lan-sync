# Decision — Resolve MK-1 fixed-claim-refuted: make the "incremental rebuild" claim true

- Area: phase7 / merkle (fix agent, round 1)
- Status: decided
- Date: 2026-06-29
- Decider: Phase 7 fix agent (MK-1)
- Resolves: `docs/audit/findings/review/votes/MK-1-skeptic1.md` (Vote: **REFUTED**),
  consistent with `docs/audit/findings/review/MK-1-skeptic-3-vote.md` (NOT REFUTED, defect #1)
- Touches finding: `docs/audit/findings/merkle/MK-1-tree-construction.md`,
  review verdict `docs/audit/findings/review/MK-1.md`
- Code: `internal/merkle/{node.go,tree.go,merkle_test.go}` only (NOT `internal/reconcile`)

## Context

The MK-1 review verdict (FIXED) rests on three pillars: (1) RFC-9162 `0x00`/`0x01`
domain separation, (2) a byte-exact length-prefixed sorted big-endian grammar
identical on Mac/Windows, (3) **incremental rebuild touching only the changed leaf's
root→leaf path**. Pillars 1 and 2 are solidly implemented and tested (golden vector +
cross-platform round-trip) — both skeptics agree, and so do I (re-verified
`go test ./internal/merkle -race` PASS on 2026-06-29).

Skeptic #1 voted **REFUTED** on pillar 3 and skeptic #3 logged it as defect #1: the
claim is *factually wrong against the code*.

- `internal/merkle/node.go:56-74` `rehash()` has no notion of a "changed branch": it
  unconditionally recurses every directory and re-hashes **every** node.
- Its only caller is `BuildTree` (`tree.go:54`), which rebuilds the whole tree from a
  full `FileInfo` set. Production callers `reconcile/engine.go:290,621,652,745,798,847,874,930`
  always pass the full set. So **every change is an O(n) full rebuild + full-tree
  rehash; the claimed O(depth) incremental code path does not exist anywhere.**
- The `node.go:50-55` doc comment and the review's pillar-3 evidence
  (`review/MK-1.md:25-26`) both assert the non-existent incremental behaviour — a
  false claim in shipped code + audit trail.

The WS-1 acceptance criterion 3 is an **output property** ("one byte changed ⇒ the
root and exactly that leaf's branch differ"), which IS met and tested
(`TestOneByteChange_MinimalBranch`, two independent full builds compared). The
refutation is narrowly about the *computational* incremental-rebuild claim and the
false code comment — not a convergence-correctness defect.

Skeptic #1's stated bar to un-refute (option b): *"add the actual incremental
`UpdateLeaf` path + a cost/branch-touch assertion before claiming it."*

Skeptic #3 additionally noted three latent hardening gaps in the same files: (#2)
order-independence claimed but not directly tested; (#3) `nodeEncoding` does not bound
a component name to the `uint16` `nameLen` prefix; (#4) `BuildTree` does not reject an
empty intermediate path component (`a//b`).

## Options (scored 1-5 on correctness / concurrency-safety / testability / cross-platform)

### Option 1 — Scope-correction only (fix the comment; narrow the finding/verdict to the output property)
Correct `node.go`'s comment and the verdict prose so pillar 3 claims only the
determinism / minimal-branch *output* property (which is real and tested); add the
missing order-independence test. Keep full rebuild as the sole strategy; declare the
O(depth) incremental *computation* out of scope.
- correctness **3**: honest and the system stays correct, but it resolves a refuted
  claim by *narrowing the claim* rather than satisfying it; the capability the finding
  names ("how a folder hash recomputes on one leaf change") remains unimplemented.
- concurrency-safety **5**: no logic change.
- testability **3**: nothing new to assert for the incremental claim (no such code).
- cross-platform **5**: neutral.
- Total 16. Weakness: a future skeptic can fairly call this "defining the problem away."

### Option 2 — Implement the real copy-on-write incremental rebuild in the merkle package; fix the comment; harden; update docs [CHOSEN]
Add `Tree.Update(fi) (*Tree, error)`: a persistent/CoW upsert that re-hashes ONLY the
`fi.Path` root→leaf directory chain (depth+1 SHA-256), sharing every off-path subtree
with the old tree by pointer, returning a NEW tree and never mutating the old one.
Refactor `rehash()` to delegate its directory hash to a shared `hashFromChildren` so
the full-build and incremental paths use one identical recipe. Fix the false comment.
Fold in skeptic #3's hardening: a shared `splitPathComponents` that rejects empty and
`>0xFFFF`-byte components (used by both `BuildTree` and `Update`), plus an
order-independence test. Do NOT wire the engine (see Option 3).
- correctness **5**: makes the refuted claim TRUE and proves
  `Update == BuildTree(updated set)` (equivalence + randomized fuzz against the trusted
  full-build oracle); fixes the false comment; closes skeptic #3 #2/#3/#4.
- concurrency-safety **5**: pure CoW — returns a new tree, never mutates the shared
  immutable snapshot; off-path nodes are shared read-only (safe post-build, GR-5); no
  new locks/goroutines.
- testability **5**: the branch-touch property is asserted directly by **pointer
  identity** (off-path nodes are the same `*Node` in old and new tree; only the
  root→leaf chain is freshly allocated) — exactly the "off-path reused verbatim, only
  O(depth) recomputed" property that was previously unevidenced — plus equivalence,
  fuzz, and Windows-hostile-key Update tests.
- cross-platform **5**: pure CPU over canonical NFC keys; a Windows-hostile-key
  (`dir/a:b.txt`, `COM1.txt`, …) Update test is included.
- Total 20. Blast radius confined to `internal/merkle` (MK-1's home).

### Option 3 — Option 2 + wire the engine's single-leaf sites to `Tree.Update` (full-rebuild fallback)
Also replace `e.rebuildLocked()` with an incremental `applyLeafLocked` at the
single-leaf upsert sites (`onLocalChange`, `applyTombstone`, `handleCompletion`),
falling back to a full rebuild on any `Update` error.
- correctness **5**: makes the *system* incremental for the finding's exact scenario;
  fallback preserves correctness.
- concurrency-safety **4**: edits correctness-critical locked write paths; CoW +
  fallback are safe but the surface is larger.
- testability **4**: covered by the equivalence test + the integration suite, but adds
  re-verification burden across WS-4.
- cross-platform **5**: neutral.
- Total ~18, minus a real **evidence-integrity cost**: it churns `engine.go` line
  numbers that *accepted* Phase 6 evidence cites by `file:line`
  (`flow-verification.md` cites `engine.go:876,934,793-817,861-870,…`; `PR-6`,
  `PR-3/4/5` similarly), degrading the audit trail; and it adds WS-4 / REV-FLAKE-1
  regression risk. It is **not required** to lift the refutation — skeptic #1's bar is
  "add the path + a branch-touch assertion," met by Option 2.

## Decision

**Option 2.** Implement the copy-on-write `Tree.Update` incremental rebuild + a
branch-touch (pointer-identity) assertion and an equivalence/fuzz proof against the
full-build oracle, fix the false `rehash()` comment, fold in skeptic #3's
component-validation guards and an order-independence test, and update the MK-1 finding
+ review verdict prose to cite `Tree.Update` (not `rehash`) as the incremental
mechanism. Confine all code changes to `internal/merkle`.

## Rationale

- A *fixed-claim-refuted* item is closed most durably by making the claim **true and
  verifiable**, not by softening language (Option 1 invites a re-refutation).
- Option 2 satisfies skeptic #1's explicit un-refute bar and converts skeptic #3's
  defect #1 into tested code, while also discharging #3's #2/#3/#4.
- CoW keeps the "built node = immutable snapshot shared read-only under the reconcile
  RWMutex" invariant (`node.go:17-21`, GR-5) intact: `Update` never mutates an existing
  node, so the old snapshot a reader holds stays valid and the off-path sharing is a
  read of immutable data.
- Sharing one `hashFromChildren` recipe between the full and incremental paths makes
  byte-for-byte equivalence a structural guarantee, not a coincidence — and the fuzz
  test pins it.
- Confining changes to `internal/merkle` preserves the `engine.go` `file:line`
  evidence that other *accepted* Phase 6 findings depend on (the decisive reason to
  defer Option 3).

## Consequences

- New/changed in `internal/merkle`: `Tree.Update` + `cowUpsert` + `splitPathComponents`
  (tree.go); `rehash()` comment corrected and its directory hash delegated to a shared
  `hashFromChildren` (node.go); `BuildTree` routes through `splitPathComponents`
  (gains the empty/oversized-component guard).
- New tests (merkle_test.go): `TestUpdate_IncrementalEqualsFullBuild`,
  `TestUpdate_OffPathNodesReusedVerbatim` (pointer-identity branch-touch),
  `TestUpdate_NewPathCreatesIntermediateDirs`, `TestUpdate_FileDirConflict`,
  `TestUpdate_EquivalenceFuzz`, `TestUpdate_CrossPlatformKeys`,
  `TestBuildTree_OrderIndependent`, `TestBuildTree_RejectsMalformedPathComponents`.
- The engine keeps `BuildTree` (full rebuild from the authoritative `e.files` map) as
  its correctness-first batch strategy; `Tree.Update` is the verified O(depth)
  incremental primitive and the basis for a future watcher single-leaf fast-path —
  **deferred** (Option 3) to protect the cited `engine.go` evidence and avoid WS-4
  regression risk. If/when wired, the equivalence test is the safety oracle.
- Gate: `go build ./... && go test ./... -race` green before commit
  `fix(MK-1): <desc>`; MK-1 set to fixed with the SHA.
- Cross-refs: SR-5, WS-1 criterion 3/4, RFC 9162 §2.1.1; MK-1, MK-2.
