# Antipattern finding — a received path is materialised without being constrained to the sync root (path traversal)

- **Catalogue ID:** AP-20 · **finding slug:** `path-traversal-received-path`
- **Source slug:** antipatterns
- **Phase / role:** Phase 2 — antipatterns-researcher (anti-slop pass)
- **Status:** open
- **Severity:** critical
- **Proposes rule:** **SR-14 (PROPOSED)** — received-path containment
- **Reads-first honoured:** `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md`,
  `docs/audit/findings/codebases/syncthing-source.md`
- **Access date for all URLs:** 2026-06-28

## Claim

The peer chooses the filename of every file we apply. If the engine builds the
on-disk path as `filepath.Join(root, receivedName)` and writes there without
proving the result stays inside `root`, a received name containing `..`
components, an absolute path, or a drive/UNC prefix **escapes the synced folder
and overwrites or destroys arbitrary files outside it**. No current rule
(SR-1..SR-13, GR-*, XP-*) requires this check — this is a gap.

## Wrong shape

```go
func apply(root, receivedName string, body []byte) error {
    dst := filepath.Join(root, receivedName)      // receivedName = "../../.ssh/authorized_keys"
    return writeAtomic(dst, body)                 // filepath.Join CLEANS ".." → escapes root
}
```
`filepath.Join` calls `Clean`, so `Join("/sync", "../../etc/cron.d/x")` returns
`/etc/cron.d/x` — outside the root, silently. Absolute (`/abs/x`) and Windows
volume/UNC (`C:\Windows\...`, `\\host\share\...`) names are equally dangerous.

## Why it LOSES/CORRUPTS data (not merely slow)

A traversal name lets a peer **overwrite or destroy files anywhere the daemon can
write** — config, keys, other users' data — far outside the one file's expected
update. This is the "zip slip" class: "The code joins the destination path with
the raw ... entry filename ... without validating that the filename doesn't
contain `../` ... [so] files escape the intended directory"
([Zed GHSA-v385-xh3h-rrfr](https://github.com/zed-industries/zed/security/advisories/GHSA-v385-xh3h-rrfr));
"a relative path like `../../etc/passwd` can escape the target directory and write
files anywhere on the filesystem"
([Android, *Zip Path Traversal*](https://developer.android.com/privacy-and-security/risks/zip-path-traversal)).

Merkle Sync's TLS+TOFU trust model (`transport-security-tofu-vs-plaintext.md`)
does **not** retire this: a trusted peer can still be running buggy/old code, and
a name can be *manufactured* by case- or normalisation-fuzzing on one OS and land
as a traversal on the other (the exact Mac↔Windows surface this project lives on).
It is a data-integrity must, not just a security nicety. Syncthing treats a bad
name as fatal — `checkFilename` rejects names where `path.Clean(name) != name`,
empty/`.`/`..`, absolute, and traversal, and "a filename failing this test is
grounds for disconnecting the device"
(`lib/protocol/protocol.go:646-670` @v2.1.1,
`docs/audit/findings/codebases/syncthing-source.md` §1a).

## How to test (the failing assertion)

Table-driven, on **both** OSes (and in the windows-latest CI job):
```go
bad := []string{"../x", "a/../../x", "/abs/x", `C:\x`, `\\h\s\x`, "a/./b", "a//b", ""}
for _, name := range bad {
    err := apply(root, name, []byte("x"))
    assert.ErrorIs(t, err, ErrUnsafePath)       // rejected + peer flagged
    assert.NoFileWrittenOutside(t, root)        // sentinel files outside root untouched
}
```
Assert that for every entry **nothing is written outside `root`** and the peer is
flagged/disconnected.

## Correct approach (PROPOSED SR-14)

Validate every received path **before any filesystem call**:
1. It is the canonical forward-slash relative form (`path.Clean(name) == name`,
   not absolute, no `..` component) — reuse `pathnorm` (XP-1, GR-12).
2. The *resolved* destination is inside the root:
   `canonical(filepath.Join(root, name))` has `root` as a prefix
   (`strings.HasPrefix(rel, "..")` is false after `filepath.Rel`). The Zed fix is
   exactly "ensure resulting path is within destination ... `starts_with(&canonical_destination)`"
   ([Zed advisory](https://github.com/zed-industries/zed/security/advisories/GHSA-v385-xh3h-rrfr)).
3. On failure: reject, do not write, flag/disconnect the peer (Syncthing posture).

This belongs in `internal/reconcile/apply.go` (and the index-ingest path in
`internal/protocol`), enforced for INDEX/INDEX_UPDATE/REQUEST/RESPONSE names.
Sibling hazard AP-21 (symlink-following) is the *other* escape route and is
covered by the companion finding `symlink-following-on-apply`.

## Cross-references

- Catalogue: `docs/audit/rules/sync-antipatterns.md` AP-20 (+ AP-21).
- Rules: extends SR-13/XP-1 (canonical path) into a *containment* guarantee;
  no existing SR forbids traversal on apply — **gap**.
- Synthesis: orthogonal to R-1 (convergence) — this is a write-safety gap not in
  the top-5 because no prior finding raised it.
- Decision: `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.

## Sources (accessed 2026-06-28)

- Zed GHSA-v385-xh3h-rrfr (zip slip) — https://github.com/zed-industries/zed/security/advisories/GHSA-v385-xh3h-rrfr
- Android, *Zip Path Traversal* — https://developer.android.com/privacy-and-security/risks/zip-path-traversal
- Syncthing source `protocol.go` `checkFilename` (@v2.1.1) — via `docs/audit/findings/codebases/syncthing-source.md` §1a
