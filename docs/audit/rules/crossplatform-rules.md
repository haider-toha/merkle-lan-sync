# Cross-platform path/filename rules (PRELIMINARY — Mac ↔ Windows)

> **Status: PRELIMINARY.** These are the Phase 0 starting rules so implementation
> is not blocked. The elevated **crossplatform-researcher** track (Phase 2) owns
> the evidence and the final decisions; every rule below ends with
> **"preliminary — confirm in Phase 2"** and names what must be confirmed. Do not
> treat any rule here as final.
>
> Hard reality (plan/README.md): this is being built on a Mac, but the
> requirement is Mac↔Windows. "Green on the Mac" is necessary, not sufficient —
> NTFS case collisions, Windows reserved names, `ReadDirectoryChangesW` event
> drops, and trailing-dot/space handling **cannot** be verified from one machine.
> Phase 6 emits a CI windows-latest job + a manual `CROSS_PLATFORM_CHECKLIST.md`.
>
> **UPDATE — Phase 2 confirmation complete (2026-06-28, crossplatform-researcher).**
> XP-1..XP-6 are now confirmed with cited evidence (incl. runnable Go reproductions
> on this Mac) and logged decisions. See the closed checklist at the bottom, the
> six decisions in `docs/audit/decisions/crossplatform/`, and the six findings in
> `docs/audit/findings/crossplatform/`. The per-rule "preliminary — confirm in
> Phase 2" notes below are **superseded** by those decisions. Sub-items that
> genuinely require a real Windows/NTFS target remain deferred to Phase 6 (flagged
> per checklist item). One correction surfaced: XP-5's "FSEvents coalescing" wording
> — our chosen library (`fsnotify`) uses **kqueue** on macOS, not FSEvents; the
> rule (rescan is truth) is unchanged, only the mechanism is named correctly. One
> amendment surfaced: XP-6 shows raw `mode` in the structural hash is a convergence
> bug; the leaf-shape hash must commit to a canonical 2-state mode (handed to
> merkle-researcher / tree-critic).

The reconciler's correctness (SR-5 convergence, SR-13 canonical identity) depends
on the *same logical file* mapping to the *same canonical key and hash* on both
operating systems. Everything here serves that.

---

## XP-1 — Canonical path = forward-slash, relative, never an OS separator

