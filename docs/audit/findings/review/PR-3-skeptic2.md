# PR-3 skeptic #2 vote — REFUTE the "fixed" verdict (qualified)

- Reviewed: 2026-06-29 (skeptic #2 of 3, challenging the FIXED verdict)
- Finding: `docs/audit/findings/protocol/PR-3-conflict-copy-policy-and-tiebreaker.md`
- Vote: **refuted = true** (confidence: medium)

## What holds (verified, not disputed)
The core no-data-loss + symmetric-convergence contract for the LIVE-vs-LIVE
concurrent conflict is solidly implemented and tested:
- `aWins` is antisymmetric/total for genuinely concurrent pairs; the content-hash
  backstop is only reached with differing hashes because `resolve` gates on
  `sameContent` BEFORE `conflictPlan` (`apply.go:62,77`), so `bytes.Compare==0` can
  never strand the winner. `authorOf(a,b)` vs `authorOf(b,a)` always return distinct
  IDs for a concurrent pair (same ID cannot satisfy a.V>b.V and b.V>a.V), so tier 2
  resolves deterministically.
- Ran locally 2026-06-29: `go test ./internal/reconcile -run
  'TestW_Commutative|TestConflict_CopyName|TestResolver_'` PASS;
  `go test ./test/integration -run TestConflict_NeitherVersionLost -race` PASS. The
  integration test verifies byte content of BOTH versions on BOTH peers + identical
  conflict-copy filename. Strong.

## Why I still refute: two finding-enumerated claims are not evidenced

### 1. (Primary) Test obligation §7.4 is unmet — the delete-wins data-loss case is untested
The finding's §5 and §7 item #4 make an explicit no-data-loss claim: in a
**concurrent modification-vs-deletion** conflict where the **deletion wins**, the
losing modification MUST survive as a `.sync-conflict` copy. This is the single most
subtle data-loss path in a sync engine and the finding calls it out by name (SR-9).

There is **no test for it** — unit or integration:
- `TestResolver_Matrix` (`reconcile_test.go:142-168`) covers `concurrent + diff
  content` (both LIVE), `concurrent both tombstone → mergeVV`, and `dominated-by
  tombstone` (CAUSAL, not concurrent). It has **no** `concurrent: live modification
  vs tombstone → planConflict (+ copy preserving the modification)` row.
- `TestDeletion_NoResurrection` (`sync_test.go:92`) exercises the CAUSAL
  dominated-by tombstone path, not the concurrent conflict.

I traced `conflictPlan` and the code *appears* correct for this branch (loser =
live modification, `l.Deleted==false` ⇒ conflict copy minted before the winning
tombstone is applied; `apply.go:93-113`, `engine.go:672-686`). But "appears correct
on inspection" is exactly what the test obligation exists to guarantee. A FIXED
verdict that asserts the finding is "verified by tests" while its own enumerated
data-loss obligation #4 has zero coverage is not solidly evidenced for that branch.

### 2. (Secondary) §6 MAX_PATH bounding claim is unwired
Finding §6 states the conflict-copy path "is bounded against MAX_PATH / reserved-name
escaping on Windows (XP-3)". `conflictName` appends ~40 chars (`.sync-conflict-
YYYYMMDD-HHMMSS-<16hex>`) yet `WouldExceedMaxPath` (`pathnorm/pathnorm.go:182`) is
**never called** from `reconcile` or `cmd` (grep: defined+tested in `pathnorm` only).
So a conflict copy whose name crosses 260 chars on Windows is not refused+flagged as
the finding claims. Deferred-to-Phase-6-windows note exists, but the §6 claim as
written is not implemented.

## Disposition
Not a refutation of the whole design — the live-vs-live contract is real and tested.
But two finding-enumerated claims (the delete-wins no-data-loss obligation #4, and
§6 MAX_PATH bounding) lack the evidence the "fixed" status asserts. Per the skeptic
default (refuted=true when "fixed" is not solidly evidenced), I vote refute and
recommend adding: (a) a resolver-matrix row + integration test for concurrent
delete-vs-modify where delete wins (assert the modification survives byte-for-byte as
the conflict copy on both peers), and (b) wiring `WouldExceedMaxPath` into the
conflict-copy creation path (or retracting the §6 claim).
