# Skeptic #1 vote — MK-1 "FIXED" verdict

- Date: 2026-06-29
- Vote: **REFUTED** (confidence: medium)
- Target: `docs/audit/findings/review/MK-1.md` (Verdict: FIXED)

## Where the verdict is solid (not contested)
- Domain separation: real and tested. `internal/merkle/node.go:12-15,31-48`;
  `TestStructuralHash_GoldenVector` (`merkle_test.go:127`) rebuilds the grammar and
  pins `SHA-256(0x00||leaf)` / `SHA-256(0x01||node)` hex. Verified PASS under -race.
- Byte-exact grammar: `internal/merkle/codec.go:30-65`, big-endian, length-prefixed,
  golden-vector pinned. Sorted children bytewise (`node.go:65`). PASS.
- Cross-platform root value-equality: `TestCrossPlatformRoot_RoundTrip` PASS.

## Why I still vote REFUTED — pillar 3 ("incremental rebuild") is misrepresented
The verdict lists three pillars; the third is "incremental rebuild touching only the
changed leaf's root->leaf path," evidenced (review MK-1, lines 25-26) as:

> "Incremental rebuild reuses off-path sibling hashes: node.go:56-74 rehash()
>  recurses and re-hashes only along the changed branch."

This is factually wrong against the code:
- `node.go:56-74` `rehash()` has **no notion of a "changed branch."** It
  unconditionally recurses every directory and re-hashes **every** node
  (`for i, name := range names { c.rehash() ... }`, line 68-72).
- Its **only caller** is `BuildTree` (`tree.go:54`), which builds a fresh tree from
  scratch on each call. `grep -rn rehash internal/merkle` shows no other caller and
  no `Update`/incremental API. So every `BuildTree` is O(n) re-hash of all nodes, not
  the claimed O(depth) root->leaf path.

The finding's own §"Incremental rebuild" (MK-1-tree-construction.md:80-91) explicitly
describes an algorithm that "re-hash[es] the changed leaf, then ... each ancestor
directory (reusing every off-path sibling hash verbatim)" at cost "O(Σ depths of
changed paths), not O(n)". **That code path does not exist.** scanner.go:76 only
refers to WS-4's *rescan* pre-filter, not an in-tree incremental rehash.

## Why the test does not rescue the claim
`TestOneByteChange_MinimalBranch` (`merkle_test.go:89`) builds **two fully
independent** trees and asserts off-path node hashes are byte-identical between them.
That proves the *value* property of SR-5 (determinism: same child bytes -> same hash)
— which is genuine and good — but it neither exercises nor asserts any incremental
rebuild, and nothing tests the O(depth) cost claim. The verdict conflates "off-path
hashes have equal VALUES across two full builds" with "rehash reuses off-path hashes
to avoid recomputation." Only the former is true/tested.

## Calibration
For convergence *correctness*, full rebuild is fine (arguably safer), and the
hashing/domain-sep/grammar pillars are solidly evidenced. But the FIXED verdict
asserts three properties as implemented + verified, and one of them is supported by an
inaccurate code citation and an absent code path. Per the skeptic mandate ("default
refuted=true if the claim is not solidly evidenced"), the incremental-rebuild pillar
is not solidly evidenced. Recommend the verdict either (a) drop/scope-out the
incremental-rebuild claim and correct lines 25-26, or (b) add the actual incremental
`UpdateLeaf` path + a cost/branch-touch assertion before claiming it.
