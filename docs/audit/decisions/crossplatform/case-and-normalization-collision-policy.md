# Decision: Canonical-key collisions (case + normalisation) — case-sensitive keys + fold index; refuse + flag, never clobber

- Area: crossplatform / pathnorm + reconcile (confirms XP-4, and the
  normalisation-collision edge of XP-2)
- Status: **decided** (Phase 2 — crossplatform-researcher)
- Date: 2026-06-28
- Decider: crossplatform-researcher
- Confirms: `docs/audit/rules/crossplatform-rules.md` XP-4 (and the XP-2 "APFS
  dual-form" edge); cross-refs SR-7/SR-9 (no data loss on conflict), SR-13.

## Context

Two kinds of distinct logical files can collapse onto **one slot** on a target
filesystem, and a naive "just write it" then **clobbers** one — silent data loss:

1. **Case collision.** `File.txt` vs `file.txt`. Reproduced on this machine's
   default APFS volume (case-insensitive): creating `File.txt` ("first") then
   `file.txt` ("second") left **one** file whose contents were `second` — the
   first was silently overwritten. Microsoft: "Do not assume case sensitivity …
   consider the names OSCAR, Oscar, and oscar to be the same … NTFS supports POSIX
   semantics for case sensitivity but this is not the default behavior"
   ([Microsoft naming page](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file),
   accessed 2026-06-28). So Windows (always) and macOS (default) are
   case-insensitive; a **case-sensitive** Linux peer (or a case-sensitive macOS
   volume) can hold both and will happily send both.
2. **Normalisation collision.** `résumé.txt` stored as NFC and as NFD. After we
   canonicalise to NFC (`decisions/crossplatform/unicode-canonical-form.md`) both
   map to the **same** canonical key. On APFS they cannot even coexist (probe:
   creating both forms yielded 1 entry); on Linux they are two real files a peer
   can send.

Both reduce to: *two distinct on-disk files that cannot both be represented under
one canonical key on a case-insensitive / normalization-insensitive target.* The
no-data-loss contract (SR-7) forbids clobbering either.

Syncthing is the reference posture. Its `caseSensitiveFS` is **`false` by
default**, which means "Syncthing's case sensitivity safety checks are enabled …
[it] will then attempt to detect and prevent case-only file name collisions that
can occur on case insensitive systems such as Windows and macOS"; the setting "is
**not** meant to change the basic principles of how Syncthing handles
case-sensitivity"
([caseSensitiveFS docs](https://docs.syncthing.net/advanced/folder-caseSensitiveFS.html),
v2.1.0, accessed 2026-06-28). Internally it keeps paths case-sensitive
("`file.txt` and `FILE.txt` denote two independent things") and, on a collision,
either makes it a sync error or writes a conflict copy
(`Foo.case-conflict-<timestamp>-<dev>.txt`), detecting the real on-disk case "using
directory listing methods" (released v1.9.0 —
[Syncthing wiki: Filesystem Case Sensitivity](https://github.com/syncthing/syncthing/wiki/Filesystem-Case-Sensitivity),
accessed 2026-06-28).

## Sub-decision 1 — key representation

### Option 1A — fold case into the canonical key (lowercase the key)
- Correctness: **1** — `File.txt` and `file.txt` become the *same identity*. On a
  case-sensitive Linux peer those are two real, different files → they'd be merged
  and one lost. Unicode case-folding is also locale/edge-case fraught. Rejected.

### Option 1B — case-sensitive **NFC** keys + a separate fold index (PROPOSED)
- Keys are exactly the NFC canonical name (case preserved). A side **collision
  index** maps `fold(key) → key` where `fold` = Unicode simple case-fold of the
  NFC form. The index is consulted only to *detect* a clash on an insensitive
  target; it never changes identity.
- Correctness: **5** — preserves distinctness for case-sensitive peers; detects
  clashes for insensitive ones. Testability: **5** (pure `fold`, table-driven).
  Cross-platform: **5**. Concurrency-safety: **4** — the index is shared mutable
  state, so it lives under the single tree `RWMutex` (GR-5), updated by the one
  writer; readers snapshot under `RLock`.

### Option 1C — case-sensitive keys, no index, rely on the OS to error
- Correctness: **1** — on APFS the second write *silently clobbers* (proven above),
  the OS returns no error. Rejected.

## Sub-decision 2 — action on a detected collision

### Option 2A — clobber (write anyway)
- Rejected outright: silent data loss, violates SR-7.

### Option 2B — refuse the second + flag, keep both in the tree (PROPOSED default)
- The winner (first-seen / deterministic) occupies the path on the insensitive
  target; the colliding file is **not materialised** there, is retained in the
  tree, and is surfaced as a flagged "case/normalisation collision — cannot
  represent on this filesystem". Both versions remain on the case-sensitive peer.
  Nothing is lost. This is the roster-pinned "refuse + flag" posture.

### Option 2C — auto-rename loser to a `.sync-conflict`/`.case-conflict` copy
- Mirrors Syncthing v1.9+. No data loss either (the loser is renamed, then syncs
  as a normal file), but it *creates* a file on the insensitive target the user
  didn't ask for and can ping back to the sensitive peer. Reasonable as an opt-in.

## Decision

- **Sub-decision 1: Option 1B** — keys stay case-sensitive NFC; maintain a
  **fold-and-normalise collision index** (`fold(NFC(name))`) under the tree
  `RWMutex` (GR-5). The same index key detects *both* case collisions and
  normalisation collisions (since both are NFC by then, `fold` differs only by
  case), unifying XP-2's dual-form edge and XP-4 under one mechanism.
- **Sub-decision 2: Option 2B (refuse + flag) is the default**, with **Option 2C
  (case-conflict copy) available as a config flag** for users who prefer
  Syncthing's auto-resolution. Default never clobbers and never invents a file.
- **Target case-sensitivity is probed at startup** (create two temp names
  differing only by case in the sync root, observe whether both survive — the same
  technique Syncthing uses via directory listing). The result selects whether the
  fold index is enforced (insensitive target) or collisions are allowed
  (case-sensitive target). Probe result is logged.
- **Winner selection is deterministic** so both peers agree which file occupies the
  shared slot: reuse the conflict tiebreaker (SR-7) — newer mtime wins, else larger
  DeviceID loses — applied to the colliding pair.

## Rationale

- Keeping keys case-sensitive is the only choice that does not lose data on a
  case-sensitive peer, while a fold index still gives us the detection an
  insensitive target needs — exactly Syncthing's validated posture.
- Folding the *NFC* form means one index catches both collision classes; no second
  mechanism for normalisation.
- Refuse+flag is the strictest no-data-loss default; the conflict-copy variant is
  offered but not forced, because auto-creating files is a sharper, more surprising
  behaviour.

## Consequences

- Drives `internal/pathnorm/casefold.go` (`Fold`, the collision index helpers) and
  reconcile's apply path (`apply.go`/`conflict.go`): before materialising a key on
  an insensitive target, check the fold index; on clash, refuse+flag (or
  case-conflict copy if configured), never `os.OpenFile`+truncate.
- The index is concurrency-shared → it is owned by the single reconcile writer
  under the one `RWMutex` (GR-5); zero I/O under the lock.
- **Cannot be fully verified on the Mac for the Windows/NTFS side** (plan/README
  explicitly lists "NTFS case-insensitive collisions" as needing a real Windows
  target). The macOS-insensitive and Linux-sensitive sides *are* reproducible
  locally (the clobber probe already shows the macOS side). Closed by Phase 6 CI
  `windows-latest` + `CROSS_PLATFORM_CHECKLIST.md`.
