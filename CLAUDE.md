# CLAUDE.md — Merkle Sync agent contract (hard rules)

Module: `github.com/haider-toha/merkle-sync` · Go 1.23 · decentralised LAN
file-sync engine, **Mac ↔ Windows**, no central server, raw TCP + UDP multicast,
**Merkle trees are the source of truth for what differs**.

This file is the contract for anyone (human or agent) writing code here. Read it
before touching `internal/` or `cmd/`. It points at the authoritative rule files
and decisions; it does not restate them in full.

> The distilled implementer spec (Merkle diff algorithm, version-vector scheme,
> framing spec, path-normalisation rules) lives in
> [`.claude/skills/merkle-sync/SKILL.md`](.claude/skills/merkle-sync/SKILL.md).
> Per-role contracts live in [`.claude/agents/`](.claude/agents/).

---

## 0. The contract in one paragraph

This program guards the user's files. A race that double-applies a change, a
non-atomic write, a mishandled deletion, or a denormalised filename is **data
loss**, not a flake. Four invariants are non-negotiable and graded by the Phase 6
flow-verifier: **(1) convergence** — after changes settle, both peers expose the
*identical* Merkle root hash; **(2) no data loss on conflict** — the losing side
is renamed to a conflict copy, never deleted; **(3) atomic transfer** — a transfer
killed mid-stream leaves no corrupt file; **(4) no sync loop** — receiving a file
produces zero outbound hash broadcasts. Everything below serves these four.

---

## 1. Authoritative rules (read these; they are binding)

These are HARD constraints, each with evidence and a test obligation. Code is
reviewed against their stable IDs (`SR-n`, `GR-n`, `XP-n`); cite the ID in commits
and findings.

- **Sync invariants** — [`docs/audit/rules/sync-rules.md`](docs/audit/rules/sync-rules.md) (`SR-1`..`SR-13`)
  - SR-1/SR-2 atomic write (temp → fsync → rename → fsync dir); SR-3 idempotent
    content-addressed apply; SR-4 version vectors, not mtime, order edits; SR-5
    convergence ⇔ equal root hash; SR-6 broadcast only after a *local* change;
    SR-7 conflict loser renamed never deleted; SR-8 a received file is not a local
    change (break the watcher echo); SR-9 deletions are tombstones; SR-10 a stale
    peer must not resurrect a deletion; SR-11 watcher events are hints, rescan is
    truth; SR-12 framing max-length guard; SR-13 canonical forward-slash identity.
- **Go idioms for this domain** — [`docs/audit/rules/go-rules.md`](docs/audit/rules/go-rules.md) (`GR-1`..`GR-13`)
  - GR-2 one `context` tree from `signal.NotifyContext`; GR-3 every goroutine has
    a `WaitGroup` owner (no leaks on peer disconnect); GR-4 three listeners
    coordinate only via context + channels; GR-5 one `sync.RWMutex` guards the
    tree, **zero I/O under the lock**; GR-6 wrap errors with `%w` + sentinels;
    GR-7 `encoding/binary` not `gob` from the network; GR-8 `io.ReadFull` + max-len
    guard; GR-9 fsnotify is advisory; GR-10 debounce ~150 ms; GR-11 stdlib-first;
    GR-12 `path` for canonical keys, `filepath` only at the OS boundary; GR-13
    `-race` is mandatory.
- **Cross-platform path/filename rules** — [`docs/audit/rules/crossplatform-rules.md`](docs/audit/rules/crossplatform-rules.md) (`XP-1`..`XP-6`)
  - XP-1 canonical = forward-slash relative; XP-2 normalise Unicode to **NFC** at
    the boundary; XP-3 reserved names / illegal chars / trailing dot-space →
    escape-or-reject; XP-4 case-insensitivity collisions → refuse + flag, never
    clobber; XP-5 watcher drops differ per OS, rescan is the net; XP-6 `mode`/`mtime`
    are not portable — excluded from the structural hash.

Phase 0 decisions these implement (read before writing the matching package):
[`framing-format.md`](docs/audit/decisions/phase0/framing-format.md),
[`merkle-leaf-shape.md`](docs/audit/decisions/phase0/merkle-leaf-shape.md),
[`transport-security-tofu-vs-plaintext.md`](docs/audit/decisions/phase0/transport-security-tofu-vs-plaintext.md),
[`message-type-codes.md`](docs/audit/decisions/phase0/message-type-codes.md).

Proposed file layout + dependency DAG: [`docs/audit/plan/structure.md`](docs/audit/plan/structure.md).
**The package boundaries and dependency direction there are the contract** —
`protocol` and `pathnorm` are leaves; `protocol` never imports `merkle`;
`reconcile` is the only package that mutates tree state.

---

## 2. The five hard rules you will most often get wrong

1. **Never write a destination file in place.** Temp-write in the same directory →
   `fsync` temp → `os.Rename` → `fsync` parent dir. On any error before the rename,
   delete the temp and leave `dst` untouched. (SR-1, SR-2)
2. **Version vectors decide ordering, never wall-clock mtime.** `mtime` is *only*
   the conflict tiebreaker when version vectors say two edits are concurrent.
   (SR-4, SR-7)
