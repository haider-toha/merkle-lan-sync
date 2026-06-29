# Finding MK-1 — Merkle tree construction: leaf/node hashing, domain separation, incremental rebuild

- Slug: `MK-1-tree-construction`
- Phase / role: Phase 2 — merkle-researcher
- Status: **fixed** (WS-1 + Phase-7 round 1) — implemented + verified in
  `internal/merkle/{node.go,codec.go,tree.go}`: RFC-6962 `0x00`/`0x01` domain
  separation, the byte-exact grammar, n-ary no-duplicate-last, and the
  one-byte-change minimal-branch property. Golden-vector + cross-platform-root tests
  green. Decision `docs/audit/decisions/ws1/structural-hash-grammar-finalization.md`.
  Commit `182ff00a16868df05377cb3585b914aa1d59784e`. (Originally: complete; backs
  `decisions/merkle/leaf-shape-and-structural-hash.md`.)
- **Phase-7 update (2026-06-29):** the *incremental rebuild* pillar (below) was
  refuted at review — `rehash()`/`BuildTree` do a full O(n) recompute; the claimed
  O(depth) path did not exist (`docs/audit/findings/review/votes/MK-1-skeptic1.md`,
  REFUTED; corroborated by skeptic-3 defect #1). Resolved by implementing the real
  copy-on-write `merkle.Tree.Update` (re-hashes only the changed leaf's root→leaf
  chain, shares off-path subtrees by pointer; byte-for-byte equal to a full build via
  the shared `hashFromChildren` recipe). Proven by `TestUpdate_OffPathNodesReusedVerbatim`
  (pointer-identity branch-touch) + `TestUpdate_EquivalenceFuzz`/`...IncrementalEqualsFullBuild`,
  with skeptic-3 #2/#3/#4 hardening (`TestBuildTree_OrderIndependent`,
  `TestBuildTree_RejectsMalformedPathComponents`). Decision
  `docs/audit/decisions/phase7/MK-1-incremental-rebuild-resolution.md`. Commit
  `920290f3354e28ada401579d54d17bbfd305829f`. All green under
  `go build ./... && go test ./... -race` + `GOOS=windows` build.
- Severity: **medium** (the domain-separation gap is a real, cheap-to-close
  hardening of the Phase 0 spec; the construction itself is foundational to SR-5)
- Date / access date for all URLs: 2026-06-28
- Reads-first honoured: `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md`,
  `docs/audit/findings/literature/{merkle-tree,cdc-chunking}.md`,
  `docs/audit/decisions/phase0/merkle-leaf-shape.md`,
  `.claude/skills/merkle-sync/SKILL.md`

## Claim

A directory's hash derives **only** from its direct children's `(name,
structuralHash)` pairs, recursively, so the root hash commits to the whole tree and
a single leaf change re-hashes exactly the O(depth) nodes on its root→leaf path —
**provided** the hashing uses RFC-6962 leaf-vs-node **domain separation** (`0x00`
leaf / `0x01` node, *required for second-preimage resistance*) and a **byte-exact,
length-prefixed, sorted, big-endian** serialization identical on Mac and Windows.
The current SKILL/Phase-0 recipe omits domain separation and leaves the byte grammar
unpinned; this finding closes both (OQ-4).

## Evidence

### The git/Merkle directory-hash property (what we adopt)

A directory hash computed over its children's hashes gives the recursive
root-commitment and the identical-subtree dedup: *"a tree's hash reflects its
complete directory structure and file contents recursively"* and *"if two
directories have identical contents, they produce identical hashes and share a
single tree object"* (dev.to, *Git Internals Part 1*,
https://dev.to/calebsander/git-internals-part-1-the-git-object-model-474m, accessed
2026-06-28; folded in `literature/merkle-tree.md` §2.2, §3.1). Git commits the
child **name** in the *tree* (parent) entry, and the *blob* id is content-only — so
identical content at different names still dedups to one blob. We mirror this:
**name committed by the parent child-entry, not by the leaf's own hash**
(`decisions/merkle/leaf-shape-and-structural-hash.md` §D.3).

### Domain separation is *required*, not optional (the one spec change vs Phase 0)

