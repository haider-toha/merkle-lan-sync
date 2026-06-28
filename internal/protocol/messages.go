package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MsgType is the 1-byte frame type. 0x00 is reserved-invalid; 0x01..0x07 are the
// frozen catalogue; 0x08+ are unassigned (skipped by an older peer). Codes are
// frozen — future types take the next free 0x08+ code, never a renumber
// (docs/audit/decisions/protocol/message-type-enumeration.md).
type MsgType byte

const (
	MsgInvalid     MsgType = 0x00 // reserved/invalid — never sent; receipt is fatal
	MsgHello       MsgType = 0x01 // handshake: proto version, DeviceID, folder, root hash, feature flags
	MsgIndex       MsgType = 0x02 // full index snapshot (folderID + count + wireFileInfo×count)
	MsgIndexUpdate MsgType = 0x03 // incremental FileInfo deltas since the last index (SR-6 broadcast)
	MsgRequest     MsgType = 0x04 // want bytes of a file: reqID, path, content hash, offset, length
	MsgResponse    MsgType = 0x05 // chunk data (or a typed error) for a prior REQUEST
	MsgPing        MsgType = 0x06 // keepalive — empty payload (frame length == 1)
	MsgClose       MsgType = 0x07 // graceful shutdown — optional reason
)

func (t MsgType) String() string {
	switch t {
	case MsgInvalid:
		return "INVALID"
	case MsgHello:
		return "HELLO"
	case MsgIndex:
		return "INDEX"
	case MsgIndexUpdate:
		return "INDEX_UPDATE"
	case MsgRequest:
		return "REQUEST"
	case MsgResponse:
		return "RESPONSE"
	case MsgPing:
		return "PING"
	case MsgClose:
		return "CLOSE"
	default:
		return fmt.Sprintf("MsgType(0x%02x)", byte(t))
	}
}

// RecvAction is the total policy a receiver applies to a frame's type byte. It is
// defined for every one of the 256 byte values (RecvAction below).
type RecvAction int

const (
	// ActionFatal: a reserved 0x00 type — corruption / zero-init. Drop the
	// connection.
	ActionFatal RecvAction = iota
	// ActionDispatch: a known 0x01..0x07 type — decode and handle.
	ActionDispatch
	// ActionSkip: an unassigned 0x08+ type — discard this frame and continue
	// (the length prefix already delimited it), preserving forward-compat.
	ActionSkip
)

func (a RecvAction) String() string {
	switch a {
	case ActionFatal:
		return "Fatal"
	case ActionDispatch:
		return "Dispatch"
	case ActionSkip:
		return "Skip"
	default:
		return fmt.Sprintf("RecvAction(%d)", int(a))
	}
}

// RecvAction classifies a received type byte into the split, total unknown-type
// policy: 0x00 ⇒ Fatal, 0x01..0x07 ⇒ Dispatch, 0x08..0xFF ⇒ Skip
// (docs/audit/decisions/protocol/message-type-enumeration.md, PR-1 §3).
func (t MsgType) RecvAction() RecvAction {
	switch {
	case t == MsgInvalid:
		return ActionFatal
	case t >= MsgHello && t <= MsgClose:
		return ActionDispatch
	default:
		return ActionSkip
	}
}

// ErrorCode is the RESPONSE status (PR-1 §4); it mirrors BEP's enum so a source
// that no longer has a requested block answers cleanly rather than hanging the
// puller.
type ErrorCode uint8

const (
	CodeOK          ErrorCode = 0
	CodeGeneric     ErrorCode = 1
	CodeNoSuchFile  ErrorCode = 2
	CodeInvalidFile ErrorCode = 3
)

// Codec sentinels (branchable with errors.Is, GR-6).
var (
	// ErrUnknownMsgType is returned by DecodeMessage for any non-dispatchable
	// type (0x00 or 0x08+). The caller uses RecvAction to decide drop vs skip.
	ErrUnknownMsgType = errors.New("protocol: unknown or reserved message type")
	// ErrShortPayload is returned when a payload is shorter than its grammar
	// requires (a truncated or adversarial message).
	ErrShortPayload = errors.New("protocol: payload shorter than message grammar requires")
	// ErrStringTooLong is returned when a length-prefixed string exceeds the
	// uint16 prefix range on encode.
	ErrStringTooLong = errors.New("protocol: length-prefixed string exceeds uint16")
)

// Message is one of the seven envelope types. The encode method is unexported so
// only this package defines the wire types; callers use EncodeMessage /
// WriteMessage / DecodeMessage.
type Message interface {
	Type() MsgType
	encodeInto(e *encbuf)
}

// Hello is the first post-TLS frame each side sends. rootHash drives the SR-5
// "skip INDEX when already converged" short-circuit; featureFlags is the
// fail-closed algo/chunking negotiation hook.
type Hello struct {
	ProtoVersion uint16
	DeviceID     DeviceID
	FolderID     string
	RootHash     [32]byte
	FeatureFlags uint32
}

