# Skeptic vote #2 — MK-2 "fixed" verdict

- Vote date: 2026-06-29
- Skeptic role: skeptic #2 of 3, refuting the Phase 6 "FIXED" verdict on MK-2
- Finding under review: `docs/audit/findings/merkle/MK-2-diff-reconciliation.md`
- Review verdict challenged: `docs/audit/findings/review/MK-2.md` (FIXED)
- **Vote: REFUTED (medium confidence)**

## What holds up

The load-bearing prune/recurse claims are genuinely well evidenced and tested:
- Prune-equal-at-top-of-call: `internal/merkle/differ.go:63`.
- White-box prune assertion `TestDiff_PrunesEqualSubtrees` (`differ_test.go:27`) asserts
  exactly 3 comparisons — a hollow implementation that recursed the equal `big/`
  subtree would report ≥8. This is a strong, non-gameable proof.
- Single-sided candidate (no pre-classification): `differ_test.go:67`, `:91`.
- All `internal/merkle` tests pass under `-race` (`docs/audit/runs/race-all.log:6`).

I do not dispute the prune-equal efficiency property or the simple single-sided
candidate behaviour. Those are solid.

## Why I still refute: the file-vs-directory divergence path is untested AND emits a materially false candidate

`diffNodes` contains a distinct branch for when one side is a FILE leaf at a path and
the other side is a DIRECTORY at the same path (`internal/merkle/differ.go:80-87`):

```go
if lLeaf { emit(out, path, l, nil) }   // local file
if rLeaf { emit(out, path, nil, r) }   // remote file
// ... then recurse the directory side's children
```

This is a real cross-tree scenario: local has `foo` as a file, the peer has `foo` as a
directory (the classic "deleted a file, created a dir of the same name", or vice
versa). Within one tree `BuildTree` forbids it (`tree.go:36-37,49` `ErrTreeConflict`),
but two independently-built trees legitimately disagree on the type at a path, and the
differ must walk it.

### Evidence — reproduced behaviour (temp test, run 2026-06-29, then removed)
With local `= {foo (file)}` and remote `= {foo/bar.txt}`, `Diff` returns:

```
Path="foo"          Local=true  Remote=false
Path="foo/bar.txt"  Local=false Remote=true
```

The `Path="foo"` entry carries **`Remote=nil`** — but the remote is **not** absent at
`foo`; it has a *directory* there with content. The differ reports "remote is absent at
foo" when that is materially false.

### Why this matters (correctness, not cosmetics)
1. **It violates the finding's own central safety claim.** MK-2's headline is "absence
   is ambiguous → never pre-classify; hand the truth to the VV/tombstone resolver so it
   decides direction." Here the differ hands the resolver a *false* absence signal: the
   resolver sees `{foo, Local=file, Remote=nil}` indistinguishable from an ordinary
   local-only file, with no indication that the remote occupies `foo` as a directory.
   The "absence is ambiguous" guarantee is exactly the property that breaks at this
   boundary.
2. **Downstream hazard.** The WS-4 resolver, seeing `Remote=nil`, may decide either
   "propagate local file `foo` to the peer" (peer cannot create file `foo` — a
   directory `foo` with children already exists there → filesystem collision) or
   "remote deleted `foo`, delete locally" (drops the local file in favour of a delete
   that never happened). No code in `internal/reconcile` was found that detects a
   file-vs-directory type collision from these flattened candidates
   (`grep isDir|TypeFile|directory` over `internal/reconcile/*.go` shows no type-clash
   guard keyed off the diff output).

### Test gap
None of the enumerated MK-2 tests exercise the file-vs-directory branch:
`TestDiff_IdenticalTreesEmpty`, `TestDiff_PrunesEqualSubtrees`, `TestDiff_OneLeafDiffers`,
`TestDiff_SingleSidedCandidate`, `TestDiff_RemoteOnlySubtreeRecursedToLeaves`
(`differ_test.go`) all use pure file-leaf or remote-only-subtree shapes. The
~12-line file-vs-dir branch (`differ.go:80-87`) and the `lDir/rDir` mixed recursion
ship with zero coverage. The finding's own "Test obligations" list also omits it.

## Conclusion

The differ's core prune/recurse/single-sided behaviour is fixed and proven. But the
verdict claimed a clean "no gap found"; there is a gap: a real, untested code path
whose emitted candidate (`Remote=nil` over a path the remote holds as a directory)
contradicts the very "absence is ambiguous" invariant the finding rests on. That is
enough to deny a solid "FIXED". Recommend: add a file-vs-dir table case and either (a)
emit a distinct type-clash marker the resolver can act on, or (b) document+test that
WS-4 detects the collision via the filesystem no-clobber path.

**Vote: REFUTED — medium confidence.**