Verified first-hand against the RFC (RFC 9162 §2.1.1, Certificate Transparency v2.0,
https://datatracker.ietf.org/doc/html/rfc9162, accessed 2026-06-28):

```
MTH({})     = HASH()
MTH({d0})   = HASH(0x00 || d0)                              # leaf
MTH(D_n)    = HASH(0x01 || MTH(D[0:k]) || MTH(D[k:n]))      # node, k = largest power of two < n
```

with the normative statement: *"the hash calculations for leaves and nodes differ;
this domain separation is required to give second preimage resistance."* Without
it, *"an adversary claims that an internal node hash is a leaf"* and forges a
different tree with the same root (er4hn, *Second Preimage Attack against Merkle
Trees*, https://er4hn.info/blog/2022.10.08-second_preimage_on_merkle_tree/, accessed
2026-06-28; `literature/merkle-tree.md` §4.1, AL-20). The Phase-0 recipe
(`SKILL.md:72-76`, `merkle-leaf-shape.md:104-106`) relies only on the differing
*field layout* of a leaf vs a node to keep them distinct — fragile. The fix is one
byte: `SHA-256(0x00 || leafEncoding)` vs `SHA-256(0x01 || nodeEncoding)`.

### Non-deterministic serialization is the highest-probability convergence bug

`literature/merkle-tree.md` §4.3 names ambiguous/unsorted/variable-width
serialization "the highest-probability convergence bug," worsened because we are
*literally* Mac↔Windows (NFD-vs-NFC, `/` vs `\` — XP-1/XP-2). Git guards the
analogous case by sorting entries *"otherwise the representation of a tree would not
be unique"* (dev.to, accessed 2026-06-28). The fix is a fixed grammar: sorted
children (plain bytewise compare of canonical NFC name bytes), `uint16`/`uint32`
length prefixes, big-endian fixed-width integers, raw 32-byte hashes — pinned byte
for byte in `decisions/merkle/leaf-shape-and-structural-hash.md` §D.3.

### Incremental rebuild — how a folder hash recomputes on one leaf change

Because a node hash depends only on its **direct** children, a leaf change
propagates only up its own path: re-hash the changed leaf, then re-serialize and
re-hash each ancestor directory (reusing every off-path sibling hash verbatim), to
the root. Honest cost: `O(Σ depths of changed paths)` node re-hashes, **not** `O(n)`
and **not** a strict `O(log n)` — a directory hierarchy is unbalanced (depth = FS
nesting `D`, unrelated to `log N`) (`literature/merkle-tree.md` §4.5, §5; the SKILL
`~O(d+k)` shorthand is optimistic). This is the same prune property applied to
*rebuild* rather than *diff* (see `MK-2`). Directory **rename** re-parents a subtree
and changes every ancestor hash — the Dynamo "key ranges change" analogue
(`merkle-tree` §4.6); handled by `decisions/merkle/rename-detection.md`.

> **Implementation (Phase-7):** this exact O(depth) recompute is `merkle.Tree.Update`
> (`internal/merkle/tree.go`): a copy-on-write upsert that re-hashes only the changed
> leaf's root→leaf chain and reuses every off-path subtree verbatim (shared by
> pointer), returning a new immutable tree. `rehash()`/`BuildTree` remain the O(n)
> full-build path (the engine's batch strategy over the authoritative FileInfo map);
> both paths share one `hashFromChildren` recipe so `Update` is byte-identical to a
> full build. See `decisions/phase7/MK-1-incremental-rebuild-resolution.md`.

### Odd-node duplication (CVE-2012-2459) — why we stay n-ary

Our directory tree is **n-ary and never pads to power-of-two**, so it is not exposed
to the Bitcoin duplicate-last-node collision (CVE-2012-2459;
https://bitcoinops.org/en/topics/merkle-tree-vulnerabilities/, accessed 2026-06-28;
`merkle-tree` §4.2). The binding lesson: never duplicate a node to fill a level, and
if a *binary* chunk-subtree is ever added under `content_hash` (deferred CDC), it
MUST use the RFC-9162 largest-power-of-two split, never duplicate-last.

## Recommendation / impact

- **ADOPT** the git directory-hash property + RFC-6962 domain separation + the
  byte-exact grammar (all pinned in the leaf-shape decision). SHA-256 throughout
  (not git's SHA-1; `merkle-tree` §4.7).
- **Implementers:** `internal/merkle/{node.go,tree.go,codec.go}`. Test with a golden
  vector for `leafEncoding`/`nodeEncoding`, the SR-5 "one byte ⇒ exactly that branch
  + root" property, and the SR-13 Mac→wire→Windows→wire→Mac identical-subtree-hash
  round-trip.
- **Cross-refs:** SR-5, SR-13, XP-1/2, GR-12; AL-1, AL-20.
