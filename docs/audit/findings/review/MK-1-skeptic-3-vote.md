# Skeptic #3 vote — MK-1 "FIXED" verdict

- Date: 2026-06-29
- Target: `docs/audit/findings/review/MK-1.md` (verdict FIXED), finding
  `docs/audit/findings/merkle/MK-1-tree-construction.md`, code `internal/merkle/{node,codec,tree}.go`.
- **Vote: NOT REFUTED (fixed stands)** — confidence medium.

## What I tried to break

The finding has three pillars: (1) RFC-9162 0x00/0x01 domain separation, (2) byte-exact
length-prefixed sorted big-endian grammar identical Mac/Windows, (3) incremental-rebuild
minimal-branch property.

### Pillars 1 and 2 are solidly evidenced — could not refute
- `node.go:13-14,31-48` implement `SHA-256(0x00||leafEncoding)` vs `SHA-256(0x01||nodeEncoding)`.
- `codec.go:30-65` pin the grammar; `TestStructuralHash_GoldenVector` reconstructs both
  encodings byte-for-byte AND pins stable hex (`goldenLeafHex`/`goldenNodeHex`), so a silent
  recipe drift fails closed. `TestCrossPlatformRoot_RoundTrip` proves a Windows-hostile key set
  yields a bit-identical root after a Mac->wire->Windows->wire->Mac trip.
- Reproduced fresh 2026-06-29: `go test ./internal/merkle -race` → all five named tests PASS.
These are the substance of the finding ("the one spec change vs Phase 0" = domain separation),
and they are genuinely closed.

## Defects found (do NOT overturn FIXED, but should be recorded)

1. **The review's pillar-3 evidence misstates the code.** Review says: *"Incremental rebuild
   reuses off-path sibling hashes: node.go:56-74 rehash() recurses and re-hashes only along the
   changed branch."* This is false. `Node.rehash()` (node.go:56-74) unconditionally re-hashes the
   ENTIRE subtree it is called on. `BuildTree` (tree.go:54) always rebuilds the whole tree from the
   full FileInfo set and calls `root.rehash()` on the root. Production callers
   `reconcile/engine.go:290,641-642` always pass the full set — there is **no incremental rebuild
   code path anywhere in the repo**. Every change is a full O(n) rebuild + full-tree rehash.
   The *output* property (one byte change ⇒ exactly that branch's hashes differ) is real and is
   tested by `TestOneByteChange_MinimalBranch` (it compares two independent full builds), which is
   what WS-1 criterion 3 actually requires — so the acceptance criterion is met. But the verdict's
   prose claims an incremental computation that does not exist; the "incremental rebuild" pillar is
   delivered only as a determinism/locality output property, not as the O(depth) recompute the
   finding describes.

2. **Order-independence is claimed but not directly tested.** `BuildTree` doc and finding claim
   "the same set always yields the identical root regardless of input order." `TestScanTwice_*`
   scans the same directory twice (deterministic scanner order), so it does not exercise shuffled
   input. The property holds by construction (map-keyed children + sort in rehash), but there is no
   test feeding a permuted FileInfo slice.

3. **`nodeEncoding` does not bound the component name length.** `codec.go:57` writes
   `uint16(len(c.name))`; a path component whose UTF-8 byte length exceeds 65535 would truncate the
   length prefix and create an ambiguous/colliding encoding. `EncodeFileInfo` guards the wire path
   at 0xFFFF, and a local scan component is in practice bounded, so this is latent, not live. No
   test.

4. **`BuildTree` does not reject empty intermediate path components.** A non-canonical key like
   `a//b` splits to `["a","","b"]` and silently creates a child keyed `""`; the only guard is the
   upstream canonicaliser. No defense-in-depth test at the tree layer.

## Conclusion

The correctness substance of MK-1 (domain separation + byte-exact deterministic grammar +
cross-platform identical root + minimal-branch output property) is implemented and tested with
golden vectors and a cross-platform round-trip, passing under -race. The verdict is sound on
correctness. The "incremental rebuild" wording overstates the implementation (full rebuild, not
incremental), and items 2-4 are untested edge cases — hardening gaps, not correctness defects.
Refuted = false.
