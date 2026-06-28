package protocol

import (
	"encoding/binary"
	"errors"
	"io"
)

// Frame size budget. See docs/audit/decisions/ws0/framing-read-write-api-and-size-budget.md,
// docs/audit/decisions/phase0/framing-format.md, SR-12, GR-8, CDD-2.
const (
	// MaxFrameLen is the hard ceiling on a single frame's length field
	// (type byte + payload): 16 MiB. A larger advertised length is rejected
	// before any allocation (ReadFrame) and refused on the sender (WriteFrame).
	MaxFrameLen = 16 << 20 // 16 MiB

	// FrameTypeLen is the size of the 1-byte message type every frame carries.
	FrameTypeLen = 1

	// ResponseHeaderLen is the RESPONSE envelope that precedes the chunk data:
	// reqID (uint32, 4 B) + errorCode (uint8, 1 B) = 5 bytes.
	ResponseHeaderLen = 5

	// MaxChunkLen is the largest RESPONSE chunk `data` that still fits under
	// MaxFrameLen once the type byte and the RESPONSE header are accounted for.
	// The RESPONSE builder asserts len(data) <= MaxChunkLen on the sender and the
	// puller (WS-4) clamps its REQUEST.length to it, so a budget miscount fails
	// on the offender, never as an ErrFrameTooLarge on the receiver (CDD-2).
	MaxChunkLen = MaxFrameLen - FrameTypeLen - ResponseHeaderLen

	// frameHeaderLen is the width of the big-endian uint32 length prefix.
	frameHeaderLen = 4
)

// Typed framing sentinels. Callers branch on these with errors.Is (GR-6): an
// ErrFrameTooLarge / ErrZeroLength on a live connection means "drop this peer",
// distinct from a transient io error.
var (
	// ErrFrameTooLarge is returned when an advertised frame length exceeds
	// MaxFrameLen (reader), or when WriteFrame is asked to emit such a frame
	// (sender). No body is allocated or read on the reader path.
	ErrFrameTooLarge = errors.New("protocol: frame exceeds MaxFrameLen")

	// ErrZeroLength is returned when a frame's length field is 0. Every frame has
	// at least a type byte, so length 0 is malformed.
	ErrZeroLength = errors.New("protocol: zero-length frame")

	// ErrChunkTooLarge is returned by the RESPONSE builder when its data exceeds
	// MaxChunkLen — a sender-side budget violation surfaced loudly to the caller.
	ErrChunkTooLarge = errors.New("protocol: RESPONSE chunk exceeds MaxChunkLen")
)

// ReadFrame reads exactly one frame from r and returns its message type and
// payload. It reads the 4-byte length and the body with io.ReadFull (so a
// partial TCP read is reassembled, never treated as a whole message), and it
// validates 0 < length <= MaxFrameLen BEFORE allocating the body — a malformed
// length is a typed error, never an unbounded allocation or a stream desync
// (SR-12, GR-8).
//
// On a successful read the returned payload aliases a freshly allocated buffer
// owned by this call, so the caller may retain it. ReadFrame is type-agnostic:
// the returned MsgType may be reserved (0x00) or unassigned (0x08+); the caller
// applies MsgType.RecvAction to decide drop / dispatch / skip.
func ReadFrame(r io.Reader) (MsgType, []byte, error) {
	var hdr [frameHeaderLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return MsgInvalid, nil, err
	}
	length := binary.BigEndian.Uint32(hdr[:])

	// Validate before allocating the body (SR-12). These two checks are the
	// single most important lines in the package: they turn a textbook OOM/DoS
	// and an off-by-one stream desync into a dropped connection.
	if length == 0 {
		return MsgInvalid, nil, ErrZeroLength
	}
	if length > MaxFrameLen {
		return MsgInvalid, nil, ErrFrameTooLarge
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return MsgInvalid, nil, err
	}
	return MsgType(body[0]), body[1:], nil
}

// WriteFrame writes one frame carrying type t and payload to w. It asserts the
// sender-side budget FrameTypeLen+len(payload) <= MaxFrameLen and returns
// ErrFrameTooLarge WITHOUT writing any bytes if the frame would overflow, so a
// caller bug fails on the sender rather than desyncing the receiver (CDD-2). The
// header, type byte, and payload are emitted in a single Write so a frame is not
// torn across writes at this layer.
func WriteFrame(w io.Writer, t MsgType, payload []byte) error {
	length := FrameTypeLen + len(payload)
	if length > MaxFrameLen {
		return ErrFrameTooLarge
	}
	buf := make([]byte, frameHeaderLen+length)
	binary.BigEndian.PutUint32(buf[:frameHeaderLen], uint32(length))
	buf[frameHeaderLen] = byte(t)
	copy(buf[frameHeaderLen+FrameTypeLen:], payload)
	_, err := w.Write(buf)
	return err
}
