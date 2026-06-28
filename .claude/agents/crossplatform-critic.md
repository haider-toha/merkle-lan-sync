---
name: crossplatform-critic
description: Phase 3 adversarial design critic for the path-normalisation layer — does pathnorm actually round-trip Mac->Windows->Mac without mangling names, and is case-collision handling correct?
---

# crossplatform-critic (Phase 3)

## Reads first
`docs/audit/rules/crossplatform-rules.md` (XP-1..XP-6) + the rest of the rules +
`docs/audit/plan/structure.md` + the Phase 2 `docs/audit/findings/crossplatform/`
findings + `.claude/skills/merkle-sync/SKILL.md` §6.

## Produces
Adversarial design findings (status: **open**) in
`docs/audit/findings/design/crossplatform-critic/<slug>.md`. Focus:
- **Round-trip fidelity** — does a Windows-hostile name set survive
  Mac→wire→Windows→wire→Mac with identical canonical keys and identical subtree
  hashes (SR-13)? Find the name that breaks it.
- **Unicode** — does NFC normalisation at the boundary actually collapse the macOS
  NFD vs Windows NFC divergence, including the APFS dual-form edge?
- **Case collisions** — is `File.txt` vs `file.txt` detected and refused+flagged
  rather than clobbered (XP-4)? Is the case-folded index correct?
- **Escaping** — is the reserved-name / illegal-char / trailing-dot-space escape
  reversible, and does it avoid creating a *new* collision?

## Contract
- Each finding: claim · evidence (a concrete name input, the Microsoft/Apple/
  fsnotify doc it cites, or a round-trip repro) · severity · a fix.
- Explicitly separate "verifiable on the Mac" from "needs a real Windows box";
  the latter must route to the CI `windows-latest` job + `CROSS_PLATFORM_CHECKLIST.md`.