func (Hello) Type() MsgType { return MsgHello }
func (h Hello) encodeInto(e *encbuf) {
	e.u16(h.ProtoVersion)
	e.raw(h.DeviceID[:])
	e.lenPrefixed(h.FolderID)
	e.raw(h.RootHash[:])
	e.u32(h.FeatureFlags)
}

// Index is a full index snapshot. Body is the opaque concatenation of Count wire
// FileInfo encodings; its per-entry grammar is finalized in WS-1 (internal/merkle),
// so this package round-trips the envelope without fixing it (the WS-0/WS-1 seam).
type Index struct {
	FolderID string
	Count    uint32
	Body     []byte
}

func (Index) Type() MsgType          { return MsgIndex }
func (m Index) encodeInto(e *encbuf) { encodeIndexBody(e, m.FolderID, m.Count, m.Body) }

// IndexUpdate carries incremental FileInfo deltas (the SR-6 post-local-change
// broadcast). Same envelope as Index.
type IndexUpdate struct {
	FolderID string
	Count    uint32
	Body     []byte
}

func (IndexUpdate) Type() MsgType          { return MsgIndexUpdate }
func (m IndexUpdate) encodeInto(e *encbuf) { encodeIndexBody(e, m.FolderID, m.Count, m.Body) }

func encodeIndexBody(e *encbuf, folderID string, count uint32, body []byte) {
	e.lenPrefixed(folderID)
	e.u32(count)
	e.raw(body)
}

// Request asks a source for a byte range of a file. The puller (WS-4) clamps
// Length <= MaxChunkLen and splits large ranges.
type Request struct {
	ReqID       uint32
	Path        string
	ContentHash [32]byte
	Offset      uint64
	Length      uint32
}

func (Request) Type() MsgType { return MsgRequest }
func (r Request) encodeInto(e *encbuf) {
	e.u32(r.ReqID)
	e.lenPrefixed(r.Path)
	e.raw(r.ContentHash[:])
	e.u64(r.Offset)
	e.u32(r.Length)
}

// Response answers a prior Request with chunk data, or an error code. Encoding
// asserts the sender-side budget len(Data) <= MaxChunkLen (CDD-2): an over-budget
// chunk fails loudly on the sender as ErrChunkTooLarge instead of producing a
// frame the receiver rejects as ErrFrameTooLarge.
type Response struct {
	ReqID uint32
	Code  ErrorCode
	Data  []byte
}

func (Response) Type() MsgType { return MsgResponse }
func (r Response) encodeInto(e *encbuf) {
	if len(r.Data) > MaxChunkLen {
		e.fail(ErrChunkTooLarge)
		return
	}
	e.u32(r.ReqID)
	e.u8(uint8(r.Code))
	e.raw(r.Data)
}

// Ping is a keepalive with an empty payload (frame length == 1).
type Ping struct{}

func (Ping) Type() MsgType        { return MsgPing }
func (Ping) encodeInto(e *encbuf) {}

// Close requests a graceful shutdown with an optional reason.
type Close struct {
	Reason string
}

func (Close) Type() MsgType          { return MsgClose }
func (c Close) encodeInto(e *encbuf) { e.lenPrefixed(c.Reason) }

// EncodeMessage returns the frame payload bytes for m, surfacing any sender-side
// encoding error (ErrChunkTooLarge for an over-budget RESPONSE, ErrStringTooLong
// for an over-long length-prefixed field).
func EncodeMessage(m Message) ([]byte, error) {
	var e encbuf
	m.encodeInto(&e)
	if e.err != nil {
		return nil, e.err
	}
	return e.b, nil
}

// WriteMessage encodes m and writes it as one frame to w (EncodeMessage +
// WriteFrame). The frame-length budget is enforced by WriteFrame.
func WriteMessage(w io.Writer, m Message) error {
	payload, err := EncodeMessage(m)
	if err != nil {
		return err
	}
	return WriteFrame(w, m.Type(), payload)
}

// DecodeMessage parses payload (the bytes ReadFrame returns) for a dispatchable
// type into the matching envelope. A reserved/unassigned type yields
// ErrUnknownMsgType; a truncated payload yields ErrShortPayload. Decoding never
// panics on adversarial input. Returned []byte fields (Index.Body,
// Response.Data) alias payload, which ReadFrame allocates fresh per frame.
func DecodeMessage(t MsgType, payload []byte) (Message, error) {
	switch t {
	case MsgHello:
		return decodeHello(payload)
	case MsgIndex:
		folderID, count, body, err := decodeIndexBody(payload)
		if err != nil {
			return nil, err
		}
		return Index{FolderID: folderID, Count: count, Body: body}, nil
	case MsgIndexUpdate:
		folderID, count, body, err := decodeIndexBody(payload)
		if err != nil {
			return nil, err
		}
		return IndexUpdate{FolderID: folderID, Count: count, Body: body}, nil
	case MsgRequest:
		return decodeRequest(payload)
	case MsgResponse:
		return decodeResponse(payload)
	case MsgPing:
		return Ping{}, nil
	case MsgClose:
		return decodeClose(payload)
	default:
		return nil, fmt.Errorf("%w: 0x%02x", ErrUnknownMsgType, byte(t))
	}
}