- **Rule:** the on-disk root is the only absolute path. Every path in the tree,
  on the wire, and as a map key is **relative to the root** and uses **`/`** as
  the separator. Convert with `filepath.ToSlash` on read and `filepath.FromSlash`
  on write; never store `\`.
- **Why:** Windows `filepath` uses `\`; storing it makes the same file hash
  differently across OSes and breaks SR-5/SR-13. Microsoft itself reserves `\`
  and `/` as path-component separators that cannot appear in a name ([Microsoft, *Naming Files, Paths, and Namespaces*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file), ms.date 2024-08-28, updated 2025-04-11, accessed 2026-06-28).
- **preliminary — confirm in Phase 2:** confirm UNC / `\\?\` long-path prefixes
  and drive-letter roots are stripped correctly before relativisation;
  round-trip a deep tree Mac→Windows→Mac.

## XP-2 — Canonical Unicode form: normalise to **NFC** at the boundary

- **Rule (proposed):** treat **NFC** (Composed) as the canonical on-the-wire /
  in-tree form. Normalise filenames to NFC when scanning into the tree and when
  receiving from a peer; apply the OS-native form only when calling the
  filesystem. Store the canonical NFC key; remember the on-disk byte form
  separately if needed to re-open the file on macOS.
- **Why:** "macOS uses NFD (Normalization Form Decomposed) by default when
  storing filenames, while Windows and Linux primarily expect NFC." HFS+
  "automatically converts filenames to Form D (NFD)"; APFS "doesn't normalise"
  but "a normalisation layer in macOS ensures that file and directory names are
  normalised, so APFS behaves just like HFS+ did." The visible symptom of getting
  this wrong: "When you create a file named résumé.pdf on a Mac and send it to
  Windows ... the filename appears as re´sume´.pdf"
  ([The Eclectic Light Company, *Unicode, normalization and APFS*](https://eclecticlight.co/2021/05/08/explainer-unicode-normalization-and-apfs/); [Michael Tsai, *APFS's "Bag of Bytes" Filenames*](https://mjtsai.com/blog/2017/03/24/apfss-bag-of-bytes-filenames/), both accessed 2026-06-28). The "same" name has two
  byte representations; without one canonical form the same file shows up as two
  leaves and never converges.
- **preliminary — confirm in Phase 2:** confirm **NFC vs NFD** as canonical (NFC
  is the leaning, matching Windows/Linux majority), confirm **where** normalisation
  happens (scan-time), and handle the APFS edge case where two files differing
  only by normalisation can coexist on disk (a collision class like XP-4). Use
  `golang.org/x/text/unicode/norm`; this adds a dependency — log it. Unit-test
  NFC/NFD round-trips per plan/README.md ("Unicode NFC/NFD normalisation unit
  tests").

## XP-3 — Reserved names and illegal characters: detect, and escape-or-reject

- **Rule (proposed):** a name is **Windows-unsafe** if it (a) contains a reserved
  character, (b) contains a control character, (c) is a reserved device name, or
  (d) ends in a space or period. On the receiving (Windows) side, such a name
  from a peer must be **escaped to a safe on-disk form** (reversible) or
  **refused and flagged** — never written verbatim in a way that silently
  mangles or fails. The canonical tree key keeps the original name; only the
  on-disk Windows form is escaped.
- **Why (all verbatim from [Microsoft, *Naming Files, Paths, and Namespaces*](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file), accessed 2026-06-28):**
  - **Reserved characters:** `< > : " / \ | ? *`, plus "Integer value zero ...
    the ASCII NUL character" and "Characters whose integer representations are in
    the range from 1 through 31".
  - **Reserved device names:** "CON, PRN, AUX, NUL, COM1, COM2, COM3, COM4, COM5,
    COM6, COM7, COM8, COM9, COM¹, COM², COM³, LPT1 ... LPT9, LPT¹, LPT², and
    LPT³. Also avoid these names followed immediately by an extension; for
    example, NUL.txt and NUL.tar.gz are both equivalent to NUL." Note the
    superscript subtlety: "Windows recognizes the 8-bit ISO/IEC 8859-1 superscript
    digits ¹, ², and ³ as digits and treats them as valid parts of COM# and LPT#
    device names."
  - **Trailing space/period:** "Do not end a file or directory name with a space
    or a period ... However, it is acceptable to specify a period as the first
    character of a name."
  - **Case:** "Do not assume case sensitivity. For example, consider the names
    OSCAR, Oscar, and oscar to be the same" (see XP-4).
  - **MAX_PATH:** "In editions of Windows before Windows 10 version 1607, the
    maximum length for a path is MAX_PATH, which is defined as 260 characters."
    Long paths need the `\\?\` prefix and Unicode APIs.
- **preliminary — confirm in Phase 2:** **decide & log the escape vs reject
  strategy** (e.g. percent-encode unsafe codepoints reversibly, append a marker
  for trailing dot/space, suffix-disambiguate reserved names) and the
  MAX_PATH/long-path handling. Build the **table-driven Windows-hostile name set**
  the plan calls for and round-trip it without loss.

## XP-4 — Case-insensitivity collisions: detect, refuse, and flag — never clobber

- **Rule (proposed):** two distinct canonical keys that differ **only** by case
  (`File.txt` vs `file.txt`) cannot coexist on a case-insensitive target
  (Windows; macOS default). On detecting such a collision while applying, **do
  not overwrite** — refuse to write the colliding second file and flag the
  collision (surface it; keep both in the tree). Borrow Syncthing's posture:
  refuse + flag rather than silently merge.
- **Why:** Microsoft: "Do not assume case sensitivity ... consider the names
  OSCAR, Oscar, and oscar to be the same" ([Microsoft naming page](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file), accessed 2026-06-28). macOS default
  volumes are case-insensitive/case-preserving. If two case-variant files arrive
  from a case-sensitive source (Linux peer, or a case-sensitive macOS volume),
  blindly writing the second clobbers the first → silent data loss, violating
  SR-7's no-data-loss spirit.
- **preliminary — confirm in Phase 2:** confirm detection mechanism (case-folded
  index), the exact refuse/flag UX, and whether to fold case in the canonical key
  or keep keys case-sensitive but maintain a case-folded collision index. This
  cannot be fully verified on the Mac (needs a real Windows/NTFS target — plan/README.md).

## XP-5 — Watcher event drops differ per OS; rescan is the safety net

- **Rule:** never assume the watcher delivered every event. On Windows, raise the
  `ReadDirectoryChangesW` buffer via `WithBufferSize` and on overflow trigger a
  full rescan; on macOS, expect FSEvents-style coalescing and Spotlight noise.
  The periodic + on-overflow full rescan (SR-11, GR-9) is what makes missed
  events recoverable on both platforms.
- **Why:** fsnotify reports `ErrEventOverflow` — Windows: "The buffer size is too
  small; WithBufferSize() can be used to increase it" with a default
  `ReadDirectoryChangesW()` buffer of 64K; on directory removal Windows "may not
  send events for all files in that directory"; and the Windows backend "doesn't
  remove the watcher on renames" ([pkg.go.dev/github.com/fsnotify/fsnotify](https://pkg.go.dev/github.com/fsnotify/fsnotify), accessed 2026-06-28).
  macOS lacks a native FSEvents backend in fsnotify today (uses kqueue, one FD per
  watched file) and Spotlight indexing "can result in multiple events."
- **preliminary — confirm in Phase 2:** quantify the Windows buffer size to use,
  confirm rename-watch cleanup on the Windows backend, and validate the
  debounce window (~150 ms) under load on a **real Windows box** — explicitly out
  of reach on the Mac (plan/README.md: "ReadDirectoryChangesW buffer-overflow
  event drops vs macOS FSEvents coalescing").

## XP-6 — `mode` / `mtime` semantics are not portable; do not over-trust them

- **Rule (proposed):** treat the POSIX permission bits and the executable bit as
  best-effort across Windows (which lacks the same model); keep them in `FileInfo`
  but exclude raw mtime from the structural hash (already pinned in the leaf-shape
  decision). Symlinks and the exec bit get a documented, lossy mapping on Windows.
- **Why:** mode bits and high-resolution mtime do not have identical meaning on
  NTFS vs APFS/HFS+; hashing them structurally would manufacture cross-OS diffs
  (SR-5). The leaf-shape decision already excludes mtime/size from the structural
  hash for this reason (`docs/audit/decisions/phase0/merkle-leaf-shape.md`).
- **preliminary — confirm in Phase 2:** decide the exact Windows mapping for the
  exec bit and symlinks, and whether mode participates at all in conflict
  detection.

---

## Phase-2 confirmation checklist (CLOSED by crossplatform-researcher, 2026-06-28)

Each item is resolved by a logged decision (`docs/audit/decisions/crossplatform/`)
and a finding (`docs/audit/findings/crossplatform/`). `[x]` = Phase-2 research +
decision complete. The "→ Phase 6" tail names the sub-part that still needs a real
Windows/NTFS target (plan/README), to be closed by the CI windows-latest job + the
manual `CROSS_PLATFORM_CHECKLIST.md`.

- [x] **XP-1** — forward-slash relative canonical; UNC/`\\?\`/drive-root stripping.
  Decision: none new (hard rule SR-13/GR-12); finding `path-separators.md`. Repro:
  `filepath.Separator == '/'` on this Mac vs `\` on Windows. → Phase 6: deep-tree
  Mac→Windows→Mac round-trip + prefix stripping on real Windows.
- [x] **XP-2** — NFC locked as canonical, normalised at scan-time + on receive, per
  component; raw on-disk name kept for I/O. Decision `unicode-canonical-form.md`;
  finding `unicode-normalization.md`. Repro proved APFS is normalization-preserving
  (`os.ReadDir` returns NFC or NFD as written). APFS dual-form + Linux-peer
  normalisation collision routed to the collision policy. → Phase 6: Windows end of
  the round-trip.
- [x] **XP-3** — reversible percent-escape on the OS form, refuse+flag fallback;
  Windows-hostile test table built and round-tripped (runnable repro: all cases
  lossless + escaped-legal). MAX_PATH via Go `os.fixLongPath`, never hand-`\\?\`.
  Decisions `illegal-name-strategy.md` + `maxpath-longpath-handling.md`; finding
  `filename-legality.md`. → Phase 6: reserved-name/ADS/trailing-dot/>260 real
  writes on Windows.
- [x] **XP-4** — case-sensitive NFC keys + fold-and-normalise collision index;
  refuse+flag (never clobber), optional `.case-conflict` copy; startup
  case-sensitivity probe. Decision `case-and-normalization-collision-policy.md`;
  finding `case-sensitivity.md`. Repro proved silent clobber on this Mac's APFS.
  → Phase 6: real NTFS case-collision behaviour.
- [x] **XP-5** — events are hints + ~150 ms debounce + periodic rescan as truth +
  overflow→rescan; fsnotify (kqueue on macOS, **not** FSEvents); Windows
  `WithBufferSize` >64 KiB local-only; watch-set reconcile for the
  Windows-no-remove-on-rename + non-recursive realities. Decision
  `watcher-trust-and-debounce.md`; finding `watcher-reality.md`. → Phase 6: real
  `ReadDirectoryChangesW` overflow/drop, exact buffer value, rename-watch cleanup
  under load.
- [x] **XP-6** — `mode` canonicalised to a portable 2-state `{exec, fileType}`
  **before** hashing (amends leaf-shape — raw `mode` in the hash diverges
  Mac↔Windows); symlinks as typed leaves, refuse+flag on unprivileged Windows;
  `mtime` stays out of the structural hash. Decision `mode-symlink-mapping.md`;
  finding `mode-symlink-portability.md`. → Phase 6: exec-bit round-trip +
  symlink-privilege behaviour on Windows. **Hand-off:** merkle-researcher folds the
  2-state mode into the exact structural-hash grammar (OQ-4); tree-critic notified.
