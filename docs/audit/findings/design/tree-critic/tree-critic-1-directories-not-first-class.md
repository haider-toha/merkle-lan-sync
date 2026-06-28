---
id: tree-critic-1
title: Directories are not first-class versioned entities — empty-dir sync and directory deletion/metadata are broken (no directory version-vector, tombstone, or mode)
severity: high
status: rejected
phase: 3
role: tree-critic
area: merkle / leaf-shape + folder-hash-recompute
date: 2026-06-28
---

# tree-critic-1 — Directories carry none of the two-way-sync metadata the design declares mandatory for files

## Claim

The design makes a **file** a first-class `FileInfo` leaf carrying a version vector,
a `deleted` tombstone, and `mode` — and argues at length that without those three a
two-way sync cannot answer "who is newer," "concurrent vs causal," and "deleted vs
never-created" (MK-3; leaf-shape decision). But a **directory** is *not* a
`FileInfo`. In the chosen structural-hash grammar a directory is a pure structural
`nodeEncoding` whose bytes are only `childCount` + a list of `(name, childHash)` —
it carries **no version vector, no `deleted` flag, and no `mode`**. The consequence
is that the exact three questions the file leaf was designed to answer are
*unanswerable for directories*:

1. **An empty directory cannot sync at all** (it has no `FileInfo`, so it never
   appears in an `INDEX`, so it is never transferred).
2. **A directory deletion is an ambiguous absence**, not a versioned tombstone — so
   it can be resurrected by a stale peer, the precise SR-9/SR-10 failure the file
   design eliminates.
3. **A directory `mode`/permission change is invisible** to the tree.

This is the same "absence is ambiguous" defect the design spent MK-3, SR-9, and
SR-10 closing for files — left wide open for directories.

## Evidence

- **The node grammar excludes VV / deleted / mode.** The chosen recipe
  (`docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md:147-158`) is:
  ```
  leafEncoding  = content_hash[32] || mode:u32 || deleted:u8 || vvCount:u16 || counters…
  nodeEncoding  = childCount:u32 || childCount × ( nameLen:u16 || nameBytes || childHash[32] )
  ```
  Only `leafEncoding` (a file/tombstone) has `mode`, `deleted`, and the version
  vector. `nodeEncoding` (a directory) has none of them. The per-field
  justification table (`leaf-shape-and-structural-hash.md:106-114`) and MK-3's
  "required leaf — field by field" table
  (`docs/audit/findings/merkle/MK-3-leaf-metadata-two-way-sync.md:44-56`) are
  written **entirely about a file leaf**; neither mentions a directory entity.

- **The wire/index has no directory entity.** `INDEX` is defined as "full index
  snapshot: a set of `FileInfo`" and `INDEX_UPDATE` as "incremental `FileInfo`
  deltas" (`.claude/skills/merkle-sync/SKILL.md:233-234`). `FileInfo` is the
  per-*path* file record (`SKILL.md:33-42`). A directory that contains no file
  produces no `FileInfo`, therefore nothing to put in an `INDEX`. `structure.md`
  lists `fileinfo.go` for the leaf and only notes `node.go` as "`Node` (file/dir) +
  structural-hash composition" (`docs/audit/plan/structure.md:75-76`) — the file
  record is specified; a directory record is not.