func decodeHello(payload []byte) (Hello, error) {
	d := decbuf{b: payload}
	var h Hello
	h.ProtoVersion = d.u16()
	h.DeviceID = d.array32()
	h.FolderID = d.lenPrefixed()
	h.RootHash = d.array32()
	h.FeatureFlags = d.u32()
	return h, d.err
}

func decodeIndexBody(payload []byte) (folderID string, count uint32, body []byte, err error) {
	d := decbuf{b: payload}
	folderID = d.lenPrefixed()
	count = d.u32()
	body = d.rest()
	return folderID, count, body, d.err
}

func decodeRequest(payload []byte) (Request, error) {
	d := decbuf{b: payload}
	var r Request
	r.ReqID = d.u32()
	r.Path = d.lenPrefixed()
	r.ContentHash = d.array32()
	r.Offset = d.u64()
	r.Length = d.u32()
	return r, d.err
}

func decodeResponse(payload []byte) (Response, error) {
	d := decbuf{b: payload}
	var r Response
	r.ReqID = d.u32()
	r.Code = ErrorCode(d.u8())
	r.Data = d.rest()
	return r, d.err
}

func decodeClose(payload []byte) (Close, error) {
	if len(payload) == 0 {
		return Close{}, nil // tolerate a fully empty payload as reason ""
	}
	d := decbuf{b: payload}
	reason := d.lenPrefixed()
	return Close{Reason: reason}, d.err
}

// encbuf is an append-only big-endian encoder with a sticky error, so an encode
// path can fail (e.g. an over-budget RESPONSE) without panicking or partial
// surprises — EncodeMessage checks e.err once at the end.
type encbuf struct {
	b   []byte
	err error
}

func (e *encbuf) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}
func (e *encbuf) u8(v uint8) {
	if e.err != nil {
		return
	}
	e.b = append(e.b, v)
}
func (e *encbuf) u16(v uint16) {
	if e.err != nil {
		return
	}
	e.b = binary.BigEndian.AppendUint16(e.b, v)
}
func (e *encbuf) u32(v uint32) {
	if e.err != nil {
		return
	}
	e.b = binary.BigEndian.AppendUint32(e.b, v)
}
func (e *encbuf) u64(v uint64) {
	if e.err != nil {
		return
	}
	e.b = binary.BigEndian.AppendUint64(e.b, v)
}
func (e *encbuf) raw(p []byte) {
	if e.err != nil {
		return
	}
	e.b = append(e.b, p...)
}
func (e *encbuf) lenPrefixed(s string) {
	if e.err != nil {
		return
	}
	if len(s) > 0xFFFF {
		e.fail(ErrStringTooLong)
		return
	}
	e.u16(uint16(len(s)))
	e.b = append(e.b, s...)
}

// decbuf is a bounds-checked big-endian decoder with a sticky error, so a
// truncated or adversarial payload yields ErrShortPayload instead of a panic.
type decbuf struct {
	b   []byte
	off int
	err error
}

func (d *decbuf) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}
func (d *decbuf) u8() uint8 {
	if d.err != nil {
		return 0
	}
	if d.off+1 > len(d.b) {
		d.fail(ErrShortPayload)
		return 0
	}
	v := d.b[d.off]
	d.off++
	return v
}
func (d *decbuf) u16() uint16 {
	if d.err != nil {
		return 0
	}
	if d.off+2 > len(d.b) {
		d.fail(ErrShortPayload)
		return 0
	}
	v := binary.BigEndian.Uint16(d.b[d.off:])
	d.off += 2
	return v
}
func (d *decbuf) u32() uint32 {
	if d.err != nil {
		return 0
	}
	if d.off+4 > len(d.b) {
		d.fail(ErrShortPayload)
		return 0
	}
	v := binary.BigEndian.Uint32(d.b[d.off:])
	d.off += 4
	return v
}
func (d *decbuf) u64() uint64 {
	if d.err != nil {
		return 0
	}
	if d.off+8 > len(d.b) {
		d.fail(ErrShortPayload)
		return 0
	}
	v := binary.BigEndian.Uint64(d.b[d.off:])
	d.off += 8
	return v
}

// raw returns the next n bytes as a sub-slice of the payload (zero-copy).
func (d *decbuf) raw(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || d.off+n > len(d.b) {
		d.fail(ErrShortPayload)
		return nil
	}
	v := d.b[d.off : d.off+n]
	d.off += n
	return v
}
func (d *decbuf) array32() [32]byte {
	var a [32]byte
	copy(a[:], d.raw(32))
	return a
}
func (d *decbuf) lenPrefixed() string {
	n := int(d.u16())
	return string(d.raw(n))
}

// rest returns the remaining unread bytes (zero-copy alias of the payload).
func (d *decbuf) rest() []byte {
	if d.err != nil {
		return nil
	}
	v := d.b[d.off:]
	d.off = len(d.b)
	return v
}
