# Skeptic #1 vote — MK-2 "fixed" verdict

- Date: 2026-06-29
- Target: `docs/audit/findings/review/MK-2.md` (verdict FIXED)
- Finding: `docs/audit/findings/merkle/MK-2-diff-reconciliation.md`
- Code: `internal/merkle/differ.go`, tests `internal/merkle/differ_test.go`
- **Vote: REFUTED (the "fixed" claim is over-evidenced on test coverage)**

## What holds up (not disputed)
- Prune-equal is real and load-bearing-tested: `differ.go:62-66` plus the white-box
  comparison counter (`differ.go:26-43`) and `TestDiff_PrunesEqualSubtrees`
  (`differ_test.go:27`, asserts `== 3`) make a hollow recurse-everything impl fail.
- "Absence is ambiguous → single-sided candidate" is enforced structurally
  (`emit()` `differ.go:45-54`; `TestDiff_SingleSidedCandidate` `differ_test.go:67`).
- Concurrency / zero-I/O-held claim is sound: the engine snapshots under `RLock`
  (`engine.go:633-635`), `FileInfo` is value-copied, and `VersionVector` is
  copy-on-write (`internal/protocol/versionvector.go:55-59`), so `BuildTree`+`Diff`
  run on private trees after `RUnlock`. A diff-vs-watcher race is therefore
  structurally impossible — the missing explicit concurrent unit test is moot.

## Why I still refute the verdict
1. **The file-vs-directory divergence is an untested, reachable code path.**
   `differ.go:78-87` handles the case where the same path is a FILE on one side and
   a DIRECTORY on the other (emit the file as a single-sided candidate, then recurse
   the directory side's children). This branch is NOT covered by any test in
   `differ_test.go` — every test uses files-only or files-vs-absent. A hollow or
   wrong file-vs-dir handler would pass the entire suite green.

2. **This path is not even in the finding's own specification.** The MK-2 pseudocode
   ("The algorithm (specification)") only enumerates `both file leaves` and `both
   directories`; the mixed file↔dir case is an implementation addition with no spec
   and no test. The review (`review/MK-2.md`) cites `differ.go:74-87` "emits
   file-vs-dir / single-sided as candidates" as *evidence of correctness*, but
   conflates it with the single-sided tests — there is no test asserting file-vs-dir
   output. The review's "No gap found" is therefore unsupported for this branch.

3. **It is the highest-stakes branch for this project.** A path being a file on the
   Mac and a directory on Windows (or after a delete+recreate-as-dir) is exactly the
   cross-platform / structural-divergence scenario the engine exists to survive
   without data loss. Leaving the differ's behaviour here unverified means the
   FIXED verdict rests on the *least* exercised, *most* dangerous code path.

## What would flip me to "fixed"
A table-driven test in `differ_test.go` exercising: local file `x` vs remote dir
`x/{a,b}` (and the symmetric case), asserting `x` is emitted local-only and `x/a`,
`x/b` remote-only, with the comparison count proving the recursion is minimal.

## Severity of my objection
Medium. The load-bearing diff properties are genuinely proven; the gap is a real,
reachable, cross-platform-critical branch claimed-as-covered but untested — enough
to deny a clean "fixed" until a file-vs-dir test exists.
