# Antipattern finding — applying a file follows an existing symlink at the destination (writes outside the tree)

- **Catalogue ID:** AP-21 · **finding slug:** `symlink-following-on-apply`
- **Source slug:** antipatterns
- **Phase / role:** Phase 2 — antipatterns-researcher (anti-slop pass)
- **Status:** open
- **Severity:** high
- **Proposes rule:** **SR-14 (PROPOSED) sibling** — no write through an out-of-tree symlink
- **Reads-first honoured:** `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md`
- **Access date for all URLs:** 2026-06-28

## Claim

Even when the received *name* is clean (AP-20 handled), the on-disk `dst` — or one
of its parent directories — may already exist **as a symlink** pointing outside
the synced folder. A naive `os.Rename(tmp, dst)` or `OpenFile(dst, O_TRUNC)`
follows that link and **overwrites the link's target**, destroying data outside
the tree. No current rule covers symlink-following on the write path.

## Wrong shape

```go
// dst (or a parent dir) is already a symlink → outside root (synced earlier, or pre-existing)
func performFinish(tmp, dst string) error {
    return os.Rename(tmp, dst)   // rename onto a symlink path resolves the link → writes through it
}
// equally: os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o644) truncates the link target
```

## Why it LOSES data (not merely slow)

Writing through a symlink silently mutates a file the user never intended to touch
— the classic symlink attack: "CVE-2000-1178 involved a text editor that followed
symbolic links when creating a rescue copy ... enabling local users to overwrite
files belonging to other users"
([Twingate, *What is a symlink attack*](https://www.twingate.com/blog/glossary/symlink-attack));
"if they open and write to a symlink destination, they can corrupt critical ...
files" (same). "Name collisions between a symlink to a file and a regular file can
result in following the symlink and overwriting its target's contents with that of
the regular source file" ([Twingate](https://www.twingate.com/blog/glossary/symlink-attack);
background [LWN, *Exploiting symlinks and tmpfiles*](https://lwn.net/Articles/250468/)).
This is the second escape route out of `root` (AP-20 is the first); on a trusted
LAN the trigger is usually an *innocently* synced or pre-existing link, not an
attacker — but the data outside the tree is destroyed just the same.

## How to test (the failing assertion)

```go
// sentinel lives OUTSIDE the sync root
os.WriteFile(sentinel, []byte("precious"), 0o644)
os.Symlink(sentinel, filepath.Join(root, "evil"))   // dst is a symlink out of the tree
apply(root, "evil", []byte("attacker bytes"))
got, _ := os.ReadFile(sentinel)
assert.Equal(t, "precious", string(got))            // FAILS for the naive writer
```
Also assert the parent-component case: a symlinked *directory* in the path.

## Correct approach (PROPOSED SR-14 sibling)

For a regular-file apply, **never write through a symlink**:
- `lstat` `dst` and each parent component; if any is a symlink that resolves
  outside `root`, refuse + flag (do not "helpfully" follow it).
- Prefer `O_NOFOLLOW` on the final open, and openat/`*at`-relative semantics
  (resolve each path component without following links) where the platform allows.
- Treat a *synced* symlink as its own typed, contained entity (its target stored
  as data, recreated with `os.Symlink`, never traversed during a file write) —
  consistent with XP-6's "symlink mapping is documented and lossy on Windows".

Lands in `internal/reconcile/{transfer,apply}.go`, alongside the AP-20 path check.

## Cross-references

- Catalogue: `docs/audit/rules/sync-antipatterns.md` AP-21 (sibling of AP-20).
- Rules: XP-6 (symlink/exec-bit mapping) notes symlinks are lossy on Windows but
  says nothing about *following* them on apply — **gap**; SR-14 (PROPOSED) extends
  containment to the resolved target, not just the name.
- Decision: `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.

## Sources (accessed 2026-06-28)

- Twingate, *What is a symlink attack* — https://www.twingate.com/blog/glossary/symlink-attack
- LWN, *Exploiting symlinks and tmpfiles* — https://lwn.net/Articles/250468/
