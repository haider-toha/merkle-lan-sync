# Literature finding — Merkle hash trees + the git tree-object model

- **Source slug:** `merkle-tree`
- **Phase / role:** Phase 1 — literature-mapper
- **Reads-first honoured:** `docs/audit/rules/{sync-rules,go-rules,crossplatform-rules}.md`,
  `docs/audit/decisions/phase0/merkle-leaf-shape.md`,
  `docs/audit/decisions/phase0/framing-format.md`,
  `docs/audit/plan/structure.md`, `.claude/skills/merkle-sync/SKILL.md`,
  `plan/README.md`, `plan/agent_roster.md`
- **Status:** complete. Recommendations feed Phase 2 `merkle-researcher` (exact
  canonical serialization, leaf-vs-node domain separation) and the design-critic
  `tree-critic`. One concrete, evidence-backed change to the current SKILL spec is
  flagged in §6/§7 (add RFC-6962-style domain separation to the structural hash).
- **Date / access date for all URLs:** 2026-06-28
- **Evidence policy:** every claim is grounded in a cited URL (access date
  2026-06-28) or a `file:line` in this repo. Where the literature and the existing
  Phase 0 spec disagree or under-specify, that is called out explicitly rather than
  smoothed over.

---

## 1. TL;DR

A **Merkle tree** (hash tree) labels every leaf with the hash of a data block and
every internal node with the hash of (the concatenation of) its children's
labels; the single **root hash** therefore commits to the entire dataset, and any
change to any leaf propagates up to change the root. Two structures share this
idea but differ in shape:

- **Merkle's balanced binary tree (1979 / CRYPTO '87)** — used by Certificate
  Transparency (RFC 6962 / 9162), Bitcoin, Cassandra/Dynamo anti-entropy. Built
  over an **ordered list** of `n` leaves; depth `⌈log2 n⌉`; the classic
  "membership proof / diff in `O(log n)`" results are about *this* shape.
- **Git's n-ary directory tree** — the tree mirrors the **filesystem hierarchy**:
  a `blob` per file, a `tree` per directory whose hash is computed over its
  **name-sorted child entries** `(mode, name, child-hash)`. This is the model
  Merkle Sync actually wants, because the sync unit is "a file at a path in a
  directory tree," and the diff naturally becomes "recurse only into directories
  whose hash changed."

The two load-bearing properties Merkle Sync inherits:

1. **Folder hash derives from sorted child hashes** ⇒ identical content ⇒
   identical hash, deterministically, on both OSes (the basis of `SR-5`
   convergence and the SKILL §2 tree construction).
2. **Prune-equal-subtrees diff** ⇒ comparing two trees costs work proportional to
   the *differences*, not the total size: a one-byte change touches exactly that
   leaf's root-to-leaf path and nothing else (the `SR-5` acceptance test, and the
   anti-entropy mechanism of Dynamo/Cassandra).

The two load-bearing **failure modes** to design against:

- **Second-preimage / leaf-vs-internal confusion** — if a leaf hash and an
  internal-node hash are computed from the same domain, an internal node can be
  passed off as a leaf (or vice-versa), forging a different tree with the same
  root. Fix = **domain separation** (RFC 6962: prefix `0x00` to leaves, `0x01` to
  internal nodes). The current Merkle Sync structural-hash spec does **not** yet
  do this; §6/§7 recommends adding it.
- **Non-deterministic / ambiguous serialization** — unsorted children, variable
  integer widths, OS path separators, or the odd-node duplication bug
  (CVE-2012-2459) all let "the same data" produce different roots (breaks `SR-5`)
  or "different data" produce the same root (silent divergence). Fix = one fixed,
  byte-deterministic, length-prefixed, sorted, domain-separated serialization,
  identical on Mac and Windows.

---

## 2. Core algorithm — two lineages of one idea

### 2.1 Merkle's original "tree authentication" (the balanced binary tree)

