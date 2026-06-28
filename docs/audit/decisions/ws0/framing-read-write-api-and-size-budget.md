# Decision (WS-0): framing read/write API, typed sentinels, and the size budget

- Area: ws0 / internal/protocol (framing.go)
- Status: decided; acted on in this workstream
- Date: 2026-06-29
- Decider: WS-0 implementer
- Plan items discharged: WS-0 acceptance #1 (split-read survival), #2
  (malformed/oversized/zero rejected pre-alloc, typed sentinel, no desync), #3
  (MaxChunkLen budget asserted on the sender).
- Reads-first: `decisions/phase0/framing-format.md`, PR-1 §2/§6,
  `findings/design/protocol-critic/protocol-critic-1-framing-maxlen-accounting.md`
  (→ CDD-2), SR-12, GR-6/GR-7/GR-8, SKILL §5.

## Context

The frame is `[4-byte BE length][1-byte type][payload]`, `length = 1 +
len(payload)`, hard `MaxFrameLen = 16 MiB`, reject `length == 0 || length >
MaxFrameLen` **before** allocating, both header and body read with `io.ReadFull`
(SR-12, GR-8). CDD-2 (from protocol-critic-1) additionally requires the
*complementary sender-side* budget: `MaxChunkLen = MaxFrameLen − FrameTypeLen(1) −
ResponseHeaderLen(5)`, with the sender failing **loudly on itself** rather than
emitting a frame the receiver rejects as `ErrFrameTooLarge` (which would drop the
victim peer and livelock convergence). The open implementation choice is the
*shape* of the read/write API and how each guard surfaces.

## Options (scored 1–5 on correctness / concurrency-safety / testability / cross-platform)

### Option A — stateless free funcs `ReadFrame(r)(MsgType,[]byte,error)` / `WriteFrame(w,t,payload)error`, typed sentinels, guard-before-alloc, `MaxChunkLen` constant + a typed sender error (CHOSEN)
`ReadFrame` does `io.ReadFull(hdr[:4])` → decode `uint32` → reject `0`
(`ErrZeroLength`) / `>MaxFrameLen` (`ErrFrameTooLarge`) **before** `make` → `io.ReadFull(body)`
→ return `body[0]`, `body[1:]`. `WriteFrame` validates `FrameTypeLen+len(payload) ≤
MaxFrameLen` and writes header+type+payload in one buffered write. A
`BuildResponse`/chunk path validates `len(data) ≤ MaxChunkLen` and returns
`ErrChunkTooLarge` before constructing the frame.
- correctness **5** — exactly the framing-format.md contract; guard precedes every
  allocation; `length` counts type+payload so empty `PING` ⇒ `length==1`.
- concurrency-safety **5** — no shared parser state between frames; one reader and
  one writer goroutine own each side (GR-4); functions are reentrant.
- testability **5** — `iotest.OneByteReader` proves split-read reassembly; a crafted
  4-byte header proves oversized→`ErrFrameTooLarge` with **no** body `make`
  (assertable by reading from a reader that would block/erro on the body);
  `MaxChunkLen` boundary round-trips.
- cross-platform **5** — `encoding/binary.BigEndian`, fixed widths; identical bytes
  Mac/Windows.

### Option B — a `*FrameReader`/`*FrameWriter` object wrapping the conn with internal buffers
- correctness **5**, concurrency **4** (object carries mutable state; must be
  owned by exactly one goroutine — easy to misuse), testability **4** (more setup),
  cross-platform **5**.
- **Cost:** stateful surface for no benefit; the conn already serialises reads on one
  goroutine. The stateless funcs compose into the per-conn reader/writer (WS-2)
  without imposing an object lifecycle. Rejected.

### Option C — `ReadFrame` returns a `Frame{Type, Payload}` struct value
- correctness **5**, concurrency **5**, testability **5**, cross-platform **5**.
- **Cost:** purely cosmetic vs Option A; a 2-value `(MsgType, []byte)` return matches
  the framing-format.md signature (`ReadFrame(r) (type, payload, error)`) verbatim and
  is what every consumer wants. Kept as a non-blocking nicety, not adopted to honour
  the pinned signature.

### Option D — `panic` on oversize/zero (hard assert) instead of typed errors
- correctness **3** — a panic on a *peer-supplied* length is a remote DoS-by-crash;
  the whole point of SR-12 is to convert a bad length into a **dropped connection**,
  not a process abort.
- concurrency **2**, testability **3** (panics are awkward to assert at a trust
  boundary), cross-platform **5**. Rejected for the read path. For the *sender* budget
  a panic is defensible (programmer bug), but a returned typed error is daemon-safe
  and still "loud" (the caller must check it), so we use a typed error there too.

## Decision

Adopt **Option A**. `internal/protocol/framing.go` exports:
- constants `MaxFrameLen = 16<<20`, `FrameTypeLen = 1`, `ResponseHeaderLen = 5`,
  `MaxChunkLen = MaxFrameLen - FrameTypeLen - ResponseHeaderLen`;
- typed sentinels `ErrFrameTooLarge`, `ErrZeroLength`, `ErrChunkTooLarge`
  (`errors.New`, branchable via `errors.Is`, GR-6);
- `func ReadFrame(r io.Reader) (MsgType, []byte, error)` — `io.ReadFull` header,
  reject `0`/`>MaxFrameLen` **pre-alloc**, `io.ReadFull` body;
- `func WriteFrame(w io.Writer, t MsgType, payload []byte) error` — asserts
  `FrameTypeLen+len(payload) ≤ MaxFrameLen` (else `ErrFrameTooLarge`, **no bytes
  written**), single buffered write.

The `RESPONSE` chunk budget (`len(data) ≤ MaxChunkLen` → `ErrChunkTooLarge`) is
enforced in the message layer's response builder (decision
`message-envelope-codec-and-unknown-type-policy.md`), co-located with the
`RESPONSE` envelope it protects.

## Rationale

- Stateless free functions mirror the pinned `framing-format.md` API, compose
  cleanly into the WS-2 per-conn reader/writer, and carry no inter-frame state to
  race on.
- Guard-before-alloc + `io.ReadFull` is the literal SR-12/GR-8 contract; the typed
  sentinels let the transport branch "drop this peer" vs "transient read error"
  (GR-6) and let the *sender* fail on its own budget bug rather than punishing the
  receiver (CDD-2).
- Typed errors over panics at the network boundary turn adversarial input into a
  dropped connection, never a crash.

## Consequences

- `framing.go` + `framing_test.go` (`TestReadFrame_OneByteReader`,
  `TestReadFrame_OversizedRejected` asserting no body read on a bad length,
  `TestReadFrame_ZeroLength`, `TestWriteFrame_OversizedRejectedNoWrite`,
  `TestMaxChunkLen_BoundaryRoundTrips`).
- `MaxChunkLen` is the single named budget WS-4's puller clamps to and WS-2's conn
  loop relies on; `ResponseHeaderLen(5)` = `reqID u32 + errorCode u8`.
- Cross-refs SR-12, GR-8, CDD-2, PR-1, `framing-format.md`.
