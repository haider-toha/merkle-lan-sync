# Decision: which delta-encoding codebase to map (rsync vs librsync)

- Area: phase1 / codebases (informs the phase2 chunking decision)
- Status: decided
- Date: 2026-06-28
- Decider: codebase-mapper (rsync-or-librsync)

## Context

The Phase 1 task "rsync-or-librsync" asks me to study a real delta-encoding
implementation (signature / delta / patch, rolling checksum) and write
`docs/audit/findings/codebases/rsync-or-librsync.md` to **inform** (not decide)
the fixed-32 KiB-vs-content-defined-chunking choice that the Phase 2
merkle-researcher owns (`plan/agent_roster.md`: "Decide & log: fixed 32KB chunks
vs content-defined chunking. → docs/audit/findings/merkle/").

There are two canonical real sources and they are not interchangeable as *study
material*, so picking which one to deeply map (and which to use only for
provenance) is a real, log-first methodological choice. Both were recon'd via
the GitHub trees API before deciding:

- **librsync** (`librsync/librsync`, default branch `master`) — a standalone C
  library whose entire reason to exist is the signature/delta/patch codec. It
  has dedicated files per concern: `src/mksum.c` (signature), `src/delta.c`
  (delta), `src/patch.c` (patch), `src/sumset.{h,c}` (signature data structure +
  match), `src/rollsum.{h,c}` and `src/rabinkarp.{h,c}` (two rolling checksums),
  `src/checksum.{h,c}` (weak/strong abstraction), `src/job.{h,c}` (state-machine
  driver), `src/librsync.h` (public API). It is transport-agnostic.
  (Tree listed via `https://api.github.com/repos/librsync/librsync/git/trees/HEAD?recursive=1`,
  accessed 2026-06-28.)
- **rsync** (`WayneD/rsync`, default branch `master`) — the original tool. The
  delta logic (`match.c`, `checksum.c`, `token.c`) is **entangled** with a whole
  client/server daemon, remote-shell launching, protocol multiplexing, file-list
  exchange and IO buffering (`clientserver.c`, `io.c`, `flist.c`, `sender.c`,
  `receiver.c`, `generator.c`, `main.c`, ...). Its authoritative algorithm prose
  is `tech_report.tex` (Tridgell & Mackerras).
  (Tree listed via `https://api.github.com/repos/WayneD/rsync/git/trees/HEAD?recursive=1`,
  accessed 2026-06-28.)

Scoring axes are adapted from the contract's
(correctness / concurrency-safety / testability / cross-platform) to "which
source best informs *our* design":
- **correctness** = fidelity/authority of the delta-algorithm signal, esp. the
  fixed-block-vs-CDC question;
- **concurrency-safety** = how cleanly its structure maps to our
  concurrency-safe Go (vs misleading us with tangled global state);
- **testability** = can the extracted patterns be lifted into isolated,
  table-testable Go units;
- **cross-platform** = is the structure free of OS/transport entanglement that
  would pollute the lesson.

## Options (scored 1–5, 5 = best)

### Option A — Map **librsync** primarily; cross-reference rsync `tech_report.tex` + `match.c` for provenance (PROPOSED)

- Correctness: **5** — librsync isolates exactly the three operations we care
  about; the rsync tech report supplies the original, citable algorithm
  statement and false-alarm data to ground every claim.
- Concurrency-safety: **5** — librsync's job/state-machine + streaming-buffer
  shape (`rs_result` RS_DONE/RS_BLOCKED/RS_RUNNING) is the cleanest analogue to
  driving codec steps over Go `io.Reader`/`io.Writer` without owning the socket.
- Testability: **5** — per-file separation (mksum/delta/patch) maps 1:1 onto
  isolated Go units we can table-test.
- Cross-platform: **5** — transport-agnostic; no daemon/socket assumptions leak
  into the algorithm description.

### Option B — Map **rsync** (`WayneD/rsync`) primarily

- Correctness: **5** — it *is* the canonical implementation.
- Concurrency-safety: **2** — delta logic is interwoven with multiplexed IO,
  forked sender/receiver/generator processes, and file-global mutable state
  (`match.c` uses file-scope `static` accumulators like `last_match`,
  `false_alarms`); mapping that onto our goroutine model invites the wrong
  lessons.
- Testability: **2** — `hash_search()` is one ~200-line function reading a
  memory-mapped buffer and writing tokens to a socket fd; hard to lift cleanly.
- Cross-platform: **3** — lots of OS/daemon machinery (`syscall.c`,
  `clientserver.c`) surrounds the algorithm.

### Option C — Map **both** equally, deep

- Correctness: **5**, but Testability/Concurrency: **3** — double the surface for
  marginal extra signal; the Phase 1 budget is a single focused finding, and the
  two implement the *same* algorithm. Diminishing returns.

### Option D — Use only rsync `tech_report.tex` (paper), no source

- Correctness: **4** (authoritative prose) but **3** overall — the task
  explicitly says "study the real source ... cite file:line refs." A paper alone
  cannot show the data-structure and state-machine *structure* I must map, nor
  the post-paper evolution (RabinKarp 2019, BLAKE2, the magic-number versioning).

## Decision

Adopt **Option A**: deeply map **librsync** as the structural reference, and cite
**rsync's `tech_report.tex` and `match.c`** for canonical algorithm provenance
and the false-alarm/round-trip evidence. The finding draws file:line refs from
both, with librsync carrying the structure mapping.

## Rationale

- librsync's one-concern-per-file layout (signature/delta/patch as separate
  jobs) is the closest existing mirror of how Merkle Sync's `internal/reconcile`
  should be shaped, so the mapping is direct and the "patterns to adopt" are
  liftable rather than aspirational.
- The rsync tech report is the authoritative source for the *fixed-block +
  all-offsets rolling search* design that is the precise contrast to CDC — the
  heart of the chunking question I'm here to inform — and it carries hard
  numbers (false alarms < 1/1000 of matches; 20 bytes/block of signature) I can
  cite instead of asserting.
- Using both at their strengths (librsync = structure, rsync = provenance) gives
  full evidentiary coverage at the cost of one finding, not two.

## Consequences

- Drives the content and citations of
  `docs/audit/findings/codebases/rsync-or-librsync.md`.
- The finding stays in its lane: it **frames** the fixed-vs-CDC tradeoff with
  evidence and gives an input recommendation; the binding chunking decision
  remains the Phase 2 merkle-researcher's
  (`docs/audit/findings/merkle/` + a `decisions/phase2/` entry).
- Sources pulled to the session scratchpad for line-accurate reading
  (librsync `src/*` + `doc/*`, rsync `checksum.c`/`match.c`/`tech_report.tex`);
  all refs below are also citable upstream at
  `github.com/librsync/librsync/blob/master/...` and
  `github.com/WayneD/rsync/blob/master/...` (accessed 2026-06-28).
