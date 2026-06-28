# Decision: MAX_PATH / long paths — rely on Go's `os.fixLongPath`, never hand-prepend `\\?\`

- Area: crossplatform / pathnorm (confirms XP-3's MAX_PATH / long-path sub-item)
- Status: **decided** (Phase 2 — crossplatform-researcher)
- Date: 2026-06-28
- Decider: crossplatform-researcher
- Confirms: `docs/audit/rules/crossplatform-rules.md` XP-1/XP-3 ("confirm UNC /
  `\\?\` long-path prefixes and drive-letter roots are stripped"; "MAX_PATH/long-
  path handling"); cross-refs the illegal-name decision and GR-12.

## Context

Microsoft: "In editions of Windows before Windows 10 version 1607, the maximum
length for a path is **MAX_PATH**, which is defined as 260 characters. In later
versions of Windows, changing a registry key or using the Group Policy tool is
required to remove the limit." The `\\?\` prefix "tells the Windows APIs to disable
all string parsing and to send the string that follows it straight to the file
system … you can exceed the MAX_PATH limits"; "Unicode APIs should be used"; and it
"also allows the use of `..` and `.` in the path names". "Many but not all file I/O
APIs support `\\?\`." (All verbatim,
[Microsoft, *Naming Files, Paths, and Namespaces*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file),
accessed 2026-06-28.)

The critical interaction: **the `\\?\` prefix changes name semantics.** Go's own
docs/source note `Mkdir(\\?\c:\foo )` creates a directory `foo ` *with* the trailing
space, while `Mkdir(c:\foo )` strips it to `foo`
([go.dev/src/os/path_windows.go](https://go.dev/src/os/path_windows.go);
[Klaus Post, *Long Windows paths (UNC paths) in Go*](https://blog.klauspost.com/long-windows-paths-unc-paths-in-go/),
accessed 2026-06-28). That directly fights our illegal-name rule (no trailing
space/period): if *we* prepend `\\?\` we could *create* exactly the Windows-unsafe
names we just decided to escape.

Go already handles long paths for us. The `os` package's `fixLongPath` "returns the
extended-length (`\\?\`-prefixed) form of path when needed, in order to avoid the
default 260 character file path limit"; on Windows 10 build ≥ 15063 with long-path
support it "returns the path unmodified, otherwise it calls `addExtendedPrefix`"
([go.dev/src/os/path_windows.go](https://go.dev/src/os/path_windows.go), accessed
2026-06-28). Caveat: historically `fixLongPath` only acted on **absolute** paths
([golang/go#41734](https://github.com/golang/go/issues/41734)), and the process-wide
long-path enablement mechanism is under active 2024-2026 discussion
([golang/go#66560](https://github.com/golang/go/issues/66560),
[#69853](https://github.com/golang/go/issues/69853), accessed 2026-06-28).

## Options (scored 1–5, 5 = best)

### Option A — manually prepend `\\?\` to every Windows path
- Correctness: **2** — turns on the trailing-space/dot-preserving semantics, so we
  risk creating Windows-unsafe names (contradicts the illegal-name decision);
  `..`/`.` no longer collapse; not all APIs accept it.
- Cross-platform: **2**. Testability: 3. Rejected as the default.

### Option B — rely on Go's `os.fixLongPath`; build absolute OS paths; refuse+flag the residual (PROPOSED)
- Always construct the OS path as `filepath.Join(absRoot, filepath.FromSlash(canonRel))`
  (XP-1: the root is the only absolute path), so the result is absolute and
  `fixLongPath` applies automatically. Do **not** add `\\?\` ourselves.
- Correctness: **5** — Go adds the prefix only when length actually requires it and
  keeps normal name semantics otherwise (no accidental trailing-space creation).
- Cross-platform: **5** — identical call sites on Mac/Windows; Go handles the
  Windows-specific fixup.
- Testability: **4** — length logic is unit-testable; the *actual* >260 write needs
  Windows (Phase 6).
- Concurrency-safety: 5.

### Option C — hard-reject any path > 260 chars
- Correctness: **2** — long paths are legitimate and Go can write them; refusing
  them needlessly breaks valid syncs. Rejected.

## Decision

Adopt **Option B**. Concretely:

1. **Never store or transmit `\\?\`, UNC, or drive-letter prefixes.** Strip any
   such prefix when relativising a scanned path against the root; the canonical key
   is always a clean forward-slash relative path (XP-1, SR-13).
2. **Construct OS paths as absolute** (`filepath.Join(absRoot, FromSlash(rel))`)
   at the filesystem boundary, so Go's `fixLongPath` engages without us touching
   `\\?\`. This also preserves our trailing-dot/space escaping (Option A would
   defeat it).
3. **Defensive length flagging:** compute the would-be OS path length; if it
   approaches/exceeds 260 on a Windows target whose long-path support is not
   confirmed, **refuse + flag** (consistent fallback with the illegal-name and
   collision decisions) rather than risk a partial/failed write. Whether long-path
   support is enabled (registry `LongPathsEnabled` / Group Policy / app manifest)
   is a deployment fact we can detect on Windows but not on the Mac.

## Rationale

- Hand-prefixing `\\?\` is the classic way to *re-introduce* trailing-space/dot
  bugs and `..` surprises; letting Go decide keeps name semantics normal and our
  escaping authoritative.
- Building absolute paths is something we already do (root is absolute per XP-1),
  so `fixLongPath` "just works" without special-casing.

## Consequences

- Drives `internal/pathnorm/pathnorm.go` (`ToOSPath` joins against the absolute
  root; prefix stripping in `Canonicalize`) and `windows.go` (length check feeding
  the refuse+flag fallback).
- The real >260 behaviour, long-path-enabled vs not, and UNC/`\\?\` stripping
  **cannot be verified on the Mac** (plan/README MAX_PATH item) → Phase 6 CI
  `windows-latest` + `CROSS_PLATFORM_CHECKLIST.md`. Add a deep-tree
  Mac→Windows→Mac round-trip to the checklist.