- **The design explicitly adopts git's tree model, and git cannot represent empty
  directories.** MK-1 pins "name committed by the parent child-entry, not by the
  leaf's own hash … (git model)"
  (`docs/audit/findings/merkle/MK-1-tree-construction.md:38-41`). In that exact
  model "A directory entry in a tree object is only written when it contains at
  least one tracked file. An empty directory contributes no blobs and no tree
  entries, so Git has no object to store and nothing to track"
  ([Baeldung, *Git Objects and How to Add an Empty Directory*](https://www.baeldung.com/ops/git-objects-empty-directory),
  accessed 2026-06-28). The design inherits this limitation without acknowledging
  it; `nodeEncoding = childCount(0)` for an empty dir
  (`leaf-shape-and-structural-hash.md:181-182`) is internally defined but
  *unreachable over the wire* because no `FileInfo` ever carries it.

- **Real systems that *do* version directories still get this wrong — a system that
  doesn't is strictly more exposed.** Syncthing represents directories as
  first-class `FileInfo` entries with their own version and `deleted` flag, and even
  so has a long-standing empty-directory-resurrection bug: deleting/renaming
  directories causes "empty dir tree to get 'put back'"
  ([syncthing #9371](https://github.com/syncthing/syncthing/issues/9371), accessed
  2026-06-28; also referenced from the antipatterns mass-delete finding). Merkle
  Sync removes the very mechanism (a directory `FileInfo` with a VV + tombstone)
  that gives Syncthing even a *chance* of resolving this.

- **The folder-hash-recompute path confirms the gap.** D.4
  (`leaf-shape-and-structural-hash.md:188-206`) shows that deleting the *last file*
  in a directory tombstones that file and re-hashes ancestors correctly — but the
  now-empty directory node persists with the constant hash
  `SHA-256(0x01 || 00000000)`. There is no event that says "the directory itself was
  removed," so a peer that created `photos/` and then `rm -r photos/` (including the
  dir) cannot propagate the removal of the directory entry: the contained files
  tombstone, but the directory's own existence is a structural artifact with no
  versioned removal.

## Impact

- **Silent missing data (high).** Any folder the user cares about that contains
  empty directories (build-output trees, `node_modules`-style scaffolds, photo
  import folders before files land, `.keep`-style placeholders' *absence*) will not
  reproduce on the peer. The two trees are then *permanently non-convergent by
  design* for that subtree — yet SR-5's "equal root ⇔ converged" oracle will report
  them as differing forever with no resolution path, because one side's parent node
  lists a child the other can never materialise.
- **Directory-deletion resurrection (high).** A directory removed while a peer is
  offline reappears on reconnect, because directory removal is an absence, not a
  dominating tombstone (the SR-10 hole, re-opened for directories). For a *non-empty*
  directory the contained-file tombstones mask it; for the directory entry itself and
  for empty directories it does not.
- **Lost metadata (medium).** Directory permission/mode changes never propagate; on
  the cross-platform target this also means a directory's exec/traversal bits diverge
  silently.

## Recommended change (beats the status quo)

Promote directories to first-class entities, matching the file leaf the design
already justified, rather than leaving them as bare structural nodes:

1. **Emit a `FileInfo` for every directory** (Syncthing's `Type=DIRECTORY` shape):
   `{path, type=dir, mode, mtime, version_vector, deleted}` with `content_hash`
   defined as a constant (e.g. 32×0x00) or omitted. Put directory `FileInfo`s in the
   `INDEX`/`INDEX_UPDATE` exactly like files. This makes empty directories
   transferable and directory deletes versioned (a dominating tombstone → no
   resurrection, SR-9/SR-10 restored for dirs).
2. **Fold the directory's own VV/`deleted`/`mode` into its `nodeEncoding`** (add the
   leaf fields to the node grammar, still domain-separated `0x01`), so the structural
   hash commits to directory identity and "equal root ⇔ converged" becomes true for
   directory state too — closing the D.4 gap where an emptied directory has no
   removal event.
3. **If directories are instead deliberately implicit (git-style, no empty-dir
   support)**, that is a *scope decision* that must be logged in
   `docs/audit/decisions/` and reflected in SKILL/structure (state plainly: "empty
   directories are not synced; directory deletion is the deletion of all contained
   files"), and the unreachable `nodeEncoding=childCount(0)` empty-dir case
   (`leaf-shape-and-structural-hash.md:181-182`) removed or marked dead. Silence is
   the bug: today the grammar implies empty dirs are representable while the wire
   format makes them unsyncable.

Option 1 is the recommended path: it is the minimal change that makes the design's
own stated two-way-sync requirements (MK-3) hold for *all* tree entries, not just
files, and it reuses the existing VV/tombstone machinery rather than inventing a new
one. Add a WS-1 acceptance test: create an empty directory and a `rm -r` of a
populated directory on A; assert both converge on B (empty dir present; deleted dir
absent and **not** resurrected after a partition/reconnect of B).
