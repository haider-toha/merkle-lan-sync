# Skeptic #3 vote — protocol-critic-4 (tombstone wipe resurrection / reseed)

**VOTE: REFUTED** (confidence: medium)

## What I verified

I read the finding, PR-4 §5.1 (`docs/audit/findings/protocol/PR-4-deletions-tombstones-resurrection.md:84-96`),
the seeding decision (`docs/audit/decisions/protocol/vv-counter-seeding.md:56-66`,
98-102), and the diff rule (`.claude/skills/merkle-sync/SKILL.md` §2 diff,
lines 104-106).

The **mechanism is technically real**: if M authors a delete (tombstone `{M:6,P:3}`)
that never propagates, M's disk is wiped with no snapshot, and P was partitioned the
whole window holding pre-delete `F {M:5,P:3}`, then on reunion M has zero record of F.
The reseed merges P's VV and, with no local tombstone, the diff yields "only remote
has it → candidate to FETCH" (SKILL §2). F is resurrected, no conflict marker. That
chain is sound and I do not dispute it.

But a sound mechanism is not the same as a finding that holds **as filed**. Three
problems sink it.

## 1. The headline "false-mitigation claim is unconditionally wrong" is a misread of §5.1

The finding's title and Impact assert PR-4 §5.1 *claims this case is mitigated* and is
"unconditionally wrong." Read the actual text. §5.1's FM-4 says: *"a wiped peer
**re-authoring with a low counter** could make **a tombstone fail to dominate**;
mitigated by the persisted-snapshot + cold-start reseed."* That is scoped to **counter
rollback while a tombstone still exists somewhere** — the reseed gives the re-authored
edit a high-enough counter so an existing tombstone can still win. It says **nothing**
about the case where the *only* tombstone is destroyed. §5.1 does not assert the
deleter-wipe-total-loss case is mitigated; it **omits** it. So the finding's own prong
1 ("omits a direct fourth mode") is the accurate characterization, and prong 2
("§5.1 says it's mitigated ... unconditionally wrong") attacks a claim §5.1 never
makes. The marquee assertion in the title is a strawman built by conflating
"rollback of an existing tombstone" with "total loss of the sole tombstone."

## 2. The primary recommendation (#2) is actively harmful — it breaks initial sync

Recommendation 2 — *"during reseed it must not treat 'peer has a path I lack' as an
authoritative remote create" → quarantine such paths as conflict copies* — is
unworkable for the dominant case. A freshly-wiped or first-run device lacks **every**
path the peer holds; adopting them all as live files **is the entire purpose of
reseed/initial sync** (`vv-counter-seeding.md:56-62`). The finding itself concedes the
device "genuinely cannot distinguish delete-then-wipe from never-received," which means
rec 2 must fire on *all* peer-only paths — i.e. it would **quarantine the whole folder
on every wipe-recovery and every new-device join**. It trades an extremely rare silent
resurrection for a common, painful "conflict-copy the entire tree" regression. The
proposed change loses to the status quo for the case that matters most.

## 3. Severity "high" is overstated; this is an accepted industry-wide limit

The trigger is a four-way conjunction: delete authored AND not yet propagated (the
design broadcasts deletes eagerly on confirmed scan, SR-6/PR-6, so this window is
tiny) AND author disk-wiped with no snapshot inside that window AND the sole peer
partitioned throughout. The finding admits the conjunction "bounds the likelihood."
More tellingly, the finding's **own evidence** shows the whole industry treats an
unacknowledged-delete-vs-author-wipe as an accepted limitation, not a high-severity
defect: Syncthing #10590 is **closed as not-planned**, and the forum thread documents
this as long-standing behavior. An unacknowledged write (delete is a write) is not
durable against destruction of its sole copy — that is a fundamental property of any
no-central-server replicated system, not a fixable Merkle-Sync bug. Recommendation 3
states exactly this and is correct, but it is a "document a known limit" item, which
does not support a **high** severity or the "false mitigation" framing.

## What survives

A legitimate, narrow documentation kernel: add an explicit "deleter wiped before
propagation (sole tombstone destroyed)" entry under SR-10 noting it is an accepted v1
limit (rec 1 + rec 3, minus the severity and minus rec 2). That is a one-line doc
nicety, not a high-severity design defect, and it does not require the actionable
mechanism change the finding centers on.

## Conclusion

The finding is well-written and the resurrection mechanism is real, but it (a) hangs
its headline on a misreading of §5.1's scoped FM-4 claim, (b) proposes a primary fix
that breaks the common initial-sync/wipe-recovery path, and (c) overstates severity for
what its own citations show is an accepted, fundamentally-unfixable property of
decentralised sync. As filed — severity high, "false-mitigation unconditionally wrong,"
quarantine-peer-only-paths fix — it does not hold. **REFUTED.**
