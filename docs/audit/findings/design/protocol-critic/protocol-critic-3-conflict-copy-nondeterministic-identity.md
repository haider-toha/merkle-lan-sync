---
id: protocol-critic-3
title: Conflict-copy identity is non-deterministic as specified — the symmetric "both peers independently create the copy" model leaves the name timestamp and the copy's version vector unpinned, producing duplicate or non-convergent conflict copies
severity: high
status: rejected
area: conflict-copy policy + mtime tiebreaker
---

# protocol-critic-3 — PR-3's symmetric conflict proof silently assumes a determinism it never establishes; Syncthing avoids this only by having a *single* creator

## Claim

PR-3's correctness rests on both peers, **with no coordination**, independently
producing a **byte-identical** conflict-copy leaf. For the trees to converge that
leaf must be identical in *both* its name *and* its version vector — because the
structural hash commits to the version vector, two same-named copies with different
VVs are different leaves and the roots never match (SR-5). The design pins **only**
the `<deviceID>` suffix as deterministic. It leaves unspecified:

1. **the `<date>-<time>` source.** PR-3 §6 justifies determinism with "UTC, so both
   peers format the same string regardless of local timezone" — that addresses the
   *timezone*, not *which instant*. If the timestamp is the resolution-time wall
   clock (exactly what Syncthing's own `conflictName` uses: `time.Now()`), the two
   peers resolve the conflict at *different* instants → two differently-named copies
   → duplication, each of which then propagates and re-syncs.
2. **the conflict copy's version vector.** Nothing in PR-3 / SR-7 / SKILL §3 says
   what VV the renamed copy carries (the renamer's freshly-bumped VV? the loser's
   original VV? a `Merge`?). If M mints it via a local rename (bumping `{M}`) while P
   mints it via apply (some other VV), the same-named copy has different structural
   hashes on the two peers → **non-convergence** even though both hold both versions'
   bytes.

Syncthing escapes both problems not by symmetry but by **asymmetry**: the conflict
is detected and resolved on *one* device, which creates the named copy and then
*propagates* it as an ordinary file. Merkle Sync's PR-3 §4 explicitly does the
opposite — both peers independently create the copy — which is precisely the
configuration in which `time.Now()` and an unpinned VV diverge.

## Evidence

- PR-3 §4 "Worked symmetry check": **both** M and P independently rename to "an
  **identically named** `.sync-conflict-…-<M>.txt`" and the proof concludes "the
  trees converge to the same root hash"
  (`docs/audit/findings/protocol/PR-3-conflict-copy-policy-and-tiebreaker.md`
  lines 98-114). The proof justifies only the `<M>` deviceID suffix (the loser's
  `modified_by`); it never shows the `<date>-<time>` portion or the VV match.
- PR-3 §6: "`<UTC-date>` = `YYYYMMDD`, `<UTC-time>` = `HHMMSS` (UTC, so both peers
  format the same string regardless of local timezone)" (lines 124-131) — the stated
  rationale is timezone-only; UTC-of-now still differs across two independently-
  resolving peers.
- **Syncthing uses `time.Now()`** (the creating device's wall clock), and is safe
  only because a single device creates+propagates the copy:
  `func conflictName(name, lastModBy string) string { ext := filepath.Ext(name);
  return name[:len(name)-len(ext)] + time.Now().Format(".sync-conflict-20060102-150405-")
  + lastModBy + ext }`
  (https://raw.githubusercontent.com/syncthing/syncthing/main/lib/model/folder_sendrecv.go,
  func `conflictName`, accessed 2026-06-28). The "single creator, then propagate"
  model: conflict copies "are treated as normal files after they are created, so they
  are propagated between devices" (https://docs.syncthing.net/users/syncing.html,
  accessed 2026-06-28). PR-3 §2 (lines 44-46) and §4 instead specify *both* peers
  rename independently.
- **VV is in the structural hash** (so the copy's VV must match for SR-5):
  `docs/audit/decisions/merkle/leaf-shape-and-structural-hash.md` D.1 (line 113 —
  `version_vector` "in structural hash? yes") and D.3 (lines 148-158, VV encoded
  into `leafEncoding`); SR-5 "converged ⇔ identical root hash"
  (`docs/audit/rules/sync-rules.md` lines 76-88). PR-3 / SR-7 / SKILL §3 never
  assign the copy's VV.
- The determinism requirement PR-3 leans on: FM-6 "A VV without a deterministic
  tiebreaker leads peers to disagree on the winner → divergence"
  (`docs/audit/findings/literature/version-vectors.md` lines 486-495) — the same
  logic applies to the *whole* copy identity, not just the winner pick.
- Corroboration that conflict-name determinism is a genuinely troublesome area:
  Syncthing #7405 "Mark sync-conflict files with both devices participating in the
  conflict" (https://github.com/syncthing/syncthing/issues/7405, accessed
  2026-06-28).

## Impact

On the engine's central no-data-loss path, depending on which reading an implementer
takes:

- **Duplication** — `time.Now()` on each side ⇒ two differently-named conflict copies
  per conflict; both propagate and persist, cluttering the folder and re-entering the
  scanner.
- **Non-convergence** — same-named copy under different VVs ⇒ different leaf hashes ⇒
  the engine can never reach "equal root", so it cannot even *signal done* (SR-5) on
  exactly the scenario SR-7 exists to make safe.

The defect is masked because PR-3 §4 presents a "proof" of symmetric convergence that
quietly assumes the determinism it omits.

## Recommended-change

Make the **entire** conflict-copy leaf a pure function of values both peers observe
identically on the wire, and extend the PR-3 §4 proof to cover all of it:

1. **Name timestamp = the loser `FileInfo`'s mtime** (already replicated; identical
   on both peers), formatted `UTC YYYYMMDD-HHMMSS`. Never `time.Now()`. Normalize
   precision (e.g. truncate to whole seconds) so Mac-nanosecond vs Windows/FAT-rounded
   mtime produce the *same* string — otherwise the tiebreaker input and the name both
   vary by filesystem rounding (a related soundness wrinkle for tier-1 of `W`).
2. **Pin the copy's version vector deterministically** — e.g. inherit the loser's VV
   verbatim (both peers hold it from the wire), or define it as `Merge(loserVV,
   winnerVV)`; fix one rule and prove both peers compute the byte-identical leaf.
3. **Or adopt Syncthing's asymmetric model explicitly:** designate one side (e.g. the
   loser-authoring side, or the smaller-DeviceID side) as the sole creator that mints
   and propagates the copy, and drop the "both independently create" claim. Either
   path is fine — but the current spec is *neither*, and its proof covers only the
   deviceID suffix.