Ralph Merkle introduced hash/authentication trees in his 1979 Stanford PhD thesis
*Secrecy, Authentication, and Public Key Systems* and the 1980 IEEE S&P paper
*Protocols for Public Key Cryptosystems*; the detailed "tree authentication"
construction and its use in a certified one-time-signature scheme appears in
*A Certified Digital Signature*, presented at **CRYPTO '87** (the "Merkle (1987)"
of this task; published in *Advances in Cryptology — CRYPTO* proceedings, LNCS
vol. 435, pp. 218–238 — the venue/date is variously cited as 1987/1989/1990).
US patent **4,309,569** ("Method of providing digital signatures") was filed 1979
and granted 1982. ([Merkle signature scheme, Wikipedia](https://en.wikipedia.org/wiki/Merkle_signature_scheme);
[ResearchGate: *A Certified Digital Signature*](https://www.researchgate.net/publication/221355342_A_Certified_Digital_Signature);
[SciRP reference record](https://www.scirp.org/reference/referencespapers?referenceid=2082402),
all accessed 2026-06-28.)

**The construction.** Over a vector of data items `Y1..Yn`, label each tree node
with an index pair `N(i, j)` covering the leaf range `[i, j]`. With a one-way hash
`H` (Merkle wrote `F`) and `||` for concatenation
([SJSU, *Improving Smart Grid Security Using Merkle Trees*, §Merkle tree, p. ~8](https://scholarworks.sjsu.edu/cgi/viewcontent.cgi?article=1419&context=etd_projects);
[Wikipedia: *Merkle tree*](https://en.wikipedia.org/wiki/Merkle_tree),
both accessed 2026-06-28):

```
N(i, j) =  H( D_i )                              if i == j      (leaf)
N(i, j) =  H( N(i, k) || N(k+1, j) )             if i <  j      (internal)
           where k = floor((i + j - 1) / 2)      (split point)

root  R = N(1, n)
```

RFC 9162 (Certificate Transparency v2.0, which carries forward RFC 6962's
definition) gives the same recurrence in implementation-ready form with an
explicit split at the largest power of two and **domain separation**
([RFC 9162 §2.1.1](https://datatracker.ietf.org/doc/html/rfc9162), accessed 2026-06-28):

```
MTH({})        = HASH()                                   # empty tree
MTH({d0})      = HASH( 0x00 || d0 )                       # leaf  (one element)
MTH(D[0:n])    = HASH( 0x01 || MTH(D[0:k]) || MTH(D[k:n]) )   # internal, n > 1
                 where k = largest power of two strictly less than n
```

The RFC states plainly: *"the hash calculations for leaves and nodes differ; this
domain separation is required to give second preimage resistance"* (RFC 9162
§2.1.1). The `k = largest-power-of-two < n` split makes the **left subtree a
perfect binary tree**, which is what gives clean `O(log n)` proofs even when `n`
is not a power of two.

**Membership / inclusion proof (audit path).** To prove `Y_i` is in the tree under
root `R`, supply the **sibling hash at each level** from the leaf to the root
(`⌈log2 n⌉` hashes). The verifier recomputes upward and checks it equals `R`. Cost
is *"proportional to the logarithm of the number of leaf nodes"*
([Wikipedia: *Merkle tree*](https://en.wikipedia.org/wiki/Merkle_tree), accessed
2026-06-28; [ordep.dev, *Diving into Merkle Trees*](https://ordep.dev/posts/diving-into-merkle-trees),
accessed 2026-06-28). (CT additionally defines a **consistency proof** that an
append-only log of size `m` is a prefix of the log of size `n`; this append-only
property is CT-specific and **not** needed by a mutable two-way file sync, so it
is noted and set aside.)

### 2.2 Git's directory hash tree (the model Merkle Sync resembles)

Git stores content-addressed objects. The two relevant types
([Pro Git, *Git Internals — Git Objects*](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects);
[dev.to, *Git Internals Part 1: the git object model*](https://dev.to/calebsander/git-internals-part-1-the-git-object-model-474m);
[dulwich, *Git File format*](https://www.samba.org/~jelmer/dulwich/docs/tutorial/file-format.html),
all accessed 2026-06-28):

- **blob** = a file's contents (a leaf). Object id = `SHA1("blob " + len + "\0" + bytes)`.
- **tree** = a directory (an internal node). It lists one entry per direct child;
  each entry is `(mode, name, child-object-id)`. The tree's own id is
  `SHA1("tree " + len + "\0" + concatenated-entries)`.

Because a directory's hash is computed **over its children's object ids**, and
each child id was itself computed over *its* contents, the tree's root id commits
to the entire subtree recursively — *"a tree's hash reflects its complete
directory structure and file contents recursively"* and *"if two directories have
identical contents, they produce identical hashes and share a single tree
object"* (dev.to, accessed 2026-06-28). That dedup-by-identical-subtree property
is exactly the "prune equal subtrees" diff in storage form.

Git's hash input is the **uncompressed** `"<type> <size>\0<payload>"`; the object
is then zlib-deflated only for on-disk storage (dulwich, accessed 2026-06-28). Git
historically uses **SHA-1** (a hardened, collision-detecting "sha1dc" variant) and
has an in-progress migration to SHA-256.

---

## 3. EXACT data structures / formulas / field layouts

### 3.1 Git tree object — verified byte layout

Header, then a packed sequence of entries (no separators between entries beyond
the per-entry `NUL`); entries are **name-sorted**:

```
tree <payload-size-in-ascii-decimal>\0          # object header (hashed, not stored compressed)
<entry><entry>...<entry>                          # payload

entry := <mode-ascii-octal> 0x20 <name-bytes> 0x00 <20-byte-raw-binary-sha1>
         └─ e.g. "100644" ─┘ space  └ filename ┘ NUL  └─ NOT 40-char hex ─┘
```

Worked hexdump of a real tree object
([dev.to](https://dev.to/calebsander/git-internals-part-1-the-git-object-model-474m),
accessed 2026-06-28):

```
00000000: 74 72 65 65 20 31 34 34 00 31 30 30 36 34 34 20   tree 144.100644
00000010: 2e 67 69 74 69 67 6e 6f 72 65 00 ea 8c 4b f7 f3   .gitignore...K..
          └ "tree 144\0" header ┘└ "100644 " ┘└".gitignore"┘NUL└ raw sha1…
```

**Field facts that matter for a re-implementation (each a real foot-gun):**

| field | exact form | gotcha |
|---|---|---|
| header size | ASCII **decimal** byte count of the payload | counts payload only, not the header |
| `mode` | ASCII **octal**, *no leading zero* in the raw object | a directory is stored as `40000`, **not** `040000` — `git cat-file -p` *displays* `040000` but the hashed bytes are `40000` ([w3tutorials](https://www.w3tutorials.net/blog/how-is-the-git-hash-calculated/); dev.to, accessed 2026-06-28) |
| child hash | **20 raw bytes**, big-endian binary | **not** the 40-char hex you see in `git cat-file`; serializing hex would change the tree hash (dulwich, accessed 2026-06-28) |
| ordering | entries **sorted by name**, *"otherwise the representation of a tree would not be unique"* (dev.to, accessed 2026-06-28) | the sort is **not** a plain `strcmp` — see §3.2 |

Git file modes (the only valid ones): `100644` regular file, `100755` executable
file, `120000` symlink, `40000` directory (subtree), `160000` gitlink (submodule
commit) ([Pro Git, *Git Objects*](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects),
accessed 2026-06-28).

### 3.2 Git's child-ordering rule (the subtle, convergence-critical part)

Git sorts entries by name, **but a directory entry is compared as if its name had
a trailing `/`**. The canonical statement, from the git source / mailing list:
*"Git's tree-order sorts sub-trees as if their paths have `/` at the end ... we do
not care about interior slashes at all, only about the final character"*; the
comparison is `base_name_compare(name, len, mode, ...)`, which appends `'/'` to a
name when `S_ISDIR(mode)` before the final-character compare
([git mailing-list patch, *intersect_paths: respect mode in git's tree-sort*](https://git.vger.kernel.narkive.com/JhSl9XOQ/patch-intersect-paths-respect-mode-in-s-tree-sort);
[git/git `tree-diff.c`](https://github.com/git/git/blob/master/tree-diff.c),
both accessed 2026-06-28).

Concrete consequence (the example that breaks naive clones): given a file
`lib.txt` and a directory `lib`, the directory sorts as `lib/`; comparing
`lib.txt` vs `lib/` diverges at byte 4 — `'.'` (`0x2e`) `<` `'/'` (`0x2f`) — so
**`lib.txt` sorts *before* the `lib` directory**, whereas a plain `strcmp` of
`lib.txt` vs `lib` would put `lib` first. Two implementations that disagree here
compute **different root hashes for identical content** → they would never
converge (`SR-5`). Merkle Sync does **not** have to copy git's exact rule, but it
**must** pick one byte-deterministic rule and apply it identically on Mac and
Windows (see §6, adapt-A4).

### 3.3 Merkle Sync's current structural-hash spec (for comparison)

From `.claude/skills/merkle-sync/SKILL.md:48-83` and
`docs/audit/decisions/phase0/merkle-leaf-shape.md`:

```
leaf  structural hash = SHA-256( canonical(name, content_hash, mode, deleted, version) )
dir   node       hash = SHA-256( name-sorted list of (childName, childStructuralHash) )
root             hash = top directory node's hash
```

- `content_hash` is the **pure file bytes** SHA-256 (doubles as the
  transfer/dedup key) — independent of metadata. Directory nodes derive their hash
  from children (the git/Merkle property).
- **Included** in the structural hash: child `name`, `content_hash`, `mode`,
  `deleted` flag, `version` vector. **Excluded**: raw `mtime` and `size` (hashing
  `mtime` would manufacture spurious whole-tree diffs across machines; `size` is
  redundant with `content_hash`) — `merkle-leaf-shape.md` Decision §2.
- Serialization constraints already fixed: forward-slash NFC paths (`XP-1`,
  `XP-2`, `GR-12`), fixed integer widths, big-endian, length-prefixed names,
  VV entries sorted by `DeviceID` (SKILL §2). Exact byte layout is handed to
  Phase 2 `merkle-researcher`.

**Gap vs the literature (flagged for Phase 2):** this spec has **no leaf-vs-node
domain separation** (no `0x00`/`0x01` tag distinguishing a file leaf's
serialization from a directory node's). See §4.1 and §6 adapt-A2.

---

## 4. Failure modes (each with citation + the Merkle-Sync-relevant fix)

### 4.1 Second-preimage attack: leaf/internal-node confusion

**What it is:** in a naive tree where `leafHash = H(data)` and
`nodeHash = H(left || right)` use the *same* `H` over the *same* domain, an
adversary can present an **internal node's hash as if it were a leaf** (or splice
subtrees), producing a *different* tree that hashes to the *same* root. *"There is
no information about the depth of a tree or leaf vs. branch nodes stored"* in a
naive implementation, so *"an adversary claims that an internal node hash is a
leaf"* ([RFC 6962 second-preimage explanation, er4hn](https://er4hn.info/blog/2022.10.08-second_preimage_on_merkle_tree/);
[Adevar Labs, *Preventing Second Preimage Attacks in Merkle Trees*](https://www.adevarlabs.com/blog/preventing-second-preimage-attacks-in-merkle-trees-a-complete-guide);
[Wikipedia: *Merkle tree*](https://en.wikipedia.org/wiki/Merkle_tree), all
accessed 2026-06-28).

**Fix — domain separation (RFC 6962 / 9162):** prefix a distinct byte before
hashing each kind: `leafHash = H(0x00 || data)`, `nodeHash = H(0x01 || left ||
right)`. Then *"the hash of a leaf node can never equal the hash of an intermediate
node"* (Adevar Labs; RFC 9162 §2.1.1, accessed 2026-06-28). **Git gets a coarser
version of this for free**: its hash input begins with the object **type tag**
(`"blob "` vs `"tree "`), and each tree entry records the child's `mode`
(`100644` file vs `40000` dir), so a blob's id cannot be substituted for a
subtree id.

**Relevance to Merkle Sync:** the current structural-hash spec (§3.3) relies only
on the differing *field layout* of a leaf vs a directory node to keep them
distinct — there is no explicit tag. That is fragile: a crafted `FileInfo` whose
serialization happens to match a directory node's child-list byte-for-byte would
collide. The cheap, standard, proven fix is to **adopt RFC-6962-style domain
separation** (a leading type byte) in the canonical serialization. This is the one
concrete spec change this finding recommends (§6 adapt-A2).

### 4.2 Odd-node duplication — CVE-2012-2459 (Bitcoin)

**What it is:** if a level has an **odd** number of nodes and the implementation
**duplicates the last hash** to pair it, then *"certain sequences of transactions
lead to the same merkle root"* — two **distinct** leaf lists yield an **identical
root**. In Bitcoin this was a DoS (block-validation outage) tracked as
**CVE-2012-2459** ([Bitcoin Optech, *Merkle tree vulnerabilities*](https://bitcoinops.org/en/topics/merkle-tree-vulnerabilities/);
[bitcoin/bitcoin commit 01c2807, *Add warning about the merkle-tree algorithm
duplicate txid flaw*](https://github.com/bitcoin/bitcoin/commit/01c2807);
[BIP 98, *Fast Merkle Trees*](https://bips.dev/98/), all accessed 2026-06-28).

**Relevance to Merkle Sync:** the directory-hierarchy tree (§2.2/§3.3) is **n-ary
and does not pad to power-of-two**, so it is **not directly exposed** to the
odd-node duplication bug. The lesson still binds the design: **never duplicate a
node to fill a level, and never let two different child sets serialize the same**.
If a *binary* sub-tree over file *chunks* is ever introduced (e.g. the deferred
content-defined-chunking work), it MUST use the RFC-9162 split
(`k = largest power of two < n`), which never duplicates, rather than the Bitcoin
duplicate-last scheme.

### 4.3 Non-deterministic child ordering / ambiguous serialization

**What it is:** if children are hashed in directory-read order (or any
non-canonical order), the same directory hashes differently on two machines, and
roots never converge. Git's own guard: entries are sorted *"otherwise the
representation of a tree would not be unique"* (dev.to, accessed 2026-06-28). The
git trailing-`/` subtlety (§3.2) is a real instance where two "reasonable" sort
rules disagree. Ambiguity also arises from variable-width integers or unescaped,
non-length-prefixed names (where `name="a", hash=Hb` could collide with
`name="a"+Hb, hash=""`).

**Relevance to Merkle Sync (`SR-5`, `SR-13`):** this is the highest-probability
*convergence* bug for this project, made worse by the cross-platform requirement
(`XP-1`/`XP-2`: NFD-vs-NFC, `/` vs `\`). Fixes already pinned: forward-slash NFC
canonical paths, fixed integer widths + big-endian, length-prefixed names, sorted
VV entries (SKILL §2). What remains for Phase 2: **fix one total order over
canonical child names and one byte-exact serialization grammar**, with a
table-driven test that the same content hashes identically across a Mac→wire→
Windows→wire→Mac round-trip.

### 4.4 Hashing volatile metadata → spurious whole-tree diffs

**What it is:** including `mtime` (or other machine-local, non-content metadata) in
the structural hash makes every node above an untouched-but-restat'd file change,
so peers that hold identical content compute different roots and "diff forever."

**Relevance:** already correctly handled — `merkle-leaf-shape.md` **excludes** raw
`mtime` and `size` from the structural hash and keeps `mtime` only as a conflict
tiebreaker (`SR-4`, `SR-7`). This finding endorses that choice; it is the right
specialization of the generic Merkle property "hash only what defines identity."

### 4.5 The "`O(log n)`" label is only true for *balanced* trees

**What it is:** the textbook `O(log n)` membership/diff cost (RFC 6962, Cassandra,
Dynamo) assumes a **balanced binary** tree of depth `log2 n`. A **directory
hierarchy is unbalanced**: its depth is the filesystem nesting depth `D`, which is
unrelated to `log(N)` (a flat folder of 100k files has `N=100000` but `D=1`;
a deeply nested repo can have large `D` with few files). Quoting `O(log n)` for the
directory tree is an over-claim.

**Relevance:** state the **honest** complexity (see §5) in the plan and tests. The
real, defensible property is **prune-equal-subtrees** (cost ∝ differences, not
total size), not a strict `O(log n)`. The `SKILL.md:88` approximation
"`~O(d + k)`" is optimistic — it omits the per-visited-directory child enumeration
(branching factor `b`); the tighter bound is in §5. The `SR-5` acceptance ("one
byte changed flips exactly that leaf's branch and the root, nothing else") is the
right way to *test* the property and should stand as written.

### 4.6 Anti-entropy churn: key/range changes force tree recomputation

**What it is:** in Dynamo, *"the disadvantage with this scheme is that many key
ranges change when a node joins or leaves the system, thereby requiring the
tree(s) to be recalculated"* ([Dynamo: *Amazon's Highly Available Key-value
Store*, §4.7](https://www.cs.cornell.edu/courses/cs5414/2017fa/papers/dynamo.pdf);
[All Things Distributed, *Amazon's Dynamo*](https://www.allthingsdistributed.com/2007/10/amazons_dynamo.html),
accessed 2026-06-28). A Merkle tree is only cheap to diff if it is cheap to keep
up to date.

**Relevance to Merkle Sync:** the analogue of "key ranges change" is a
**directory rename/move**, which re-parents a whole subtree and changes every
ancestor hash. With a path-keyed tree, a top-level folder rename looks like a mass
delete+create. Two mitigations to weigh in Phase 2: (a) the tree is naturally
*incrementally* recomputable — only ancestors of changed leaves need re-hashing
(the same prune property, applied to *rebuild* not just *diff*); (b) rename
detection (hash-match heuristic vs treat-as-delete+create) is already an open
roster decision (`plan/README.md` "Rename detection"). This finding flags that the
rebuild cost, not just the diff cost, should be in the WS-1 acceptance criteria.

### 4.7 (Git-specific, informational) SHA-1 collisions

Git's default hash is SHA-1; SHA-1 is collision-broken (SHATTERED, 2017), which is
why git ships the collision-detecting "sha1dc" variant and is migrating to
SHA-256. **Not a Merkle Sync risk** — the leaf-shape decision already mandates
**SHA-256** for both `content_hash` and the structural hash. Recorded only so the
"we copy git" framing does not silently import git's hash choice.

---

## 5. Complexity — stated honestly for each shape

Let `n` = number of leaves; `d` = number of differing leaves between two trees;
`D` = tree depth; `b` = average branching factor (children per directory node).

| operation | balanced binary tree (RFC 6962 / Cassandra) | git / Merkle-Sync directory tree |
|---|---|---|
| build full tree | `O(n)` node hashes (+ leaf hashing of all bytes) | `O(#files)` content hashes over all bytes + `O(#dirs)` node hashes |
| **incremental rebuild** after `d` leaf changes | `O(d · log n)` re-hashes | `O(Σ depths of changed paths)` ≈ `O(d · D)` node re-hashes (shared prefixes reduce this) |
| inclusion / membership proof | `⌈log2 n⌉` sibling hashes | path of `O(D)` (mode, name, sibling-hash) entries up to root |
| **diff two trees** (prune equal subtrees) | visit `O(d · log n)` nodes | visit nodes on the union of root→changed-leaf branches: `O(d · D)` node-hash compares **plus** `O(b)` child enumeration per visited directory; `= O(1)` when roots match |
| storage | `O(n)` internal nodes | `O(#files + #dirs)` objects; identical subtrees dedup to one object |

The single property that survives in **both** columns and that Merkle Sync depends
on: **if two subtree hashes are equal, the entire subtree is skipped** — so a diff
costs work proportional to the *differences*, never `O(n)` when little changed
(the Dynamo/Cassandra anti-entropy win: *"compare children hashes recursively
until you reach mismatched leaves ... sync only the data for mismatched leaves,
not the entire dataset"* — [deepengineering, *Merkle Trees and Anti-Entropy*](https://deepengineering.net/p/merkle-trees-and-anti-entropy-concepts);
[Cassandra wiki, *AntiEntropy*](https://cwiki.apache.org/confluence/display/CASSANDRA2/AntiEntropy);
[DataStax, *Manual repair: anti-entropy repair*](https://docs.datastax.com/en/cassandra-oss/3.x/cassandra/operations/opsRepairNodesManualRepair.html),
all accessed 2026-06-28). Cassandra's compact repair tree is a perfect binary tree
of depth 15 (`2^15 = 32768` leaves) — a concrete sizing data point.

---

## 6. How it maps to Merkle Sync — ADOPT vs ADAPT

`internal/merkle/{node.go,tree.go,differ.go,codec.go}` (`docs/audit/plan/structure.md`)
are the consumers. The Phase 0 leaf-shape decision already settled the *leaf
metadata*; this finding addresses the *tree hashing and diff*.

### ADOPT (take directly from the literature)

- **A1 — Git's "folder hash = hash of name-sorted child entries."** It is exactly
  the requested "how a folder hash derives from sorted child hashes," it gives the
  recursive root-commitment and the identical-subtree dedup, and it matches the
  filesystem's natural shape. (`tree.go`, SKILL §2.) Evidence: §2.2, §3.1.
- **A3 — Prune-equal-subtrees diff.** Compare node hashes top-down; equal ⇒ skip;
  differ ⇒ recurse only into differing children. This is the SKILL §2 `diff()` and
  the Dynamo/Cassandra anti-entropy algorithm. (`differ.go`.) Evidence: §2.1
  (inclusion-proof intuition), §5.
- **A5 — Separate "content hash" from "structural hash."** Git separates blob id
  (content) from tree id (structure); Merkle Sync already mirrors this
  (`content_hash` = pure bytes, doubles as transfer/dedup key; structural hash =
  identity for convergence). Keep it. (`fileinfo.go` / `node.go`.) Evidence: §3.3.

### ADAPT (take the idea, change the specifics for this project)

- **A2 — ADD leaf-vs-node domain separation (the one spec change).** Adopt RFC
  6962's mechanism into the structural hash: prefix a fixed type byte (e.g. `0x00`
  for a file leaf, `0x01` for a directory node) before hashing. Closes the
  second-preimage / layout-collision class (§4.1) for negligible cost. The current
  SKILL spec lacks this — **flagged for `merkle-researcher` to fold into the
  canonical serialization** (§7). Evidence: §4.1, RFC 9162 §2.1.1.
- **A4 — Use a SIMPLER, but strictly deterministic, child order.** Merkle Sync
  separates files and directories structurally and keys by canonical NFC
  forward-slash names, so it does **not** need git's directory-as-`name/` trailing
  slash rule (§3.2). It MAY sort children by a plain byte-wise compare of the
  canonical name — **provided** the rule is fixed once and applied identically on
  Mac and Windows. The hazard to avoid is *inconsistency*, not the specific rule.
  (`codec.go` / `tree.go`, `SR-5`, `SR-13`.) Evidence: §3.2, §4.3.
- **A6 — Richer leaves than git's `(mode, name, hash)`.** Git's leaf is
  metadata-poor (mode + name + content id) because git is a one-way snapshot
  store. Two-way sync needs `FileInfo{content_hash, size, mode, mtime,
  version_vector, deleted}`, and the structural hash commits to
  `content_hash + mode + deleted + version_vector` (NOT raw `mtime`/`size`). This
  is already decided (`merkle-leaf-shape.md`); recorded here as the deliberate
  divergence from git. Evidence: §3.3, §4.4.
- **A7 — n-ary directory tree, NOT a balanced binary tree; report honest
  complexity.** Do not pad to power-of-two and do not duplicate nodes (avoids
  CVE-2012-2459, §4.2). State diff/rebuild cost as "proportional to the changes /
  tree depth," not "`O(log n)`" (§4.5, §5). Test the property via `SR-5`
  ("one byte changed ⇒ exactly that leaf's branch + root change"), not via a
  big-O assertion. (`differ.go` tests.) Evidence: §4.5, §5.
- **A8 — Plan for incremental rebuild + rename churn.** The prune property applies
  to *rebuilding* the tree after a local change (re-hash only ancestors of changed
  leaves), and a directory rename re-parents a subtree (§4.6, the Dynamo
  "key-ranges-change" analogue). WS-1 acceptance should cover incremental rebuild
  cost, and rename handling stays a logged Phase 2 decision
  (`plan/README.md` "Rename detection"). Evidence: §4.6.

### Explicitly NOT adopted

- **Append-only / consistency proofs** (CT-specific) — Merkle Sync's tree is
  mutable; no append-only log invariant to prove (§2.1).
- **Git's SHA-1 and zlib object storage** — Merkle Sync uses SHA-256 (leaf-shape
  decision) and its own on-disk snapshot format; the git object *encoding* is a
  reference for *layout discipline*, not a format to copy (§4.7).
- **Per-virtual-node/per-range trees (Dynamo)** — Merkle Sync is a 2-device LAN
  tool with one synced folder ⇒ one tree per folder; the multi-range machinery is
  out of scope.

---

## 7. Open questions handed to Phase 2 (with this finding's recommendation)

1. **Domain separation in the structural hash** — *recommend ADOPT* the RFC-6962
   `0x00`/`0x01` (file-leaf / dir-node) type-byte prefix. Owner:
   `merkle-researcher`. Evidence: §4.1. This is a change to the current SKILL §2 /
   `merkle-leaf-shape.md` structural-hash recipe and should be logged as a
   decision when acted on.
2. **Exact canonical child order + serialization grammar** — fix one total order
   over canonical NFC names and one length-prefixed, fixed-width, big-endian,
   type-tagged byte grammar; prove Mac↔Windows determinism with a round-trip
   table test. Owner: `merkle-researcher`. Evidence: §3.2, §4.3.
3. **Incremental rebuild + directory-rename handling** — specify re-hash-ancestors
   rebuild and the rename policy (hash-match heuristic vs delete+create). Owners:
   `merkle-researcher` (rebuild), roster `Rename detection` decision. Evidence:
   §4.6.
4. **(If chunking is ever added)** any binary chunk-tree under `content_hash` must
   use the RFC-9162 `largest-power-of-two` split, never Bitcoin duplicate-last.
   Owner: reconcile workstream (currently deferred). Evidence: §4.2.

No Phase 1 decision file is written by this literature-mapper: per
`plan/agent_roster.md`, Phase 1 produces findings; the consequential tree-hashing
choices above belong to Phase 0 (already decided: `merkle-leaf-shape.md`) and
Phase 2 (`merkle-researcher`), which log decisions when they act.

---

## 8. Sources (all accessed 2026-06-28)

**Merkle's original construction**
- [Merkle signature scheme — Wikipedia](https://en.wikipedia.org/wiki/Merkle_signature_scheme)
- [Merkle, *A Certified Digital Signature* (CRYPTO '87; LNCS 435:218–238) — ResearchGate](https://www.researchgate.net/publication/221355342_A_Certified_Digital_Signature)
- [SciRP citation record for *A Certified Digital Signature*](https://www.scirp.org/reference/referencespapers?referenceid=2082402)
- [Merkle tree — Wikipedia](https://en.wikipedia.org/wiki/Merkle_tree)
- [SJSU, *Improving Smart Grid Security Using Merkle Trees* (N(i,j) recurrence)](https://scholarworks.sjsu.edu/cgi/viewcontent.cgi?article=1419&context=etd_projects)
- [ordep.dev, *Diving into Merkle Trees*](https://ordep.dev/posts/diving-into-merkle-trees)

**Domain separation / formal MTH**
- [RFC 9162 — Certificate Transparency Version 2.0 (Merkle Tree Hash, §2.1.1)](https://datatracker.ietf.org/doc/html/rfc9162)
- [er4hn, *Second Preimage Attack against Merkle Trees*](https://er4hn.info/blog/2022.10.08-second_preimage_on_merkle_tree/)
- [Adevar Labs, *Preventing Second Preimage Attacks in Merkle Trees*](https://www.adevarlabs.com/blog/preventing-second-preimage-attacks-in-merkle-trees-a-complete-guide)

**Git tree-object model**
- [Pro Git, *Git Internals — Git Objects*](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects)
- [dev.to (calebsander), *Git Internals Part 1: the git object model* (hexdump)](https://dev.to/calebsander/git-internals-part-1-the-git-object-model-474m)
- [dulwich, *Git File format*](https://www.samba.org/~jelmer/dulwich/docs/tutorial/file-format.html)
- [w3tutorials, *How Git Calculates Hashes*](https://www.w3tutorials.net/blog/how-is-the-git-hash-calculated/)
- [git mailing-list patch, *intersect_paths: respect mode in git's tree-sort* (trailing-slash rule)](https://git.vger.kernel.narkive.com/JhSl9XOQ/patch-intersect-paths-respect-mode-in-s-tree-sort)
- [git/git, `tree-diff.c`](https://github.com/git/git/blob/master/tree-diff.c)

**Diff / anti-entropy / failure modes**
- [deepengineering, *Merkle Trees and Anti-Entropy Concepts*](https://deepengineering.net/p/merkle-trees-and-anti-entropy-concepts)
- [Apache Cassandra wiki, *AntiEntropy*](https://cwiki.apache.org/confluence/display/CASSANDRA2/AntiEntropy)
- [DataStax, *Manual repair: anti-entropy repair*](https://docs.datastax.com/en/cassandra-oss/3.x/cassandra/operations/opsRepairNodesManualRepair.html)
- [Dynamo: *Amazon's Highly Available Key-value Store* (PDF, §4.7)](https://www.cs.cornell.edu/courses/cs5414/2017fa/papers/dynamo.pdf)
- [All Things Distributed, *Amazon's Dynamo*](https://www.allthingsdistributed.com/2007/10/amazons_dynamo.html)
- [Bitcoin Optech, *Merkle tree vulnerabilities* (CVE-2012-2459)](https://bitcoinops.org/en/topics/merkle-tree-vulnerabilities/)
- [bitcoin/bitcoin commit 01c2807, *duplicate txid flaw* warning](https://github.com/bitcoin/bitcoin/commit/01c2807)
- [BIP 98, *Fast Merkle Trees*](https://bips.dev/98/)

**In-repo cross-references**
- `docs/audit/decisions/phase0/merkle-leaf-shape.md` (leaf metadata; structural-hash inclusion/exclusion)
- `.claude/skills/merkle-sync/SKILL.md:67-121` (tree construction + diff spec)
- `docs/audit/rules/sync-rules.md` (`SR-4`,`SR-5`,`SR-13`), `docs/audit/rules/crossplatform-rules.md` (`XP-1`,`XP-2`), `docs/audit/rules/go-rules.md` (`GR-12`)
- `docs/audit/plan/structure.md` (`internal/merkle/*` file map)
