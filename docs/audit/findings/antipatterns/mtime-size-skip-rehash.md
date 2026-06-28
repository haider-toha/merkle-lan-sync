# Antipattern finding — trusting size+mtime to SKIP rehashing misses in-place edits

- **Catalogue ID:** AP-11 · **finding slug:** `mtime-size-skip-rehash`
- **Source slug:** antipatterns
- **Phase / role:** Phase 2 — antipatterns-researcher (anti-slop pass)
- **Status:** open
- **Severity:** high
- **Refines rule:** **SR-11** (rescan is the source of truth) + **AL-11** (two-tier gating is an optimisation, not the change oracle)
- **Reads-first honoured:** `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md` (AL-11)
- **Access date for all URLs:** 2026-06-28

## Claim

`size`+`mtime` is a fine *cheap-reject prefilter*, but using it as the **authority
to skip hashing** means an in-place edit that keeps the same byte-length and
preserves/restores the mtime is **never detected** — so a real local change is
never broadcast and the two peers silently diverge (one holds content the other
will never receive). The current rules say "rescan is truth" (SR-11) and "two-tier
checksum gating" is good discipline (AL-11), but neither explicitly forbids using
mtime as a *skip-rehash* authority during change detection. That nuance is the
bug.

## Wrong shape

```go
func leafHash(p string, prev FileInfo) [32]byte {
    fi, _ := os.Stat(p)
    if fi.Size() == prev.Size && fi.ModTime().Equal(prev.Mtime) {
        return prev.ContentHash      // ASSUME unchanged — never reads the bytes
    }
    return sha256file(p)
}
```
The `return prev.ContentHash` path is the trap: it makes mtime the change oracle.

## Why it LOSES data (not merely slow)

A content edit that preserves size and mtime is invisible to this check, so the
change is never authored into the version vector, never broadcast, and never
reaches the peer — permanent silent divergence. This is exactly rsync's default
limitation, which is *why* `--checksum` exists:

> "Rsync finds files that need to be transferred using a 'quick check' algorithm
> (by default) that looks for files that have changed in size or in last-modified
> time. ... [--checksum] changes this to compare a 128-bit checksum for each file
> that has a matching size."
> ([rsync(1)](https://man7.org/linux/man-pages/man1/rsync.1.html))

Same-size, same-mtime, different-content is a real, reachable state: editors and
tools that preserve mtime (`touch -r`, archive extraction, `cp -p`, some
build/codegen steps), filesystems with coarse (1 s) mtime granularity where two
edits land in the same second, and the Syncthing "same timestamp and size" race
([syncthing #2414](https://github.com/syncthing/syncthing/issues/2414)). On a
1-second-granularity FS, two saves within the same second to a same-length buffer
are indistinguishable by the quick check.

## How to test (the failing assertion)

```go
write(p, bytesA); base := scanFull(root)               // record baseline
overwriteSameSizeSameMtime(p, bytesB)                  // edit content; restore size+mtime
delta := scanFull(root).diff(base)
assert.Contains(t, delta, p)                            // FULL rescan still detects it
assert.NotEqual(t, base[p].ContentHash, delta[p].ContentHash)
```
`overwriteSameSizeSameMtime` writes equal-length content then `os.Chtimes(p,
oldAtime, oldMtime)`.

## Correct approach (refine SR-11 / AL-11)

- The **periodic full rescan computes the content hash unconditionally** — it is
  the source of truth (SR-11), the rsync `--checksum` posture. It must **not**
  short-circuit on size+mtime.
- size+mtime (AL-11 two-tier gating) may gate only a *fast, best-effort* hot path
  between full rescans (e.g. "should I bother re-reading right now?"), never the
  authoritative change detection. Document it as an optimisation with a guaranteed
  full-rehash floor.
- Consider including `ctime`/inode in the cheap fingerprint (harder to spoof than
  mtime), and pair with SR-17 (AP-10) so a change *during* a hash is also caught.

Lands in `internal/merkle/scanner.go`. WS-1 acceptance should add: "an in-place
edit preserving size+mtime is detected by a full rescan."

## Cross-references

- Catalogue: `docs/audit/rules/sync-antipatterns.md` AP-11.
- Rules: refines **SR-11** (makes "rescan is truth" mean "rescan re-hashes, not
  re-stats") and **AL-11** (caps the optimisation). Pairs with SR-17 (AP-10).
- Decision: `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.

## Sources (accessed 2026-06-28)

- rsync(1) (quick check vs --checksum) — https://man7.org/linux/man-pages/man1/rsync.1.html
- syncthing #2414 (same size+mtime, content changed) — https://github.com/syncthing/syncthing/issues/2414
