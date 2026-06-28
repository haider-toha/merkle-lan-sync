package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// headerOnlyReader yields exactly hdr and then fails the test if Read is called
// again — proving ReadFrame rejects a bad length BEFORE reading (or allocating)
// the body.
type headerOnlyReader struct {
	hdr []byte
	off int
	t   *testing.T
}

func (r *headerOnlyReader) Read(p []byte) (int, error) {
	if r.off >= len(r.hdr) {
		r.t.Fatalf("ReadFrame read past the 4-byte header on a bad length: the body was read/allocated, violating the pre-alloc guard (SR-12)")
	}
	n := copy(p, r.hdr[r.off:])
	r.off += n
	return n, nil
}

func header(length uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, length)
	return b
}

// TestReadFrame_OneByteReader: a valid frame survives being delivered one byte at
// a time (the partial-TCP-read survival property, SR-12 / GR-8).
func TestReadFrame_OneByteReader(t *testing.T) {
	cases := []struct {
		name    string
		typ     MsgType
		payload []byte
	}{
		{"empty payload (PING-shaped, length==1)", MsgPing, nil},
		{"one byte", MsgRequest, []byte{0xAB}},
		{"all 256 byte values", MsgResponse, allBytes()},
		{"32 bytes", MsgHello, bytes.Repeat([]byte{0x5A}, 32)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tc.typ, tc.payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			gotType, gotPayload, err := ReadFrame(iotest.OneByteReader(&buf))
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if gotType != tc.typ {
				t.Errorf("type = %v, want %v", gotType, tc.typ)
			}
			if !bytes.Equal(gotPayload, tc.payload) && !(len(gotPayload) == 0 && len(tc.payload) == 0) {
				t.Errorf("payload = %x, want %x", gotPayload, tc.payload)
			}
		})
	}
}

// TestWriteReadFrame_RoundTrip: raw frames round-trip over a plain buffer.
func TestWriteReadFrame_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		typ     MsgType
		payload []byte
	}{
		{"ping empty", MsgPing, nil},
		{"close reason bytes", MsgClose, []byte("bye")},
		{"unassigned type 0x08 still frames", 0x08, []byte("future")},
		{"reserved type 0x00 still frames (dispatch decides fatal)", MsgInvalid, []byte{0x01}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tc.typ, tc.payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			gotType, gotPayload, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if gotType != tc.typ {
				t.Errorf("type = %#x, want %#x", byte(gotType), byte(tc.typ))
			}
			if !bytes.Equal(gotPayload, tc.payload) && !(len(gotPayload) == 0 && len(tc.payload) == 0) {
				t.Errorf("payload = %x, want %x", gotPayload, tc.payload)
			}
		})
	}
}

// TestReadFrame_ZeroLength: a length-0 frame is rejected with the typed sentinel,
// before any body read.
func TestReadFrame_ZeroLength(t *testing.T) {
	r := &headerOnlyReader{hdr: header(0), t: t}
	_, _, err := ReadFrame(r)
	if !errors.Is(err, ErrZeroLength) {
		t.Fatalf("err = %v, want ErrZeroLength", err)
	}
}

// TestReadFrame_OversizedRejected: a length > MaxFrameLen is rejected with the
// typed sentinel BEFORE the body is read or allocated (the headerOnlyReader
// fails the test if the body is touched).
func TestReadFrame_OversizedRejected(t *testing.T) {
	cases := []struct {
		name   string
		length uint32
	}{
		{"MaxFrameLen+1", MaxFrameLen + 1},
		{"max uint32", ^uint32(0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &headerOnlyReader{hdr: header(tc.length), t: t}
			_, _, err := ReadFrame(r)
			if !errors.Is(err, ErrFrameTooLarge) {
				t.Fatalf("err = %v, want ErrFrameTooLarge", err)
			}
		})
	}
}

