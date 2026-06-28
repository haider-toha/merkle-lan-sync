# Literature finding — Version vectors (causality for two-way sync)

- **Source slug:** `version-vectors`
- **Phase / role:** Phase 1 — literature-mapper
- **Reads-first honoured:** `docs/audit/rules/{sync-rules,go-rules,crossplatform-rules}.md`,
  `docs/audit/decisions/phase0/merkle-leaf-shape.md`, `docs/audit/plan/structure.md`,
  `plan/README.md`, `plan/agent_roster.md`
- **Status:** complete (recommendations feed Phase 2 `merkle-researcher` /
  `protocol-researcher`; two binding sub-decisions are explicitly *deferred* to
  Phase 2 — see §9, consistent with the leaf-shape decision's Consequences)
- **Date / access date for all URLs:** 2026-06-28
- **Evidence policy:** every claim below is grounded in a cited URL (with access
  date) or a `file:line` in this repo. The most load-bearing artifact is the
  **real Syncthing source** (`lib/protocol/vector.go`), reproduced verbatim,
  because Merkle Sync's `internal/protocol/versionvector.go` will adopt/adapt it.

---

## 1. TL;DR

A **version vector** is a map `deviceID → counter` attached to each versioned
object (here: each file leaf's `FileInfo`). A device bumps **only its own**
counter, and **only when it makes a local change**; on sync, two vectors are
merged by taking the **pointwise maximum**. Comparing two vectors yields exactly
one of three answers that drive the whole reconciliation engine:

- **A dominates B** (A ≥ B everywhere, A > B somewhere) ⇒ A causally *supersedes*
  B → A is simply newer, apply A, **no conflict**.
- **B dominates A** ⇒ symmetric.
- **neither dominates** (A > B in some slot *and* B > A in another) ⇒ the edits
  are **concurrent** ⇒ a genuine **conflict** → keep both (SR-7 conflict copy).

This is the precise mechanism `SR-4` ("version vectors, not mtime, are the source
of truth for ordering"), `SR-6` (bump own counter only on a confirmed local
change), `SR-7` (concurrent ⇒ conflict copy), and `SR-9`/`SR-10` (tombstone
dominance ⇒ no resurrection) all depend on. Version vectors are *the* reason a
bare-content-hash Merkle leaf is insufficient for two-way sync
(`docs/audit/decisions/phase0/merkle-leaf-shape.md:11-31`).

---

## 2. Core algorithm

### 2.1 Definition and the three operations

A version vector over a set of devices `D` is a function `V : D → ℕ` (in practice
a sparse map; absent device ⇒ counter 0). The canonical rules
([Version vector, Wikipedia](https://en.wikipedia.org/wiki/Version_vector),
accessed 2026-06-28):

1. **Init:** "Initially all vector counters are zero."
2. **Local update:** "Each time a replica experiences a local update event, it
   increments its own counter in the vector by one." (i.e. `V[self] += 1`.)
3. **Synchronise (merge):** when replicas `a` and `b` reconcile, "they both set
   the elements in their copy of the vector to the maximum of the element across
   both counters" (i.e. `∀d: V[d] = max(Va[d], Vb[d])`).

### 2.2 The partial order — how "supersede vs conflict" is decided

Define the comparison (same source):

- **Identical:** `a = b` (equal in every slot).
- **Ordered (causal / happens-before):** `a < b` iff "every element of `V_a` is
  less than or equal to its corresponding element in `V_b`, and at least one of
  the elements is strictly less." Then `b` **dominates** `a`: `b` has seen
  everything `a` has, plus more ⇒ `b` *supersedes* `a`.
- **Concurrent (conflict):** `a ∥ b` iff "neither `a < b` nor `b < a`, yet
  vectors differ" — i.e. each side has at least one slot strictly greater than
  the other. Neither has seen the other's latest edit ⇒ **conflict**.

> Dominance comparison, restated by a practitioner source: "A vector Vx is said to
> dominate Vy when all elements of Vx are greater than or equal to the
> corresponding elements of Vy ... If neither vector dominates the other then they
> are causally concurrent, and potentially in conflict."
> ([Version Vector(III), P. Pandey, LinkedIn](https://www.linkedin.com/pulse/version-vectoriii-pratik-pandey), accessed 2026-06-28).

This three-valued result (`supersede A`, `supersede B`, `conflict`) is the entire
contract the reconciliation engine needs: it tells you **who is newer** *and*
**whether the two histories truly diverged**, without trusting any wall clock.

### 2.3 Why this is correct without synchronized clocks

Because a counter only ever increases for its own device and merges take the max,
`Va[d]` is exactly "the highest-numbered edit by device `d` that replica `a` has
observed." If `b` has observed every edit `a` has (`a ≤ b` everywhere) then `b`'s
state causally includes `a`'s — superseding is safe. If each has observed an edit
the other has not, there are two divergent histories — a real conflict. No global
time is required; this is the whole point versus mtime (`SR-4`,
`docs/audit/rules/sync-rules.md:63-74`).

---

## 3. Version vectors vs vector clocks vs Lamport timestamps

All three are *logical clocks*; they differ in what they can answer and what they
cost. The distinction matters because the roster names it explicitly
(`plan/agent_roster.md:27`).

| Mechanism | State | Increments when | Detects concurrency? | Space |
|---|---|---|---|---|
| **Lamport timestamp** | one scalar per process | every event (and `max+1` on receive) | **No** — total order only | O(1) |
| **Vector clock** | vector `proc → counter` | **every** event: internal, **send**, and **receive** (then pointwise-max merge) | **Yes** | O(n) |
| **Version vector** | vector `replica → counter` | **only on a local data update (write)**; on sync, pointwise-max merge **without** incrementing | **Yes** | O(n) |

- **Lamport (1978):** a single counter gives a consistent *total* order, but
  "it is not evident from Lamport timestamps if an event definitely occurred
  after another event or if the two events are concurrent"
  ([Clocks and Causality, exhypothesi](https://www.exhypothesi.com/clocks-and-causality/),
  accessed 2026-06-28). Useless for *conflict detection* — it would force a winner
  even on truly concurrent edits, i.e. silent data loss. (Origin: Lamport,
  *Time, Clocks, and the Ordering of Events in a Distributed System*, CACM 1978.)
- **Vector clock (Fidge/Mattern, 1988):** comparison rule
  `VC(x) < VC(y) ⟺ ∀z VC(x)_z ≤ VC(y)_z ∧ ∃z' VC(x)_z' < VC(y)_z'`; "If some
  entries are less or equal, and some entries are greater, the timestamps are
  concurrent" ([Vector clock, Wikipedia](https://en.wikipedia.org/wiki/Vector_clock)
  and [exhypothesi](https://www.exhypothesi.com/clocks-and-causality/), both
  accessed 2026-06-28). Increments on **every** event including message
  send/receive.
- **Version vector (Parker et al., 1983):** *same state shape and same comparison
  rule as a vector clock*, but "the update rules differ slightly"
  ([Version vector, Wikipedia](https://en.wikipedia.org/wiki/Version_vector),
  accessed 2026-06-28). The difference is precisely **when you increment**:
  version vectors "increment counters only when data is modified (which results in
  a new version of data), not on every event" — whereas vector clocks increment
  "when an event is generated"
  ([exhypothesi](https://www.exhypothesi.com/clocks-and-causality/), accessed
  2026-06-28). Origin: Parker et al., *Detection of Mutual Inconsistency in
  Distributed Systems*, IEEE TSE 1983 (cited by the Wikipedia article above).

**Why version vectors (not vector clocks) for Merkle Sync.** We are versioning
*files*, not ordering *messages*. We do not want a counter bump every time a peer
sends/receives a protocol frame — that would manufacture spurious causality and
break the no-sync-loop invariant. We want a counter bump *only when a file
actually changes locally*. That is exactly the version-vector update rule, and it
is exactly what `SR-6` mandates ("bump its own counter on a confirmed local
change ... receiving and applying a remote file must not bump our counter",
`docs/audit/rules/sync-rules.md:89-106`). Lamport timestamps are rejected because
they cannot detect concurrency at all.

---

## 4. EXACT data structures / formulas / field layouts (real source)

The reference implementation is Syncthing's `lib/protocol/vector.go`, reproduced
**verbatim** below (real source, accessed 2026-06-28 from
`https://raw.githubusercontent.com/syncthing/syncthing/main/lib/protocol/vector.go`).
Merkle Sync's `internal/protocol/versionvector.go` (per
`docs/audit/plan/structure.md:54`) will adopt this shape with the adaptations in §8.

### 4.1 In-memory structures

```go
type Vector struct {
	Counters []Counter        // INVARIANT: sorted ascending by Counter.ID
}

type Counter struct {
	ID    ShortID             // device key (see §4.5)
	Value uint64              // monotonic per-device counter (see §4.4)
}
```

Key design point: it is a **sorted slice of (ID, Value) pairs**, not a Go `map`.
Sorting by `ID` is what makes `Compare`/`Merge` linear merge-joins and makes
serialization byte-deterministic (critical for the structural hash, `SR-13`).

### 4.2 The comparison result type (three-valued, plus direction)

```go
type Ordering int

const (
	Equal Ordering = iota
	Greater            // v dominates b (v supersedes b)
	Lesser             // b dominates v (b supersedes v)
	ConcurrentLesser   // concurrent (conflict); v "smaller" by tiebreak direction
	ConcurrentGreater  // concurrent (conflict); v "greater" by tiebreak direction
)
```

`Concurrent()` returns true for either `ConcurrentLesser`/`ConcurrentGreater`;
the Lesser/Greater suffix is only a *deterministic direction hint* some call sites
use to pick a stable winner. Semantically there are **three** outcomes:
`Equal | Greater | Lesser | Concurrent` — supersede-this, supersede-that, or
conflict.

### 4.3 `Compare` — the dominance algorithm (VERBATIM, load-bearing)

This is the formula §2.2 turns into code: a lock-step merge over two sorted slices
that flips to `Concurrent*` the moment it sees one side strictly greater in one
slot *and* the other side strictly greater in another.

```go
func (v Vector) Compare(b Vector) Ordering {
	var ai, bi int     // index into a and b
	var av, bv Counter // value at current index

	result := Equal

	for ai < len(v.Counters) || bi < len(b.Counters) {
		var aMissing, bMissing bool

		if ai < len(v.Counters) {
			av = v.Counters[ai]
		} else {
			av = Counter{}
			aMissing = true
		}

		if bi < len(b.Counters) {
			bv = b.Counters[bi]
		} else {
			bv = Counter{}
			bMissing = true
		}

		switch {
		case av.ID == bv.ID:
			// We have a counter value for each side
			if av.Value > bv.Value {
				if result == Lesser {
					return ConcurrentLesser
				}
				result = Greater
			} else if av.Value < bv.Value {
				if result == Greater {
					return ConcurrentGreater
				}
				result = Lesser
			}

		case !aMissing && av.ID < bv.ID || bMissing:
			// Value is missing on the b side
			if av.Value > 0 {
				if result == Lesser {
					return ConcurrentLesser
				}
				result = Greater
			}

		case !bMissing && bv.ID < av.ID || aMissing:
			// Value is missing on the a side
			if bv.Value > 0 {
				if result == Greater {
					return ConcurrentGreater
				}
				result = Lesser
			}
		}

		if ai < len(v.Counters) && (av.ID <= bv.ID || bMissing) {
			ai++
		}
		if bi < len(b.Counters) && (bv.ID <= av.ID || aMissing) {
			bi++
		}
	}

	return result
}
```

A device counter that is **absent** is treated as **0** (the `aMissing`/`bMissing`
branches): a present `Value>0` on one side and absent on the other makes that side
strictly greater in that slot. This is why a tombstone whose VV adds the deleter's
counter **dominates** a stale peer's pre-delete VV (`SR-10`).

### 4.4 `Update` — local-write increment (VERBATIM) and the wall-clock floor

```go
func (v Vector) Update(id ShortID) Vector {
	now := uint64(time.Now().Unix())
	return v.updateWithNow(id, now)
}

func (v Vector) updateWithNow(id ShortID, now uint64) Vector {
	for i := range v.Counters {
		if v.Counters[i].ID == id {
			// Update an existing index
			v.Counters[i].Value = max(v.Counters[i].Value+1, now)
			return v
		} else if v.Counters[i].ID > id {
			// Insert a new index
			nv := make([]Counter, len(v.Counters)+1)
			copy(nv, v.Counters[:i])
			nv[i].ID = id
			nv[i].Value = max(1, now)
			copy(nv[i+1:], v.Counters[i:])
			return Vector{Counters: nv}
		}
	}

	// Append a new index
	return Vector{Counters: append(v.Counters, Counter{
		ID:    id,
		Value: max(1, now),
	})}
}
```

**Non-obvious, important detail:** the counter is not a pure `+1` logical clock —
it is `max(previous+1, now_unix_seconds)`. So each `Value` is **floored to the
current Unix time in seconds**. Consequences, derived directly from the code:

- Two edits by the same device within one second → `value+1` wins (strictly
  increasing). Across seconds → `now` wins. Either way it is **strictly
  monotonic** per device.
- The counter therefore *also encodes a coarse wall-clock timestamp* (values are
  ~1.75 billion today), which makes a device's counter **survive a database/state
  reset**: a wiped device restarts its counter near `now`, not at 1, so it does
  not emit lower counter values than it had before the wipe — avoiding a counter
  **rollback** that would make old remote versions falsely look newer.
- Crucially this does **not** make wall-clock the *ordering* authority (which
  `SR-4` forbids): ordering is still decided by `Compare`'s dominance test; the
  floor is purely an anti-rollback / monotonicity device. See §8 for the
  adopt/adapt recommendation and its caveat.

### 4.5 Device key: `ShortID` = first 64 bits of the device ID

Per the wire spec, "Each counter in the version vector is an ID-Value tuple. The
ID is the first 64 bits of the device ID. The Value is a simple incrementing
counter, starting at zero," and "The version field is a version vector describing
the updates performed to a file by all members in the cluster"
([Block Exchange Protocol v1, Syncthing](https://docs.syncthing.net/specs/bep-v1.html),
accessed 2026-06-28). The full device ID is "a 32 byte number ... the SHA-256 of
the device X.509 certificate" (same spec; and
[Understanding Device IDs](https://docs.syncthing.net/dev/device-ids.html),
accessed 2026-06-28). So the VV key is a **64-bit truncation** of the cert hash —
chosen to keep each counter small (8 bytes) rather than 32.

### 4.6 Wire / serialization layout (BEP v1 protobuf, real spec)

```protobuf
message Vector {
    repeated Counter counters = 1;
}

message Counter {
    uint64 id    = 1;   // first 64 bits of the 32-byte device ID
    uint64 value = 2;   // per-device monotonic counter
}

message FileInfo {
    string       name        = 1;
    FileInfoType type        = 2;
    int64        size        = 3;
    uint32       permissions  = 4;
    int64        modified_s   = 5;   // mtime seconds
    int32        modified_ns  = 11;  // mtime nanoseconds
    uint64       modified_by  = 12;  // ShortID of the device that last changed it
    bool         deleted      = 6;   // tombstone flag
    bool         invalid      = 7;
    bool         no_permissions = 8;
    Vector       version      = 9;   // <-- the version vector lives here
    int64        sequence     = 10;
    int32        block_size   = 13;
    repeated BlockInfo Blocks = 16;
    string       symlink_target = 17;
}
```

(Source: [BEP v1](https://docs.syncthing.net/specs/bep-v1.html), accessed
2026-06-28.) Note `version` (field 9) is the VV; `modified_by` (field 12) records
*which* device produced the most recent change — used by the conflict tiebreaker
(`SR-7`), not by causality itself. This is the concrete realisation of the leaf
shape in `docs/audit/decisions/phase0/merkle-leaf-shape.md:48-61`.

### 4.7 `Merge` — pointwise max keeping the slice sorted (VERBATIM)

```go
func (v Vector) Merge(b Vector) Vector {
	var vi, bi int
	for bi < len(b.Counters) {
		if vi == len(v.Counters) {
			// We've reach the end of v, all that remains are appends
			return Vector{Counters: append(v.Counters, b.Counters[bi:]...)}
		}

		if v.Counters[vi].ID > b.Counters[bi].ID {
			// The index from b should be inserted here
			n := make([]Counter, len(v.Counters)+1)
			copy(n, v.Counters[:vi])
			n[vi] = b.Counters[bi]
			copy(n[vi+1:], v.Counters[vi:])
			v.Counters = n
		}

		if v.Counters[vi].ID == b.Counters[bi].ID {
			if val := b.Counters[bi].Value; val > v.Counters[vi].Value {
				v.Counters[vi].Value = val
			}
		}

		if bi < len(b.Counters) && v.Counters[vi].ID == b.Counters[bi].ID {
			bi++
		}
		vi++
	}

	return v
}
```

**Aliasing footgun (load-bearing for our adaptation):** `Merge` and `Update` take
a **value** receiver `(v Vector)`, but `v.Counters` is a slice header whose
*backing array is shared* with the caller. The in-place writes
(`v.Counters[vi].Value = val`, `v.Counters[i].Value = max(...)`) can therefore
mutate the **caller's** vector when no reallocation happened. Syncthing tolerates
this because of how it owns vectors, but it violates Merkle Sync's
immutable-snapshot model (`GR-5`: "copy out the FileInfo ... immutable value
snapshot"; leaf-shape decision: "`FileInfo` is an immutable value snapshot ...
readers copy it", `merkle-leaf-shape.md:72-77`). See §8 adapt item (A4).

---

## 5. Failure modes (with real-world evidence)

### FM-1 — Ghost counters never pruned on device removal (deletions resurrect; conflict storms)

The single most important real-world failure, documented as an open Syncthing
bug: **when a device is removed, its counters persist forever in every file's
version vector it ever touched**, and can never be incremented again, creating a
*permanent* concurrent state. Symptoms reported:

- "**Deleted files reappear:** When a device holding a deletion is removed, other
  nodes retain non-deleted versions. Since neither vector can dominate (both
  contain ghost counters), the file persists clean rather than deleted." (Directly
  violates `SR-10`.)
- "**Conflict storms:** ... The reporter documented **8,591 conflicts** when
  replacing a device with a fresh instance."
- Root cause: "Ghost counters are cumulative: each device removal adds another set
  of permanent entries. The problem compounds over the lifetime of a cluster," and
  "There is no code path ... to remove a dropped device's counter from their
  version vectors."

([Issue #10590, syncthing/syncthing](https://github.com/syncthing/syncthing/issues/10590),
accessed 2026-06-28.) Suggested fixes there: a `Vector.DropCounter()` API plus a
removal-time sweep. **Merkle Sync must design counter cleanup in from day one**
(§8 A2). This is *the* lesson for our `SR-9`/`SR-10` tombstone story.

### FM-2 — Unbounded growth / "sibling explosion" when keyed by the wrong identity

"As more nodes contribute to an object, the size of its version vector grows ...
even though most people will never contribute again to a file, they are forever
carried along in the version vector" — illustrated as **2.86 MB per object** with
250,000+ historical contributors at 12 bytes/entry
([Logical Clocks in Real Life, K. Nath, Medium](https://medium.com/geekculture/all-things-clock-time-and-order-in-distributed-systems-logical-clocks-in-real-life-2-ad99aa64753),
accessed 2026-06-28). The academic framing: keying a VV by **client** IDs makes it
grow with the number of clients — "If 1000 different clients write to a key/value,
its VV will have 1000 (client_id, counter) entries" — causing **sibling
explosion** / false conflicts
([Dotted-Version-Vectors, R. Gonçalves et al.](https://github.com/ricardobcl/Dotted-Version-Vectors),
accessed 2026-06-28). Storage cost is a real Syncthing concern too
([Issue #6372 "Reduce database size by optimizing version list storage"](https://github.com/syncthing/syncthing/issues/6372),
accessed 2026-06-28). *Mitigated for us by keying on **device** (replica) IDs and
a small device count — see §6 and §8.*

### FM-3 — False conflicts from unequally pruned vectors

If two peers prune their vectors differently, a comparison can mistake causal for
concurrent: "two nodes ... exchanging version vectors [that] have not been equally
pruned ... may lead to a false reporting of conflicts. Virtual pruning can be used
to address the unequally pruned vector problem"
([Version Vector(III), LinkedIn](https://www.linkedin.com/pulse/version-vectoriii-pratik-pandey),
accessed 2026-06-28). Implication: **pruning is not free** — any pruning scheme we
adopt must be coordinated or provably safe, or it creates spurious conflict copies.

### FM-4 — Counter rollback / state reset makes old versions look new

If a device's state is wiped and its counter restarts at a low value, its new
edits carry counters *lower* than versions already propagated, so remote replicas
treat genuinely-new edits as stale (dominated) and silently discard them. This is
exactly what Syncthing's `max(value+1, now)` floor (§4.4) defends against. Naive
"start at 1" implementations are vulnerable.

### FM-5 — Using wall-clock instead of the VV for ordering

The recurring temptation is to compare `mtime` to decide "who wins." Wall clocks
on two laptops skew, NTP steps backwards, and "last write wins" by timestamp
silently drops data — a classic distributed-systems trap
([The trouble with timestamps, K. Kingsbury / aphyr](https://aphyr.com/posts/299-the-trouble-with-timestamps),
accessed 2026-06-28). `SR-4` forbids it: VV decides ordering; `mtime` is **only**
the tiebreaker *after* the VV says "concurrent."

### FM-6 — Concurrency is detected, not resolved

A version vector tells you **that** two edits conflict; it does **not** tell you
which to keep. Resolution needs a separate, **deterministic** policy so both peers
independently pick the same winner. Syncthing: "The file with the older
modification time will be marked as the conflicting file ... If the modification
times are equal, the file originating from the device which has the larger value
of the first 63 bits for its device ID will be marked as the conflicting file"
([Understanding Synchronization](https://docs.syncthing.net/users/syncing.html),
accessed 2026-06-28). That is `SR-7`. A VV without a deterministic tiebreaker leads
peers to disagree on the winner → divergence.

---

## 6. Complexity

Let `d` = number of distinct devices that have ever touched a given file
(= length of that file's `Counters` slice). For Merkle Sync's LAN target `d` is
small and bounded (the README scopes it to Mac↔Windows, i.e. ~2; a handful at
most), which is what makes classic per-device version vectors the right tool here
and FM-2 a non-issue in the common case.

| Operation | Cost | Notes |
|---|---|---|
| Space per leaf | **O(d)** entries × 16 bytes (8B ShortID + 8B uint64) | stored in every `FileInfo`; included in the structural hash |
| `Counter(id)` lookup | O(d) linear scan (sorted) | could be O(log d) binary search; d tiny ⇒ irrelevant |
| `Compare(a,b)` | **O(da + db)** single merge pass | the §4.3 lock-step walk; terminates early on `Concurrent*` |
| `Merge(a,b)` | **O(da + db)** | pointwise max, output stays sorted |
| `Update(self)` | **O(d)** (sorted insert/append) | amortised; bumps exactly one entry |

Comparison/merge are linear in the *number of devices*, **not** the number of
files or bytes — VV cost is independent of file size. The dominating cost in the
engine remains hashing file content, not VV math. The only superlinear *risk* is
unbounded `d` (FM-1/FM-2), addressed by §8 A2.

---

## 7. Relationship to the Merkle tree (why both are needed)

The Merkle tree answers **"do these two files differ?"** in O(log n)
(`merkle-tree` finding; `SR-5`). The version vector answers the two questions the
hash cannot: **"which side is newer?"** and **"did they diverge (conflict) or is
one just behind?"** The leaf-shape decision pins that the **structural (tree) hash
commits to the version vector** (and `content_hash`, `mode`, `deleted`), but
**excludes** raw `mtime`/`size` (`merkle-leaf-shape.md:100-117`). Consequence:
"converged ⇔ identical root hash" holds even for files whose bytes match but whose
*history* differed, and for tombstones whose bytes are absent. So the VV is not a
side-table — it is **part of the hashed identity of every leaf**, which is why its
serialization must be byte-deterministic and identical cross-platform (§8 A3,
`SR-13`).

---

## 8. How it maps to Merkle Sync — ADOPT vs ADAPT

Placement is already fixed by `docs/audit/plan/structure.md:54`:
`internal/protocol/versionvector.go` exposes `VersionVector` with `Bump`,
`Compare` (dominates / dominated / concurrent), `Merge`; `DeviceID` lives in the
same package (`deviceid.go`). `FileInfo.version_vector` (in `internal/merkle`)
carries it (`merkle-leaf-shape.md`).

### ADOPT (lift Syncthing's design more-or-less directly)

- **A-adopt-1 — Sorted-slice `[]Counter{ID, Value}` representation.** Beats a Go
  `map[DeviceID]uint64` for our use: deterministic iteration/serialization (needed
  for the structural hash, `SR-13`), cache-friendly, O(d) merge-join compare. d is
  tiny so no need for map O(1).
- **A-adopt-2 — The three-valued `Compare` (§4.3) and `Ordering` enum.** It is the
  exact dominance partial order §2.2 requires, battle-tested, and maps 1:1 to the
  engine's branches: `Greater`→apply ours/skip theirs, `Lesser`→apply theirs,
  `Concurrent*`→conflict path (`SR-7`). Keep the `Concurrent`-direction hint for a
  stable winner pick.
- **A-adopt-3 — Pointwise-max `Merge` (§4.7)** as the sync-time combine, and
  **`Update`/`Bump` increments only the local device's counter** — the literal
  encoding of `SR-6` ("bump its own counter on a confirmed local change") and
  `SR-4` ("VV is the ordering source of truth").
- **A-adopt-4 — `DeviceID` → 64-bit `ShortID` as the counter key.** Our `DeviceID`
  is already SHA-256 of the cert DER
  (`docs/audit/decisions/phase0/transport-security-tofu-vs-plaintext.md`,
  `structure.md:56`); take the first 64 bits as the VV key (8B/entry). Collision
  probability at LAN scale (≤ tens of devices) is negligible; document it.
- **A-adopt-5 — Tombstones carry a bumped VV** so a delete *dominates* a stale
  peer's pre-delete VV (the absent-counter-as-0 rule in §4.3) — directly satisfies
  `SR-9`/`SR-10`. Adopt as-is.

### ADAPT (deliberately diverge from Syncthing)

- **A1 — `mtime`/`device-ID` tiebreaker stays external to the VV.** The VV returns
  only `Concurrent`; resolution (older-mtime-loses, then larger-DeviceID-loses) is
  `internal/reconcile/conflict.go` per `SR-7`. Keep causality and policy separate
  (FM-6). *Adopt the policy, keep it out of the VV type.*
- **A2 — Build counter cleanup in from day one (fixes FM-1).** Add a
  `VersionVector.DropCounter(id)` (or `Compact(liveDevices)`); on device removal,
  sweep stored `FileInfo`s and strip the dead device's counter. For a 2-device LAN
  tool this is cheap insurance, and FM-1 shows that *not* doing it is the marquee
  long-lived data-loss/resurrection bug. **Pruning safety (FM-3) must be respected:
  only drop a counter once it is provably not needed** (e.g. device truly removed
  and both live peers acknowledged) — never time/size-based blind pruning that two
  peers could do unequally. *This is a Phase 2 `protocol-researcher` decision —
  see §9.*
- **A3 — Pin a byte-deterministic VV serialization for the structural hash.**
  Sort `Counters` by `ID` ascending; encode each as `uint64 ID` then `uint64
  Value`, **big-endian**, fixed width; length-prefix the slice. Must be identical
  on Mac and Windows (`SR-13`, `GR-12`, leaf-shape Consequences). Syncthing's
  protobuf is *not* guaranteed canonical for hashing across versions, so we define
  our own fixed encoding in `internal/merkle/codec.go`. *Exact byte layout is the
  Phase 2 `merkle-researcher` decision — see §9.*
- **A4 — Make `Bump`/`Merge`/`DropCounter` copy-on-write (fixes the §4.7 aliasing
  footgun).** Return a fresh `VersionVector` with its own backing array; never
  mutate the receiver's shared slice. Required by `GR-5` (immutable snapshots
  under the RWMutex) and the leaf-shape "FileInfo is an immutable value snapshot"
  rule. Verify with `go test -race`.
- **A5 — Decide on the `max(value+1, now)` wall-clock floor (§4.4).** *Recommend
  adopting* it: it gives strict per-device monotonicity and survives a state reset
  without counter rollback (defends FM-4), while **not** making wall-clock the
  ordering authority (ordering is still `Compare` dominance; `mtime` is still the
  only tiebreaker — `SR-4` is respected). Caveat to document: counters become
  large/time-correlated and a *badly* skewed local clock could inflate a counter;
  acceptable because inflation only ever makes our own future edits win, never
  silently discards a peer's edit (a conflict still surfaces as `Concurrent`).
  *Flag as a Phase 2 confirmation point.*

### Deliberately NOT adopted (scope)

- **Dotted Version Vectors / DVVSet are out of scope.** DVV solves *client-server*
  sibling explosion — many anonymous clients writing one key through server
  replicas, where keying by client ID explodes (FM-2)
  ([Dotted-Version-Vectors](https://github.com/ricardobcl/Dotted-Version-Vectors);
  papers: *Dotted Version Vectors*, Preguiça/Baquero/Gonçalves et al. 2010–2012;
  *Scalable and Accurate Causality Tracking for Eventually Consistent Stores*
  (DVVSet), DAIS 2015 — accessed 2026-06-28). Merkle Sync is **peer-to-peer**: each
  peer is itself a replica/server, the VV is keyed by **device (server) IDs**, and
  the device count is small and bounded. Classic per-device version vectors are the
  correct, simpler model; DVV would be unjustified complexity. Documented here so
  Phase 3 critics don't re-litigate it.

---

## 9. Open decisions deferred to Phase 2 (per leaf-shape Consequences)

This finding is *input*, not a binding decision. Two consequential VV choices are
explicitly owned by Phase 2 (`merkle-leaf-shape.md:138-147`); the contract's
"log a decision before acting" applies to **them**, when they act:

1. **Exact canonical VV serialization for hashing** (field order, integer width,
   endianness, path/length prefixing) → **Phase 2 `merkle-researcher`**, written to
   `docs/audit/decisions/ws1/` (or `phase2/`). Recommendation: §8 A3.
2. **VV pruning / compaction + device-removal counter cleanup + retention** →
   **Phase 2 `protocol-researcher`**, written to `docs/audit/decisions/phase2/`.
   Recommendation: §8 A2 (safe, ack-gated `DropCounter`; never blind time/size
   pruning — FM-3). Ties to the tombstone-retention sub-decision (`SR-10`).

No Phase-1 decision file is written here because making the binding VV-encoding /
pruning choice now would pre-empt those assigned Phase-2 owners and risk a
conflicting decision (the leaf-shape decision already routed them forward).

---

## 10. Cross-references

- `docs/audit/rules/sync-rules.md` — **SR-4** (VV is ordering truth, not mtime),
  **SR-6** (bump own counter only on confirmed local change), **SR-7** (concurrent
  ⇒ conflict copy + tiebreaker), **SR-9**/**SR-10** (tombstone VV dominance ⇒ no
  resurrection), **SR-13** (deterministic cross-platform serialization).
- `docs/audit/rules/go-rules.md` — **GR-5** (immutable snapshots under RWMutex →
  copy-on-write, §8 A4), **GR-7** (`encoding/binary` not gob on the wire),
  **GR-12** (forward-slash canonical / determinism).
- `docs/audit/decisions/phase0/merkle-leaf-shape.md` — the `FileInfo` leaf that
  carries the VV; what the structural hash includes/excludes; the two deferred
  sub-decisions in §9.
- `docs/audit/decisions/phase0/transport-security-tofu-vs-plaintext.md` —
  `DeviceID` = SHA-256(cert DER) → `ShortID` key (§8 A-adopt-4).
- `docs/audit/plan/structure.md:54,56,75-76` — `internal/protocol/versionvector.go`
  + `deviceid.go`; `FileInfo` field placement.
- Sibling literature findings to be cross-read by the synthesizer:
  `syncthing-bep` (FileInfo/BEP detail), `merkle-tree` (the diff property the VV
  complements).

---

## 11. Sources (all accessed 2026-06-28)

**Real source / specs (primary):**
- Syncthing `lib/protocol/vector.go` (verbatim impl, §4):
  https://raw.githubusercontent.com/syncthing/syncthing/main/lib/protocol/vector.go
- Syncthing Block Exchange Protocol v1 (wire layout, §4.5–4.6):
  https://docs.syncthing.net/specs/bep-v1.html
- Syncthing — Understanding Device IDs:
  https://docs.syncthing.net/dev/device-ids.html
- Syncthing — Understanding Synchronization (conflict policy, FM-6):
  https://docs.syncthing.net/users/syncing.html
- Syncthing Issue #10590 — ghost version-vector counters (FM-1):
  https://github.com/syncthing/syncthing/issues/10590
- Syncthing Issue #6372 — VV storage growth (FM-2):
  https://github.com/syncthing/syncthing/issues/6372

**Definitions / theory:**
- Version vector — Wikipedia (definition, rules, comparison, Parker 1983 origin):
  https://en.wikipedia.org/wiki/Version_vector
- Vector clock — Wikipedia (VC rules, comparison, Fidge/Mattern 1988 origin):
  https://en.wikipedia.org/wiki/Vector_clock
- Clocks and Causality — exhypothesi (Lamport vs VC vs VV update-rule
  distinction, §3): https://www.exhypothesi.com/clocks-and-causality/

**Growth / pruning research & practice:**
- Dotted Version Vectors — R. Gonçalves et al. (FM-2, out-of-scope rationale §8):
  https://github.com/ricardobcl/Dotted-Version-Vectors
- Logical Clocks in Real Life — K. Nath, Medium (growth/pruning, 2.86 MB example):
  https://medium.com/geekculture/all-things-clock-time-and-order-in-distributed-systems-logical-clocks-in-real-life-2-ad99aa64753
- Version Vector(III) — P. Pandey, LinkedIn (dominance restated; unequal pruning
  FM-3): https://www.linkedin.com/pulse/version-vectoriii-pratik-pandey
- The trouble with timestamps — K. Kingsbury (aphyr) (FM-5, why not wall-clock):
  https://aphyr.com/posts/299-the-trouble-with-timestamps

**Foundational papers (cited via the Wikipedia articles above):**
- L. Lamport, *Time, Clocks, and the Ordering of Events in a Distributed System*,
  CACM 1978.
- D.S. Parker et al., *Detection of Mutual Inconsistency in Distributed Systems*,
  IEEE TSE 1983.
- C. Fidge / F. Mattern, vector clocks, 1988.
