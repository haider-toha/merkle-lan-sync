# Cross-platform checklist — manual Mac ↔ Windows pass (run once on real hardware)

**Why this file exists.** Merkle Sync is written and CI-tested on a Mac, but the
requirement is Mac ↔ Windows sync. Some behaviour *cannot* be proven from one
machine or from a Linux/macOS CI runner (plan/README.md, "the one thing that is NOT
fully autonomous"). The CI matrix (`.github/workflows/ci.yml`) runs the full `-race`
suite — including the two-instance loopback scenarios — on a real `windows-latest`
runner, which closes the *protocol* half of the gap. This checklist closes the
*real-filesystem + real-network* half: NTFS case/normalisation collisions, reserved
device names, NFD↔NFC over the wire, Windows Firewall / multicast discovery, and
`ReadDirectoryChangesW` watcher overflow.

> **Treat "green on the Mac / green in CI" as necessary but not sufficient.** A box
> is only signed off when every PASS below is checked on real hardware.

Each item cites the rule/finding it exercises and the code that implements it, and
states an explicit **PASS** criterion. Evidence URLs were captured 2026-06-28 in the
linked findings.

---

## 0. Setup (do this first)

Prereqs:
- One **macOS** machine and one **Windows 10/11** machine on the **same LAN / same
  subnet** (multicast must reach both; a bridged VM works, NAT'd usually does not).
- Go toolchain on both (`go version` ≥ the version in `go.mod`).
- The repo checked out on both.

Build the daemon on each box:
```
# macOS
go build -o msync ./cmd/msync
# Windows (PowerShell)
go build -o msync.exe ./cmd/msync
```

Pair the two devices (TOFU allow-list, PR-7 — each must trust the other's DeviceID):
1. On **each** box, start the daemon once to mint + print its DeviceID, then Ctrl-C:
   ```
   ./msync -dir ./syncroot -port 22000        # macOS
   .\msync.exe -dir .\syncroot -port 22000    # Windows
   ```
   Note the line `device id: <hex>` from each. The identity persists under
   `<dir>/.msync`, so the ID is stable on restart.
2. Restart each daemon trusting the other:
   ```
   ./msync -dir ./syncroot -port 22000 -peer <WINDOWS_DEVICE_ID>      # macOS
   .\msync.exe -dir .\syncroot -port 22000 -peer <MAC_DEVICE_ID>      # Windows
   ```

| Field | Value |
|---|---|
| Date / tester | |
| macOS version | |
| Windows version | |
| macOS DeviceID | |
| Windows DeviceID | |
| Go version (each) | |

---

## 1. Discovery over a real LAN + Windows Firewall  — rule SR (discovery), finding `crossplatform/watcher-reality` companion

The Mac demo uses `en0` multicast; on Windows the first run pops a **Windows Defender
Firewall** prompt. Discovery is UDP **239.192.0.77:21027**
(`internal/discovery/discovery.go` `DefaultGroup`).

Steps:
1. Start both daemons (Section 0 step 2).
2. On the Windows Firewall prompt, **Allow** `msync.exe` on Private networks.
3. Watch each log for a peer-connected line (a TLS handshake completing with the
   paired DeviceID).

- [ ] **PASS:** within ~10 s (one announce interval) each box logs the other as a
  connected peer. If not: confirm same subnet, the firewall allowed UDP 21027 in +
  out, and the interface is not "Public".

---

## 2. End-to-end convergence / conflict / deletion / rename on real hardware  — SR-5, SR-7, SR-9, SR-10, PR-5

Mirror the in-process scenarios (which pass on the windows-latest CI runner over
loopback) on two **real** boxes and a **real** network.

1. **Convergence (SR-5).** With both connected, on macOS create `docs/a.txt`
   ("alpha") and on Windows create `docs\b.txt` ("bravo"). Wait to quiesce.
   - [ ] **PASS:** both folders contain *both* files with identical bytes; neither
     daemon logs a sustained churn (no sync loop).
2. **Conflict, no data loss (SR-7).** Disconnect (stop one daemon). Edit the *same*
   file `shared.txt` to **different** content on each box. Reconnect.
   - [ ] **PASS:** both boxes end with `shared.txt` (the winner) **and** one
     identically-named `shared.sync-conflict-<UTC>-<devicehex>.txt` (the loser);
     **no byte-set is lost**; the conflict-copy filename is byte-identical on both.
3. **Deletion, no resurrection (SR-9/SR-10).** Stop the Windows daemon. Delete a
   shared file on macOS; let it quiesce. Restart Windows (it still holds the old
   file).
   - [ ] **PASS:** Windows deletes its copy on reconnect and the file is **not**
     re-created on macOS (the tombstone dominates the stale copy).
4. **Rename (PR-5).** Rename `old.txt` → `new.txt` on macOS while connected.
   - [ ] **PASS:** Windows ends with `new.txt` (original bytes) and **no** `old.txt`.

---

## 3. Case-insensitive collision — `File.txt` vs `file.txt`  — XP-4 / SR-7, finding `crossplatform/case-sensitivity`

NTFS is case-**insensitive**; a case-sensitive macOS/Linux peer can legitimately hold
two files differing only in case. The receiver must **refuse + flag, never clobber**
(`internal/pathnorm/casefold.go` `Fold`; `internal/reconcile/transfer.go`
`noClobberConflict` → `ErrCaseClobber`). Reference behaviour: Syncthing refuses and
flags the collision rather than overwriting.

Steps (needs a **case-sensitive** source — a case-sensitive APFS volume on macOS, or
a Linux peer):
1. On the case-sensitive box create both `Data.txt` ("UPPER") and `data.txt`
   ("lower") in the sync root. Let it sync to Windows.

- [ ] **PASS:** Windows keeps exactly one of them and **logs a refusal**
  (`refused case/normalisation clobber`) for the other — the existing file's bytes are
  **untouched** (no silent overwrite). The collision is surfaced, not lost.
- [ ] **PASS (reverse):** the data on the case-sensitive side is never corrupted by
  the round-trip.

---

## 4. Reserved device names, trailing dot/space, MAX_PATH  — XP-3, finding `crossplatform/filename-legality`

Windows rejects names containing `< > : " / \ | ? *`, control chars, the reserved
device stems (**CON, PRN, AUX, NUL, COM1–COM9, LPT1–LPT9**, case-insensitive,
stem-only), and any name ending in a **space or period**; path length is capped at
**260** unless long-path/`\\?\` is used (Microsoft, *Naming Files, Paths, and
Namespaces*). A name a Mac/Linux peer produces verbatim must land on NTFS via the
**reversible escape** (`internal/pathnorm/windows.go`
`IsWindowsUnsafe`/`EscapeForWindows`/`UnescapeFromWindows`), keeping the canonical
tree key intact; the unrepresentable residue is **refuse + flag**.

Steps (create on the macOS/Linux side, sync to Windows):
1. Files named: `CON`, `aux.txt`, `name.` (trailing dot), `space ` (trailing space),
   `a:b.txt` (colon), `q?x.txt` (question mark).
2. A nested path whose full Windows path exceeds 260 chars.

- [ ] **PASS:** each lands on Windows as a readable, **reversible** on-disk name (or
  is explicitly refused + flagged) — the daemon never crashes and never writes a
  truncated/merged name.
- [ ] **PASS:** syncing back to macOS restores the **original** names byte-for-byte
  (the escape round-trips; canonical key preserved).
- [ ] **PASS:** the >260-char path is either created via long-path handling or
  cleanly refused + flagged — never a silent partial write.

---

## 5. Unicode NFD ↔ NFC  — XP-2 / SR-13, finding `crossplatform/unicode-normalization`

macOS is normalization-**preserving, not normalizing**: the "same" name can exist in
decomposed (NFD) or composed (NFC) bytes. Canonical form is **NFC**, applied per
component at scan and on receive (`internal/pathnorm/normalize.go`,
`golang.org/x/text/unicode/norm`). The same logical name must be **one** leaf on both
OSes, or the roots never converge (SR-5).

Steps:
1. On macOS create `résumé.txt` two ways: once typed/pasted as **NFC** (single
   `é` = U+00E9), once composed as **NFD** (`e` + U+0301). (Use a script if needed to
   force the byte form.)
2. Sync to Windows and back.

- [ ] **PASS:** Windows shows **one** `résumé.txt` (NFC) — no duplicate/ghost file,
  no second leaf, roots converge.
- [ ] **PASS:** a name that is NFD on disk on macOS is matched to the same canonical
  leaf as its NFC twin (no spurious "new file" / no churn).

---

## 6. Path separators / deep-tree round-trip  — XP-1 / SR-13, finding `crossplatform/path-separators`

Canonical keys are forward-slash relative; OS separators appear only at the FS call
(`internal/pathnorm/pathnorm.go` `ToOSPath`/`FromOSPath`, plus `\\?\`/UNC/drive
stripping). A stored `\` would poison the cross-OS hash and break convergence.

Steps:
1. Create a deep nested tree on Windows, e.g. `a\b\c\d\e\f\g.txt`.
2. Sync to macOS, modify `g.txt` on macOS, sync back.

- [ ] **PASS:** the subtree appears on macOS as `a/b/c/d/e/f/g.txt` with identical
  subtree hashes; the modification round-trips; no path component is mangled and no
  duplicate directory appears.

---

## 7. Watcher overflow → rescan recovers  — XP-5 / SR-11, finding `crossplatform/watcher-reality`

`ReadDirectoryChangesW` silently **discards the whole buffer on overflow** (returns
TRUE with 0 bytes / `ERROR_NOTIFY_ENUM_DIR`); the 64 KiB buffer overflows under a
bulk change. Correctness must not depend on the event stream — the **periodic full
rescan is the source of truth** (SR-11; fsnotify surfaces `ErrEventOverflow`).

Steps (on Windows, with both daemons connected):
1. In one operation, create/modify a **large number** of files at once (e.g. extract
   a zip of 5,000+ small files, or a scripted loop) to overflow the watcher buffer.

- [ ] **PASS:** despite a likely watcher-overflow log, **all** files converge to
  macOS after the next rescan interval — no file is permanently missed.
- [ ] **PASS:** the daemon recovers without a restart (the overflow triggers an
  immediate rescan).

---

## 8. Atomic transfer — kill mid-stream leaves no corrupt file  — SR-1 / SR-2

Proven in-process (`TestKilledTransfer_NoCorruptFileThenRecovers`) and on the CI
windows runner; confirm on real hardware with a large file and a real process kill.
Writes go to a temp + verify-before-rename (`internal/reconcile/transfer.go`
`atomicWriteVerify`).

Steps:
1. Place a large file (e.g. 200 MB) on macOS. Start the sync to Windows.
2. **Kill** the Windows daemon (Task Manager / Ctrl-C) while the transfer is in
   progress.

- [ ] **PASS:** the destination file on Windows is **absent** (or the prior complete
  version) — never a partial/corrupt file; **no `.msync-*.tmp`** lingers in the
  target folder.
- [ ] **PASS:** restarting the Windows daemon completes the transfer and the file is
  byte-identical to the macOS original.

---

## 9. File ↔ directory type clash — refuse + flag  — MK-2 (Phase 7), SR-7

A path can legitimately be a **file** on one box and a **directory** on the other —
e.g. macOS has `notes` as a file while Windows has `notes\` containing files (a delete
+ recreate-as-dir, or two independent creates). The two are irreconcilable at one path
without choosing a loser. v1 **refuses + flags** (no data lost; both keep their own),
exactly like the case-clobber refuse — never an impossible `mkdir`/`rename` retry loop.
The differ reports the clash truthfully (`internal/merkle/differ.go`
`DiffEntry.IsTypeClash`); the engine refuses it (`internal/reconcile/engine.go`
`flagTypeClash` → `ErrTypeClash`). Decision:
`docs/audit/decisions/phase7/MK-2-file-vs-dir-typeclash-resolution.md`.

Steps (with both daemons connected):
1. On **macOS** create a **file** `notes` ("mac file") in the sync root.
2. On **Windows** create a **directory** `notes\` containing `notes\inner.txt`
   ("win dir child"). Let it quiesce.

- [ ] **PASS:** each daemon **logs** `refused file-vs-directory type clash` naming
  `notes`; **no daemon crashes, no sustained retry churn** (no repeated mkdir/rename
  failures).
- [ ] **PASS (no data loss):** macOS still has its file `notes` byte-intact; Windows
  still has `notes\inner.txt` byte-intact. Nothing is deleted or overwritten on either
  side. (The path stays divergent and flagged — resolve by hand by renaming one side.)
- [ ] **PASS (recovery):** after renaming the macOS file `notes` → `notes-mac`, the
  next reconcile converges: both boxes end with `notes-mac` (file) **and** `notes/inner.txt`
  (dir) — the previously-pruned subtree now syncs.

---

## Sign-off

| # | Area | Result (PASS/FAIL) | Notes / log excerpt |
|---|---|---|---|
| 1 | Discovery + firewall | | |
| 2 | Convergence/conflict/deletion/rename | | |
| 3 | Case-insensitive collision | | |
| 4 | Reserved names / dot-space / MAX_PATH | | |
| 5 | Unicode NFD↔NFC | | |
| 6 | Path separators / deep tree | | |
| 7 | Watcher overflow → rescan | | |
| 8 | Atomic transfer kill | | |
| 9 | File↔directory type clash refuse | | |

**Box is cross-OS-signed-off only when every row is PASS.** File any FAIL as a
finding under `docs/audit/findings/crossplatform/` with the log excerpt and the
offending name/path, and re-run after the fix.
