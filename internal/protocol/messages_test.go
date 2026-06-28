package protocol

import (
	"bytes"
	"errors"
	"testing"
)

// hostileStrings exercises the codec's transparency for the canonical-key /
// path / reason / folderID fields. Normalisation is WS-1's job (internal/pathnorm);
// the protocol layer must carry these byte sequences losslessly so a
// Windows-hostile name survives the wire untouched. Includes illegal chars,
// reserved device names, trailing dot/space, backslashes, control chars, and
// NFD vs NFC byte forms.
func hostileStrings() []string {
	return []string{
		"",                         // empty
		"normal.txt",               // baseline
		"dir/sub/file.txt",         // canonical forward-slash
		`dir\sub\file.txt`,         // backslashes (must NOT be reinterpreted)
		`a<b>c:d"e|f?g*h`,          // every Windows-illegal char
		"CON", "PRN", "AUX", "NUL", // reserved device names
		"NUL.txt", "COM1", "LPT9", // reserved names with extension / numbered
		"trailingdot.", "trailingspace ", // trailing dot / space
		"\x01\x02\x1f",   // control characters
		"é",             // NFD: 'e' + combining acute
		"é",              // NFC: precomposed 'é'
		"résumé.pdf",     // NFC résumé
		"résumé.pdf",   // NFD résumé (distinct bytes — must stay distinct)
		"\U0001F600.png", // 4-byte UTF-8 (emoji)
	}
}

func sampleHash(seed byte) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = seed + byte(i)
	}
	return h
}

// roundTripMessage encodes m to a frame and decodes it back.
func roundTripMessage(t *testing.T, m Message) Message {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteMessage(&buf, m); err != nil {
		t.Fatalf("WriteMessage(%T): %v", m, err)
	}
	typ, payload, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame(%T): %v", m, err)
	}
	if typ != m.Type() {
		t.Fatalf("frame type = %v, want %v", typ, m.Type())
	}
	got, err := DecodeMessage(typ, payload)
	if err != nil {
		t.Fatalf("DecodeMessage(%T): %v", m, err)
	}
	return got
}

