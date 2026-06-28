# Decision: `mode`/`mtime`/symlink portability — canonicalise mode to a portable 2-state bit before hashing; symlinks are typed leaves, refuse+flag on unprivileged Windows

- Area: crossplatform / merkle + pathnorm (confirms XP-6) — **amends** the Phase 0
  leaf-shape decision's structural-hash recipe
- Status: **decided** (Phase 2 — crossplatform-researcher); flagged to
  merkle-researcher / tree-critic because it changes what the structural hash
  commits to
- Date: 2026-06-28
- Decider: crossplatform-researcher
- Confirms: `docs/audit/rules/crossplatform-rules.md` XP-6; cross-refs SR-4
  (mtime is tiebreaker only), SR-5 (convergence), and
  `decisions/phase0/merkle-leaf-shape.md` (which currently includes `mode` in the
  structural hash).

## Context — a real convergence bug hiding in the leaf shape

The Phase 0 leaf-shape decision puts **`mode` in the structural hash** and excludes
raw `mtime`/`size`. That is fine within one OS, but it manufactures a cross-OS diff:

- **NTFS has no POSIX permission bits and no executable bit.** A Mac file at
  `0755` synced to Windows cannot store `0755`; when the Windows peer rescans it,
  Go reports a Windows-derived mode (e.g. `0666`/`0444` mapped from the read-only
  attribute). The two peers now hold *different* `mode` values for the same file →
  *different* structural hashes → **roots never converge (SR-5 broken)**, even
  though the bytes are identical. This is the same class as the NFD/NFC and stored-
  `\` bugs: a non-portable field in the hash.
- `mtime` semantics/precision also differ (NTFS 100 ns vs APFS ns; FAT 2 s) — but
  it is already excluded from the structural hash and used only as a conflict
  tiebreaker (SR-4), so it is safe; we just confirm it stays out.
- **Symlinks** are first-class on macOS; on Windows creating a symlink needs
  `SeCreateSymbolicLinkPrivilege` (admin) or **Developer Mode**, otherwise it
  fails. Materialising a symlink verbatim on an unprivileged Windows box errors.

## Options — mode in the structural hash (scored 1–5)

### Option A — keep full POSIX `mode` in the structural hash, materialise best-effort
- Correctness: **2** — the round-trip Mac→Windows→Mac drops/maps bits → divergent
  hashes → non-convergence (the bug above). Rejected.

### Option B — canonicalise `mode` to a portable 2-state value *before hashing*; keep full mode as advisory metadata (PROPOSED)
- The structural hash commits only to `{ isExecutable: bool, fileType: file|dir|
  symlink }`, derived from `mode` by a portable rule (executable = any of the
  owner/group/other `x` bits set on a regular file). The full `mode` stays in
  `FileInfo` for best-effort application but does **not** enter the hash.
- Correctness: **5** — an intentional `chmod +x` still propagates and *converges*
  (both peers agree on the single exec bit), while Windows's inability to represent
  the other bits no longer manufactures diffs.
- Cross-platform: **5**. Testability: **5** (pure mapping, table-driven).
  Concurrency-safety: 5.

### Option C — exclude `mode` from the structural hash entirely
- Correctness: **4** — converges, but an executable-bit change carries *no* hash
  signal, so toggling `+x` (no content change) would not propagate via the tree
  diff. Weaker than B for the one permission users actually sync.

## Options — symlinks (scored 1–5)

### Option S-A — represent symlinks as a typed leaf (type=symlink, content = target path, forward-slash NFC); on unprivileged Windows, refuse+flag creation (PROPOSED)
- Correctness/no-loss: **5** — the link target is preserved in the tree and on the
  Mac; Windows simply flags "cannot create symlink (needs privilege/Developer
  Mode)" instead of erroring or silently writing a wrong file.
- Cross-platform: **5**. Testability: 4 (creation needs Windows; the typed-leaf
  hashing is Mac-testable).

### Option S-B — follow symlinks (sync the target's content as a regular file)
- Correctness: **2** — loses the link semantics, can create cycles, can escape the
  sync root (security). Rejected.

### Option S-C — materialise the link target as a plain text file on Windows
- Correctness: **2** — lossy and ambiguous (indistinguishable from a real file
  whose content happens to be a path). Rejected.

## Decision

- **`mode`: Option B.** The structural hash commits to a **canonical 2-state**
  `{executable bit, file type}`, not the raw `mode`. Full `mode` remains in
  `FileInfo` as best-effort, applied where the OS supports it, **never** part of
  conflict detection on its own (a pure mode change is a normal versioned edit via
  the exec bit; non-exec bit differences are not hashed and so never conflict). This
  **amends** `decisions/phase0/merkle-leaf-shape.md` (which currently hashes raw
  `mode`); merkle-researcher folds this into the exact canonical serialisation
  (OQ-4) and tree-critic is notified.
- **Symlinks: Option S-A.** Typed leaf carrying the normalised forward-slash NFC
  target; on Windows without symlink privilege, **refuse + flag** (consistent with
  the illegal-name and collision fallbacks), do not error the whole sync.
- **`mtime`: confirm it stays excluded from the structural hash** (SR-4 /
  leaf-shape), used only as the conflict tiebreaker; compare with a tolerance large
  enough to absorb FAT's 2 s and cross-OS sub-second precision differences when
  used as a tiebreaker.

## Rationale

- Hashing only the portable, user-meaningful permission (the exec bit) is the
  minimum that both converges Mac↔Windows *and* still propagates the one permission
  change people actually care about — strictly better than either hashing raw mode
  (diverges) or dropping mode from the hash (loses `+x` propagation).
- Treating symlinks as typed leaves with refuse+flag on unprivileged Windows keeps
  the no-data-loss posture without requiring admin rights to run the daemon.

## Consequences

- Drives `internal/merkle/{node,codec}.go` (hash the canonical 2-state, not raw
  mode), `internal/merkle/fileinfo.go` (retain full mode as advisory + a `fileType`
  incl. symlink), and `internal/pathnorm` (target normalisation).
- **Action for merkle-researcher:** incorporate the 2-state mode into the exact
  structural-hash grammar (OQ-4) and update the leaf-shape decision's "Included in
  structural hash" list from `mode` to `canonical(mode)`.
- The exec-bit round-trip and symlink-on-Windows behaviour **cannot be verified on
  the Mac** (plan/README) → Phase 6 CI `windows-latest` +
  `CROSS_PLATFORM_CHECKLIST.md`. The 2-state mapping and typed-leaf hashing are
  Mac-unit-testable.