3. **Bump your own version-vector counter only on a confirmed *local* change.**
   Applying a received file never bumps your counter and never broadcasts —
   that is the no-sync-loop invariant. (SR-6, SR-8)
4. **Canonical identity is a forward-slash, relative, NFC-normalised path.** Use
   the `path` package for keys; convert with `filepath.From/ToSlash` only at the
   filesystem call. Never store `\` or a denormalised name. (SR-13, XP-1, XP-2,
   GR-12)
5. **Zero I/O while holding the tree lock.** Copy the `FileInfo`/subtree you need
   under `RLock`/`Lock`, release, *then* do network/disk I/O. (GR-5)

---

## 3. Build / test / cross-compile

```sh
go build ./...                                   # compiles every package
go vet ./...                                     # static checks (CI gate)
go test ./... -race -count=1                     # race detector is mandatory (GR-13)
GOOS=windows GOARCH=amd64 go build ./cmd/msync   # prove the Windows daemon compiles
```

Targeted runs you will use often:

```sh
go test ./internal/pathnorm -run TestRoundTrip -v        # Windows-hostile name set
go test ./internal/protocol -run TestFrame   -v          # split-read + oversized-length framing
go test ./test/integration -run TestTwoNodeConverge -v   # two-instance convergence (SR-5)
```

Gate rules:

- **`go test ./... -race -count=1` must be green** before any workstream item is
  marked done. `-count=1` defeats the test cache; `-race` is non-optional because
  the data at stake is the user's files (GR-13).
- **`GOOS=windows GOARCH=amd64 go build ./cmd/msync` must pass.** "Green on the
  Mac" is necessary but **not sufficient** — real Mac↔Windows behaviour (NTFS case
  collisions, reserved names, `ReadDirectoryChangesW` drops, multicast through
  Windows Firewall) is closed by two artifacts only: the CI matrix at
  [`.github/workflows/ci.yml`](.github/workflows/ci.yml) (ubuntu/macos/**windows-latest**)
  and the manual `docs/audit/CROSS_PLATFORM_CHECKLIST.md` (Phase 6). Treat anything
  on that list as unverified until both are green.
- Dependencies: **stdlib-first** (GR-11). The only expected third-party deps are
  `github.com/fsnotify/fsnotify` and `golang.org/x/text/unicode/norm`. Any further
  dependency needs a logged decision in `docs/audit/decisions/`.

---

## 4. How to add a feature (the checklist)

Adding a message type or any sync behaviour follows this exact order. Skipping a
step is how data-loss bugs ship.

1. **Finding first.** Write `docs/audit/findings/<area>/<slug>.md` stating what the
   feature needs and *why*, with evidence (URL + access date, or `file:line`, or a
   runnable repro). No memory-only claims.
2. **Decision before code.** If the feature involves a consequential choice,
   enumerate **≥3 real options**, score each on *correctness / concurrency-safety /
   testability / cross-platform*, pick one, and write
   `docs/audit/decisions/<area>/<slug>.md` (Context / Options / Decision /
   Rationale / Consequences) **before** writing the code.
3. **Protocol message.** Add the type in `internal/protocol/` — a new `MsgType`
   constant from the unassigned range (`0x08`+; never reuse or renumber an existing
   code) and its envelope encode/decode. **Keep framing backward-compatible**: the
   `[4-byte len][1-byte type][payload]` frame never changes.
4. **Handler.** Implement the handler in the relevant `internal/` package
   (`reconcile` for engine behaviour, `transport` for wire concerns, etc.).
   Honour the rule IDs it touches; cite them.
5. **Table-driven test, with Windows inputs.** Add a table-driven unit test. **If
   it touches paths or filenames, you must include Windows-hostile cases** —
   reserved chars/names, trailing dot/space, NFC/NFD pairs, case collisions
   (XP-3, XP-4, SR-13). A path-touching change without Windows-input cases is
   incomplete.
6. **Integration scenario.** Add a two-instance scenario in `test/integration/`
   that proves the relevant invariant (convergence / no-loss / atomic / no-loop).
7. **Windows build.** Confirm `GOOS=windows GOARCH=amd64 go build ./cmd/msync`
   still passes and update the CI matrix / `CROSS_PLATFORM_CHECKLIST.md` if the
   feature has cross-OS behaviour that the Mac cannot verify.

Commit format: `feat(ws<n>): <desc> [fixes finding-<id>]`. Mark a plan item done
only when `go build ./... && go test ./... -race` is green and the cross-compile
passes; set the finding to `fixed` with the commit SHA.

---

## 5. Working agreement (autonomy contract)

- **Decisions are logged before they are acted on.** ≥3 scored options, one
  decision file under `docs/audit/decisions/<area>/<slug>.md`, then code.
- **Every claim cites evidence** — a URL with access date, a `file:line`, or a
  runnable reproduction. Ground current (2025–2026) facts with a web search and
  cite them; no memory-only assertions.
- **Canonical paths are forward-slash relative**, never OS-specific separators,
  everywhere they are stored, keyed, or sent.
- **Do not run `git`** unless the task explicitly grants it. Create parent
  directories with the editor/Write tool as needed.
- When in doubt, prefer the choice that **cannot lose data**, even if slower.
