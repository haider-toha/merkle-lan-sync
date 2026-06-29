# Decision: pathnorm API — explicit Target model so Windows escaping is testable on the Mac

- Area: WS-1 / pathnorm (consolidates CDD-6; implements XP-1/2/3/4)
- Status: decided
- Date: 2026-06-29
- Decider: WS-1 implementer
- Consumes decisions: `crossplatform/{unicode-canonical-form, illegal-name-strategy,
  maxpath-longpath-handling, case-and-normalization-collision-policy}.md`; folds in
  CDD-6 (one authoritative, total, per-component ToOSPath/escaping pipeline).
- Implements plan WS-1 criterion 2 (Windows-hostile round-trip + injective escape).

## Context

The same logical file must map to the same canonical key on Mac and Windows (SR-13),
yet a name legal on Mac/Linux can be illegal on Windows (XP-3) and must be escaped
reversibly on the Windows on-disk form only. The hard constraint from plan/README:
"green on the Mac is necessary, not sufficient" — but the *escape logic* must be
**fully exercised on the Mac** (the actual on-disk write of an escaped name is the
only Windows-only tail, deferred to Phase 6). So the API must let a test force a
*Windows target* while running on a Mac. The naive design (gate escaping on
`runtime.GOOS == "windows"`) makes the entire escape path dead code in the Mac test
run — exactly what the R-1/SR-13 gate must not allow.

## Options (scored 1-5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — gate escaping on `runtime.GOOS` only
- Testability **1**: on the Mac, `ToOSPath` never escapes, so the hostile-name
  round-trip and injectivity tests cannot run against the real boundary function;
  the R-1 gate degrades to testing pure helpers in isolation. Rejected.

### Option B — explicit `Target` parameter on the boundary functions (CHOSEN)
- `ToOSPath(absRoot, canonKey, target)` / `FromOSPath(absRoot, osPath, target)`
  take a `Target {Unix, Windows}`. The real filesystem call sites pass
  `HostTarget()`; tests pass `Windows` on a Mac and drive the full escape/unescape
  path. The functions do path math in their own separator space (not host-bound
  `filepath`) so a Windows target round-trips correctly on any host; for
  `target == HostTarget()` the result matches host `filepath` semantics and Go's
  `os.fixLongPath` engages because `absRoot` is absolute (maxpath decision).
- Correctness **5** · Concurrency **5** (pure) · Testability **5** ·
  Cross-platform **5**. Chosen.

### Option C — inject a filesystem/OS abstraction interface
- Testability **5** but Correctness/complexity cost: a full FS abstraction is
  WS-4-scope (apply path) and overkill for pure name math; adds surface with no
  benefit over B for WS-1. Rejected as premature.

## Decision

Adopt **Option B**. The package surface:

- `type Target int; const (Unix Target = iota; Windows); func HostTarget() Target`.
- `Canonicalize(osRelPath string) (string, error)` — host OS-native relative path →
  canonical key: `filepath.ToSlash`, strip any `\\?\`/UNC/drive prefix, **NFC per
  component** (`norm.NFC`), `path.Clean`, reject absolute or root-escaping (`..`).
- `CanonicalizeSlash(slashRel string) (string, error)` — same, for an
  already-forward-slash key (from the wire); the idempotent canonical-form check.
- `ToOSPath(absRoot, canonKey string, target Target) string` — split the key on
  `/`; for a Windows target apply `EscapeForWindows` to **every component**
  (platform-gated, never per-name; never touches `absRoot` or the separator); join
  with the target separator under `absRoot`. Never hand-prepends `\\?\`.
- `FromOSPath(absRoot, osPath string, target Target) (string, error)` — inverse:
  relativize against `absRoot` (host `filepath.Rel` when `target == HostTarget()`,
  else string strip in the target separator space), split, `UnescapeFromWindows`
  per component on a Windows target, NFC, → canonical key.
- `windows.go`: `IsWindowsUnsafe`, `EscapeForWindows`, `UnescapeFromWindows`,
  `WouldExceedMaxPath`, the reserved-char/stem tables (computed on **every** OS).
- `casefold.go`: `Fold(name)` = `cases.Fold(NFC(name))`; `FoldIndex` collision
  helper (`Add(key) (existing string, collision bool)`) for XP-4 detection.

**Escape scheme (reversible, total — `illegal-name-strategy.md` Option D):**
`%`→`%25` first; each reserved char (`< > : " / \ | ? *`) / control char (NUL,
1-31) → `%` + 2 uppercase hex; a trailing space/period → escape the final char (so
the on-disk name ends in a legal hex digit); a reserved device **stem**
(`CON/PRN/AUX/NUL/COM1-9/LPT1-9/COM¹²³/LPT¹²³`, case-insensitive, stem = before the
first `.`) → escape its first byte. `UnescapeFromWindows` reverses `%XX`; because
every literal `%` is `%25`, decode is unambiguous. Round-trip
`Unescape(Escape(x)) == x` (a left inverse ⇒ `Escape` is injective).

## Rationale

- The explicit `Target` is the single change that turns the SR-13/R-1 escape path
  from Mac-untestable dead code into a fully exercised, table-driven gate, while the
  host call sites stay correct (`HostTarget()`).
- Per-component, platform-gated escaping is CDD-6 verbatim and avoids the
  crossplatform-critic's misread "corrupts every path" failure mode (the separator
  and root are never escaped).
- Relying on `os.fixLongPath` (absolute join, no hand `\\?\`) avoids re-introducing
  the trailing-space/dot creation bug the escaping exists to prevent.

## Consequences

- Drives `internal/pathnorm/{pathnorm.go, normalize.go, windows.go, casefold.go,
  doc.go}` and adds `golang.org/x/text` (norm + cases) as an approved dependency
  (already logged in `unicode-canonical-form.md`); pinned to **v0.27.0** (the newest
  release whose `go` directive is `1.23`, matching the module/CI baseline; v0.38.0
  requires go 1.25 and would break the go-1.23 CI matrix).
- Tests (Mac-runnable): `TestWindowsHostileRoundTrip`, `TestEscape_Injective`,
  `TestCanonicalize_NFCAndSlash`, `TestCanonicalize_RejectsTraversal`,
  `TestToFromOSPath_WindowsTarget`, `TestFold_CaseAndNormCollision`.
- The real on-disk write of an escaped name, NTFS ADS via `:`, reserved-name
  rejection, and >260 writes remain Phase-6 (CI windows-latest + checklist).
- Cross-refs: SR-7/13, XP-1/2/3/4, GR-11/12; CDD-6.