// TestReadFrame_BoundaryAccepted: length == MaxFrameLen is the largest accepted
// frame (the guard is strictly greater-than).
func TestReadFrame_BoundaryAccepted(t *testing.T) {
	var buf bytes.Buffer
	payload := bytes.Repeat([]byte{0x7E}, MaxFrameLen-FrameTypeLen)
	if err := WriteFrame(&buf, MsgResponse, payload); err != nil {
		t.Fatalf("WriteFrame at boundary: %v", err)
	}
	typ, got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame at boundary: %v", err)
	}
	if typ != MsgResponse || len(got) != len(payload) {
		t.Fatalf("boundary frame did not round-trip: type=%v len=%d", typ, len(got))
	}
}

// TestReadFrame_Truncated: a short header or short body is an io error, not a
// silent partial message.
func TestReadFrame_Truncated(t *testing.T) {
	t.Run("short header", func(t *testing.T) {
		_, _, err := ReadFrame(bytes.NewReader([]byte{0x00, 0x00})) // 2 of 4 header bytes
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
	t.Run("short body", func(t *testing.T) {
		b := append(header(10), 0x01, 0x02) // claims 10 bytes, supplies 2
		_, _, err := ReadFrame(bytes.NewReader(b))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
	t.Run("empty stream is EOF", func(t *testing.T) {
		_, _, err := ReadFrame(bytes.NewReader(nil))
		if !errors.Is(err, io.EOF) {
			t.Fatalf("err = %v, want io.EOF", err)
		}
	})
}

// TestWriteFrame_OversizedRejectedNoWrite: WriteFrame refuses an over-budget
// frame with the typed error and writes ZERO bytes (the sender fails on itself).
func TestWriteFrame_OversizedRejectedNoWrite(t *testing.T) {
	var buf bytes.Buffer
	oversized := make([]byte, MaxFrameLen) // 1 + MaxFrameLen > MaxFrameLen
	err := WriteFrame(&buf, MsgResponse, oversized)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("WriteFrame wrote %d bytes on rejection; want 0 (must not desync the stream)", buf.Len())
	}
}

// TestMaxChunkLen_BoundaryRoundTrips: a RESPONSE whose data is exactly
// MaxChunkLen produces a frame of exactly MaxFrameLen that round-trips; one byte
// over fails loudly on the sender as ErrChunkTooLarge (CDD-2).
func TestMaxChunkLen_BoundaryRoundTrips(t *testing.T) {
	t.Run("exactly MaxChunkLen round-trips at MaxFrameLen", func(t *testing.T) {
		resp := Response{ReqID: 42, Code: CodeOK, Data: bytes.Repeat([]byte{0x11}, MaxChunkLen)}
		var buf bytes.Buffer
		if err := WriteMessage(&buf, resp); err != nil {
			t.Fatalf("WriteMessage at MaxChunkLen: %v", err)
		}
		// frame length = FrameTypeLen + ResponseHeaderLen + MaxChunkLen = MaxFrameLen
		wantFrame := frameHeaderLen + MaxFrameLen
		if buf.Len() != wantFrame {
			t.Fatalf("frame size = %d, want %d (header + MaxFrameLen)", buf.Len(), wantFrame)
		}
		typ, payload, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		m, err := DecodeMessage(typ, payload)
		if err != nil {
			t.Fatalf("DecodeMessage: %v", err)
		}
		got, ok := m.(Response)
		if !ok {
			t.Fatalf("decoded %T, want Response", m)
		}
		if got.ReqID != 42 || got.Code != CodeOK || len(got.Data) != MaxChunkLen {
			t.Fatalf("boundary RESPONSE did not round-trip: reqID=%d code=%d dataLen=%d", got.ReqID, got.Code, len(got.Data))
		}
	})
	t.Run("MaxChunkLen+1 fails on the sender", func(t *testing.T) {
		resp := Response{ReqID: 1, Code: CodeOK, Data: make([]byte, MaxChunkLen+1)}
		var buf bytes.Buffer
		err := WriteMessage(&buf, resp)
		if !errors.Is(err, ErrChunkTooLarge) {
			t.Fatalf("err = %v, want ErrChunkTooLarge", err)
		}
		if buf.Len() != 0 {
			t.Fatalf("over-budget RESPONSE wrote %d bytes; want 0", buf.Len())
		}
	})
}

func allBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}
