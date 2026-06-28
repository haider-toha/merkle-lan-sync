---
id: crossplatform-critic-3
title: "Reversible-escape spec is self-contradictory: the authoritative decision gates escaping on IsWindowsUnsafe (predicate-gated), which is non-injective — a Mac file `a:b` escapes to the same on-disk name as a literal `a%3Ab`, clobbering it; the fold/collision index never sees the escaped OS namespace"
severity: medium
status: rejected
phase: 3
critic: crossplatform-critic
focus: illegal-char/reserved-name strategy + round-trip reversibility + no-clobber
created: 2026-06-28
---

# Predicate-gated percent-escaping is not injective; collision index cannot see it

- Reads-first honoured:
  `docs/audit/decisions/crossplatform/illegal-name-strategy.md`,
  `docs/audit/findings/crossplatform/filename-legality.md`,
  `docs/audit/decisions/crossplatform/case-and-normalization-collision-policy.md`,
  XP-3, SR-7, SR-13.

## Claim

The reversible-escape design is **specified two different ways** in the two
authoritative artifacts, and the version stated in the *decision* (the binding
document an implementer follows) is **not injective**: it only escapes a component
when `IsWindowsUnsafe(component)` is true, so a component that is *already* Windows-
legal but *looks like an escape* (e.g. `a%3Ab`) is written verbatim — colliding on
disk with the escaped form of a genuinely-unsafe name (`a:b` -> `a%3Ab`). Worse, the
collision/fold index that is supposed to make "never clobber" hold is keyed on the
**canonical key** (`fold(NFC(name))`), which lives in a *different namespace* from the
escaped on-disk name, so it cannot detect a collision that exists only after escaping.
The two distinct logical files therefore land on one on-disk slot and the second
**silently clobbers** the first.

## Evidence

- **The decision is predicate-gated** — escaping runs only for names the predicate
  flags (`docs/audit/decisions/crossplatform/illegal-name-strategy.md`, Decision):
  > "**Where:** escape happens in the `ToOSPath` boundary conversion **only on the
  > platform/volume that rejects the name**..."
  > "A name component is unsafe if it: (a) contains any of `< > : " / \ | ? *`;
  > (b) contains a control char ...; (c) is a reserved device name ...; (d) ends in a
  > space or a period."
  Note the unsafe set does **not** include `%`. So `a%3Ab` (contains only `a`, `%`,
  `3`, `A`, `b`) is **not** unsafe -> under "escape only the names that are unsafe",
  it is written **verbatim** as `a%3Ab`.
- **The escape of an unsafe name lands on that same string.** `a:b` is unsafe
  (colon). The scheme escapes `:` -> `%3A` (uppercase hex per the decision), giving
  the on-disk name `a%3Ab`. So:
  - `a:b`  (unsafe)   -> on-disk `a%3Ab`
  - `a%3Ab` (legal)  -> on-disk `a%3Ab`  (verbatim, never escaped)
  Two distinct canonical keys, **one on-disk name** -> clobber.
- **The finding's reproduction implies the opposite (universal) behaviour**, which is
  exactly why this is a contradiction, not a typo. `filename-legality.md`'s
  `scratchpad/escapeproto` table shows `%` being escaped **even for names the predicate
  marks safe**:
  > `"100%done"  ->  "100%25done"   unsafe? false  round? true  legal? true`
  i.e. `100%done` is `unsafe=false` yet its `%` was escaped to `%25`. That is the
  *correct* (injective) behaviour — escape every `%` to `%25` on every component
  written to the rejecting platform — but it contradicts the decision's "escape only
  on the platform that rejects the name" gating, under which a non-unsafe name like
  `a%3Ab` would not be touched at all. The binding artifact (the decision) and the
  evidence artifact (the finding/repro) describe different functions.
- **The collision index is in the wrong namespace to catch it.** Per
  `case-and-normalization-collision-policy.md`, the index keys on `fold(NFC(name))`
  of the *canonical key*: `fold(NFC("a:b")) = "a:b"` and
  `fold(NFC("a%3Ab")) = "a%3ab"` — **different** index keys -> no collision detected
  -> both are materialised -> clobber. The index never evaluates the *escaped on-disk*
  form, so any collision introduced by escaping is outside its view.
- Why the collision matters at all: on NTFS the colon is not cosmetic — `name:stream`
  is an alternate data stream — so `:` *must* be escaped (Microsoft, *Naming Files,
  Paths, and Namespaces*, accessed 2026-06-28,
  https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file). That makes
  `a%3Ab` a realistic escaped target, and a user file literally named `a%3Ab` (legal
  everywhere) a realistic collider.

## Impact

- **Silent data loss (SR-7 violation) under the binding spec.** An implementer who
  follows the decision's predicate-gated wording produces a non-injective converter:
  one of two genuinely-distinct files is overwritten on the Windows side with no
  error and no flag (the index is blind to it). This is the precise failure XP-3's
  reversible-escape was introduced to prevent.
- Even under the *correct* (universal `%`-escape) reading, the design has **no stated
  guarantee** that the canonical->OS-name mapping is injective and no test that the
  *escaped* namespace is collision-free; correctness rests on a reproduction script,
  not on the spec.

## Recommended-change

1. **Pin escaping as a total, unconditional, injective transform on the rejecting
   platform**, not predicate-gated. Concretely: `EscapeForWindows` is applied to
   *every* component written on a Windows target; it **always** escapes `%` to `%25`
   first (so the only `%XX` triplets on disk are escapes), then escapes the
   reserved/control/trailing-dot-space/reserved-stem cases. Amend the decision's
   "escape only on the platform that rejects the name" wording, which currently reads
   as "skip names the predicate calls safe" and is the source of the bug. Keep the
   reproduction's behaviour as the normative spec.
2. **Add an injectivity acceptance test** (Mac-runnable): for the Windows-hostile
   table *plus* escape-lookalikes (`a%3Ab`, `100%done`, `%43ON`, `name%2E`), assert
   `EscapeForWindows` is injective (no two distinct inputs share an output) and
   `Unescape(Escape(x)) == x`. The current table tests round-trip but not
   cross-input collision.
3. **Close the namespace gap**: have the no-clobber check consult the *escaped on-disk
   name* (or feed escaped forms into the same collision index) so any residual
   collision in OS-name space is caught as refuse+flag rather than discovered as a
   clobber on disk. This also addresses the namespace mismatch raised in
   `crossplatform-critic-2`.