func TestMessages_RoundTrip(t *testing.T) {
	dev := DeviceIDFromCert([]byte("device-cert-der"))

	t.Run("HELLO with hostile folderIDs", func(t *testing.T) {
		for _, fid := range hostileStrings() {
			in := Hello{ProtoVersion: 1, DeviceID: dev, FolderID: fid, RootHash: sampleHash(7), FeatureFlags: 0xDEADBEEF}
			got := roundTripMessage(t, in).(Hello)
			if got.ProtoVersion != in.ProtoVersion || got.DeviceID != in.DeviceID ||
				got.FolderID != in.FolderID || got.RootHash != in.RootHash || got.FeatureFlags != in.FeatureFlags {
				t.Errorf("HELLO round-trip mismatch for folderID %q: got %+v want %+v", fid, got, in)
			}
		}
	})

	t.Run("INDEX and INDEX_UPDATE with opaque body", func(t *testing.T) {
		body := []byte{0x00, 0xFF, 0x10, 0x20} // opaque wireFileInfo region (WS-1 owns its grammar)
		for _, fid := range hostileStrings() {
			idx := Index{FolderID: fid, Count: 3, Body: body}
			got := roundTripMessage(t, idx).(Index)
			if got.FolderID != fid || got.Count != 3 || !bytes.Equal(got.Body, body) {
				t.Errorf("INDEX round-trip mismatch for %q: got %+v", fid, got)
			}
			upd := IndexUpdate{FolderID: fid, Count: 1, Body: body}
			gotU := roundTripMessage(t, upd).(IndexUpdate)
			if gotU.FolderID != fid || gotU.Count != 1 || !bytes.Equal(gotU.Body, body) {
				t.Errorf("INDEX_UPDATE round-trip mismatch for %q: got %+v", fid, gotU)
			}
		}
	})

	t.Run("REQUEST with hostile paths", func(t *testing.T) {
		for _, p := range hostileStrings() {
			in := Request{ReqID: 99, Path: p, ContentHash: sampleHash(3), Offset: 1 << 40, Length: 32768}
			got := roundTripMessage(t, in).(Request)
			if got.ReqID != in.ReqID || got.Path != in.Path || got.ContentHash != in.ContentHash ||
				got.Offset != in.Offset || got.Length != in.Length {
				t.Errorf("REQUEST round-trip mismatch for path %q: got %+v want %+v", p, got, in)
			}
		}
	})

	t.Run("RESPONSE codes and data", func(t *testing.T) {
		cases := []Response{
			{ReqID: 1, Code: CodeOK, Data: []byte("chunk-bytes")},
			{ReqID: 2, Code: CodeGeneric, Data: nil},
			{ReqID: 3, Code: CodeNoSuchFile, Data: []byte{}},
			{ReqID: 4, Code: CodeInvalidFile, Data: allBytes()},
		}
		for _, in := range cases {
			got := roundTripMessage(t, in).(Response)
			if got.ReqID != in.ReqID || got.Code != in.Code {
				t.Errorf("RESPONSE header mismatch: got %+v want %+v", got, in)
			}
			if len(got.Data) != len(in.Data) || (len(in.Data) > 0 && !bytes.Equal(got.Data, in.Data)) {
				t.Errorf("RESPONSE data mismatch: got %x want %x", got.Data, in.Data)
			}
		}
	})

	t.Run("PING empty payload", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteMessage(&buf, Ping{}); err != nil {
			t.Fatalf("WriteMessage PING: %v", err)
		}
		// PING is length==1 (type byte only) → 4-byte header + 1 type byte.
		if buf.Len() != frameHeaderLen+1 {
			t.Fatalf("PING frame size = %d, want %d", buf.Len(), frameHeaderLen+1)
		}
		typ, payload, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame PING: %v", err)
		}
		if typ != MsgPing || len(payload) != 0 {
			t.Fatalf("PING decoded type=%v payloadLen=%d", typ, len(payload))
		}
		if _, err := DecodeMessage(typ, payload); err != nil {
			t.Fatalf("DecodeMessage PING: %v", err)
		}
	})

	t.Run("CLOSE with hostile reasons (incl. empty)", func(t *testing.T) {
		for _, r := range hostileStrings() {
			got := roundTripMessage(t, Close{Reason: r}).(Close)
			if got.Reason != r {
				t.Errorf("CLOSE round-trip mismatch: got %q want %q", got.Reason, r)
			}
		}
	})
}

// TestClose_ToleratesEmptyPayload: a CLOSE frame with a truly empty payload
// (length==1) decodes to reason "" (forward-compat with a minimal CLOSE).
func TestClose_ToleratesEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, MsgClose, nil); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	typ, payload, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	m, err := DecodeMessage(typ, payload)
	if err != nil {
		t.Fatalf("DecodeMessage: %v", err)
	}
	if c, ok := m.(Close); !ok || c.Reason != "" {
		t.Fatalf("decoded %#v, want Close{Reason:\"\"}", m)
	}
}

// TestMsgType_UnknownPolicy: the unknown-type policy is total and split —
// 0x00 fatal, 0x01..0x07 dispatch, 0x08..0xFF skip.
func TestMsgType_UnknownPolicy(t *testing.T) {
	named := []struct {
		typ  MsgType
		want RecvAction
	}{
		{0x00, ActionFatal},
		{MsgHello, ActionDispatch},
		{MsgIndex, ActionDispatch},
		{MsgIndexUpdate, ActionDispatch},
		{MsgRequest, ActionDispatch},
		{MsgResponse, ActionDispatch},
		{MsgPing, ActionDispatch},
		{MsgClose, ActionDispatch},
		{0x08, ActionSkip},
		{0x09, ActionSkip},
		{0x10, ActionSkip},
		{0x7f, ActionSkip},
		{0x80, ActionSkip},
		{0xfe, ActionSkip},
		{0xff, ActionSkip},
	}
	for _, tc := range named {
		if got := tc.typ.RecvAction(); got != tc.want {
			t.Errorf("RecvAction(0x%02x) = %v, want %v", byte(tc.typ), got, tc.want)
		}
	}

	// Exhaustive: every one of the 256 byte values is classified per the spec.
	for v := 0; v < 256; v++ {
		typ := MsgType(byte(v))
		want := ActionSkip
		switch {
		case v == 0x00:
			want = ActionFatal
		case v >= 0x01 && v <= 0x07:
			want = ActionDispatch
		}
		if got := typ.RecvAction(); got != want {
			t.Fatalf("RecvAction(0x%02x) = %v, want %v", v, got, want)
		}
	}

	// DecodeMessage on a non-dispatchable type is a typed error.
	for _, typ := range []MsgType{0x00, 0x08, 0xff} {
		if _, err := DecodeMessage(typ, []byte{0x01, 0x02}); !errors.Is(err, ErrUnknownMsgType) {
			t.Errorf("DecodeMessage(0x%02x) err = %v, want ErrUnknownMsgType", byte(typ), err)
		}
	}
}

