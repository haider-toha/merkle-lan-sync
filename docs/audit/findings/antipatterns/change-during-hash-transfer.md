# Antipattern finding — hashing or serving a file that is mutating under you (content TOCTOU)

- **Catalogue ID:** AP-10 · **finding slug:** `change-during-hash-transfer`
- **Source slug:** antipatterns
- **Phase / role:** Phase 2 — antipatterns-researcher (anti-slop pass)
- **Status:** open
- **Severity:** high
- **Proposes rule:** **SR-17 (PROPOSED)** — detect change-during-hash / change-during-serve
- **Reads-first honoured:** `docs/audit/rules/{sync,go,crossplatform}-rules.md`,
  `docs/audit/findings/synthesis/problem-space-map.md`
- **Access date for all URLs:** 2026-06-28

## Claim

Between `stat`, hashing, and later serving a file's bytes on a peer's REQUEST, the
file can change. If the engine does not detect this, it either (a) publishes an
index entry whose `content_hash`/`size` describes bytes that no longer exist as a
consistent unit, or (b) streams bytes inconsistent with the advertised hash — so
the receiver assembles a **torn file** (the first half of version A + the second
half of version B) and, without AP-05's whole-file verify, commits it. This is a
*content-level* time-of-check/time-of-use race, distinct from SR-11's
dropped-event case, and no current rule names it.

## Wrong shape

```go
fi, _ := os.Stat(p)
h := sha256file(p)                 // file is still being written during this read
publishIndex(p, h, fi.Size())      // hash/size now describe a snapshot that's already gone

// later, answering a peer REQUEST(p, offset, len):
f, _ := os.Open(p); f.Seek(off, 0); io.CopyN(conn, f, n)   // serves CURRENT bytes, != indexed hash
```

## Why it CORRUPTS data (not merely slow)

The read is not atomic with respect to a concurrent writer, so the produced hash
matches no on-disk state and the served bytes can straddle two versions. Syncthing
hits this exact race:

> "A file has changed, but still has the same timestamp and size as when syncthing
> already created the hash."
> ([syncthing #2414](https://github.com/syncthing/syncthing/issues/2414))

surfaced to users as the "file changed during hashing" condition
([Syncthing forum](https://forum.syncthing.net/t/file-changed-during-hashing/18046)),
"particularly common when large files are being actively written to while
[scanning]". It is a textbook TOCTOU: "the more things happening simultaneously,
the greater the chance that the state of a resource will change between the
time-of-check and the time-of-use"
([Wikipedia, *TOCTOU*](https://en.wikipedia.org/wiki/Time-of-check_to_time-of-use);
[CERT FIO45-C](https://wiki.sei.cmu.edu/confluence/display/c/FIO45-C.+Avoid+TOCTOU+race+conditions+while+accessing+files)).
The dangerous part for a *block* protocol: each block can pass its own per-block
hash against a stale expectation while the assembled whole is inconsistent — which
is why AP-05 (whole-file verify) and this finding are complementary.

## How to test (the failing assertion)

```go
// continuously rewrite a file while the engine scans/serves it
go rewriteForever(p)
idx := scanOnce(p)
served := serveAll(p, idx)                         // emulate answering REQUESTs
// the engine must NOT publish/serve a snapshot it can't prove is stable:
assert.True(t, idx.stableOrReHashed)               // detected mutation → re-hash, not publish
assert.HashMatches(t, served, idx.contentHash)     // never serve bytes != advertised hash
```
Deterministic variant: rewrite the file at a fixed point mid-hash (injected
reader) and assert the engine flags "changed during hashing" and re-queues it.

## Correct approach (PROPOSED SR-17)

Capture a **stat fingerprint** before the read and re-check after:
- Before hashing: record `(size, mtime, ctime/inode where available)`. After
  hashing: re-stat; if any changed, the file moved under you — mark it dirty and
  re-hash on the next settled/debounce cycle; do **not** publish that index entry.
- Before serving a REQUEST: confirm the file still matches the advertised
  `content_hash`/fingerprint (or serve from an immutable snapshot/`O_*` handle
  opened once); if it changed, return a typed "source changed" error so the
  receiver refetches rather than commits torn bytes.
- Debounce (GR-10) reduces the window but does **not** close it (a slow writer
  spans the window); SR-17 is the correctness backstop, AP-05 the receiver-side
  net.

Lands in `internal/merkle/scanner.go` (hash side) and
`internal/reconcile/transfer.go` (serve side).

## Cross-references

- Catalogue: `docs/audit/rules/sync-antipatterns.md` AP-10 (hardens AP-09/AP-11).
- Rules: distinct from SR-11 (missed *events*) and GR-10 (debounce) — those reduce
  frequency; SR-17 (PROPOSED) detects the race when it still happens. Pairs with
  SR-16 (AP-05) on the receiver.
- Decision: `docs/audit/decisions/phase2/antipatterns-rule-gap-handling.md`.

## Sources (accessed 2026-06-28)

- syncthing #2414 (same size+mtime, content changed) — https://github.com/syncthing/syncthing/issues/2414
- Syncthing forum, *file changed during hashing* — https://forum.syncthing.net/t/file-changed-during-hashing/18046
- TOCTOU — https://en.wikipedia.org/wiki/Time-of-check_to_time-of-use
- CERT FIO45-C — https://wiki.sei.cmu.edu/confluence/display/c/FIO45-C.+Avoid+TOCTOU+race+conditions+while+accessing+files
