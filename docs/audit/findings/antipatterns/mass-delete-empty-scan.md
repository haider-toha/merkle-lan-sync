# Antipattern finding — an empty or unavailable scan is propagated as a mass deletion

- **Catalogue ID:** AP-15 · **finding slug:** `mass-delete-empty-scan`
- **Source slug:** antipatterns
- **Phase / role:** Phase 2 — antipatterns-researcher (anti-slop pass)
- **Status:** open
- **Severity:** critical
- **Proposes rule:** **SR-15 (PROPOSED)** — no bulk delete from an empty/unverified scan
- **Reads-first honoured:** `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md` (R-5, OQ-5)
- **Access date for all URLs:** 2026-06-28

## Claim

Deriving deletions purely from "in my last index but absent from this scan" is
catastrophic when the scan is **empty for the wrong reason**: an unmounted
removable drive, a network mount not yet up, a permissions blip, or a daemon
restart before the persisted baseline loads. The engine concludes *the user
deleted everything* and propagates tombstones that **wipe the entire folder on the
peer**. SR-9/SR-10 cover *how* a delete propagates; nothing covers *whether a bulk
delete should be trusted at all*. This is the highest-impact data-loss mode: one
bad scan destroys the whole folder.

## Wrong shape

```go
prev := lastKnownFiles            // or nil after restart with no persisted baseline
cur  := scan(root)                // root unmounted/locked/empty ⇒ cur == {}
for path := range prev {
    if _, ok := cur[path]; !ok {
        tombstone(path); broadcast(path)   // "everything vanished" → peer deletes its copy
    }
}
```

## Why it LOSES data (not merely slow)

Absence is ambiguous between "deleted here" and "the folder isn't really there
right now", and treating the second as the first deletes real data everywhere.
This is a documented Syncthing failure mode:

> "there is the directory, it is accessible, but there are no files in it, which
> means the user (you) has deleted all the files while Syncthing was stopped. So,
> these files were reported as deleted by you to the other machine, and the other
> machine acted accordingly."
> ([Syncthing forum, *Folder marker missing ... file mass deletion*](https://forum.syncthing.net/t/folder-marker-missing-re-created-it-file-mass-deletion/14346))

The folder marker exists precisely as the guard, and its absence is the *feature*:

> "The marker going missing is the feature that tells you it's about to nuke
> everything. It's a warning that something is screwed up and you need to
> investigate." (same thread)

Related: deleting/renaming directories caused "empty dir tree to get put back"
([syncthing #9371](https://github.com/syncthing/syncthing/issues/9371)). Merkle
Sync is *more* exposed than Syncthing here because it has **no persistent index
DB** (synthesis N4); the in-memory tree "cannot distinguish 'deleted while the
daemon was down' from 'never existed'" — synthesis **R-5/OQ-5**, the least-
mitigated risk in the register.

## How to test (the failing assertion)

```go
// baseline has N files; then the root reads back empty / unreadable
seedBaseline(root, N)
makeRootEmptyOrUnreadable(root)          // simulate unmounted / locked / pre-load
deltas := deriveDeletions(baseline, scan(root))
assert.Empty(t, deltas)                   // NO tombstones emitted
assert.Zero(t, broadcastCount)            // NO deletion broadcast; folder marked unavailable
```
Second case: a *genuine* single delete still propagates (the guard must not block
real, verified deletions).

## Correct approach (PROPOSED SR-15)

A bulk deletion is never derived from an empty or unverified scan:
1. **Require a present root/folder marker** before doing any work; if it is
   missing, declare the folder *unavailable* and stop (Syncthing `.stfolder`
   posture — [forum thread](https://forum.syncthing.net/t/folder-marker-missing-re-created-it-file-mass-deletion/14346)).
2. **Persist a last-synced baseline** (gob is allowed for *local* state, GR-7) and
   derive deletions only by diffing a *successful, verified* scan against it — not
   against `nil` after a restart (synthesis OQ-5 / R-5).
3. **Bulk-delete sanity gate:** if a scan would tombstone all/most of the tree at
   once, treat it as "folder went away", not "user emptied it" — refuse to
   propagate and surface it.

Lands in `internal/reconcile/{scanloop,tombstone}.go` + the WS-1/WS-4 persisted
snapshot. Composes with SR-9/SR-10 (tombstone semantics) and SR-11 (rescan is
truth, but a *failed/empty* rescan is not "truth").

## Cross-references

- Catalogue: `docs/audit/rules/sync-antipatterns.md` AP-15.
- Synthesis: directly hardens **R-5** (persisted-state gap) and **OQ-5**
  (last-synced snapshot for deletion-across-restart) — owner `tree-critic` → WS-1/WS-4.
- Rules: SR-9/SR-10 (tombstones, no resurrection) assume the delete is *real*;
  SR-15 (PROPOSED) is the missing precondition.
- Decision: `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.

## Sources (accessed 2026-06-28)

- Syncthing forum, *Folder marker missing, re-created it -- file mass deletion!* — https://forum.syncthing.net/t/folder-marker-missing-re-created-it-file-mass-deletion/14346
- syncthing #9371 (empty dir tree put back) — https://github.com/syncthing/syncthing/issues/9371
- Syncthing FAQ / folder markers — https://docs.syncthing.net/users/faq.html