// TestSkipUnknownFrame_StreamContinues: an unassigned 0x08+ frame is skipped and
// the very next frame still parses — the length prefix makes the unknown payload
// safe to discard, so there is no stream desync (the forward-compat property).
func TestSkipUnknownFrame_StreamContinues(t *testing.T) {
	var buf bytes.Buffer
	// An unknown future message, then a valid PING.
	if err := WriteFrame(&buf, 0x08, []byte("an unknown future message body")); err != nil {
		t.Fatalf("WriteFrame unknown: %v", err)
	}
	if err := WriteMessage(&buf, Ping{}); err != nil {
		t.Fatalf("WriteMessage ping: %v", err)
	}

	// Frame 1: read it, see Skip, discard (ReadFrame already consumed the body).
	typ, _, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame frame1: %v", err)
	}
	if act := typ.RecvAction(); act != ActionSkip {
		t.Fatalf("frame1 action = %v, want Skip", act)
	}

	// Frame 2: must still be a clean PING (no desync).
	typ2, payload2, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame frame2 after skip: %v", err)
	}
	if typ2 != MsgPing || len(payload2) != 0 {
		t.Fatalf("after skipping unknown frame, next = type %v payloadLen %d; want clean PING", typ2, len(payload2))
	}
	if act := typ2.RecvAction(); act != ActionDispatch {
		t.Fatalf("frame2 action = %v, want Dispatch", act)
	}
}

// TestFatalReservedType: a 0x00 type byte (distinct from a zero-LENGTH frame) is
// classified fatal at dispatch.
func TestFatalReservedType(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, MsgInvalid, []byte{0xAA}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	typ, _, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err) // the frame itself is well-formed; the TYPE is fatal
	}
	if act := typ.RecvAction(); act != ActionFatal {
		t.Fatalf("0x00 type action = %v, want Fatal", act)
	}
}

// TestDecode_TruncatedPayload: a payload shorter than a type's grammar yields the
// typed ErrShortPayload, never a panic.
func TestDecode_TruncatedPayload(t *testing.T) {
	cases := []struct {
		name    string
		typ     MsgType
		payload []byte
	}{
		{"HELLO missing most fields", MsgHello, []byte{0x00}},          // needs >= 2+32+2+32+4 bytes
		{"REQUEST missing tail", MsgRequest, []byte{0x00, 0x00, 0x00}}, // reqID needs 4
		{"RESPONSE missing header", MsgResponse, []byte{0x00, 0x00}},   // reqID(4)+code(1) min 5
		{"INDEX missing count", MsgIndex, []byte{0x00, 0x00}},          // folderID len-prefix then u32 count
		{"CLOSE truncated len prefix", MsgClose, []byte{0x05}},         // claims a u16 len, only 1 byte
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeMessage(tc.typ, tc.payload)
			if !errors.Is(err, ErrShortPayload) {
				t.Fatalf("err = %v, want ErrShortPayload", err)
			}
		})
	}
}

// TestEncode_StringTooLong: a length-prefixed field over uint16 fails loudly on
// the sender.
func TestEncode_StringTooLong(t *testing.T) {
	huge := string(make([]byte, 0x10000)) // 65536 > uint16 max
	_, err := EncodeMessage(Close{Reason: huge})
	if !errors.Is(err, ErrStringTooLong) {
		t.Fatalf("err = %v, want ErrStringTooLong", err)
	}
}
