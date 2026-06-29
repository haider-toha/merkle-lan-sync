package merkle

import (
	"errors"
	"reflect"
	"testing"

	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

func mustCanon(t *testing.T, p string) string {
	t.Helper()
	k, err := pathnorm.CanonicalizeSlash(p)
	if err != nil {
		t.Fatalf("canonicalise %q: %v", p, err)
	}
	return k
}

func sampleFileInfo() FileInfo {
	var ch [32]byte
	for i := range ch {
		ch[i] = byte(i * 7)
	}
	return FileInfo{
		Path:        "café/résumé.txt",
		ContentHash: ch,
		Size:        4096,
		Mode:        0o755,
		ModTimeNS:   1_700_000_000_123_456_789,
		Type:        TypeFile,
		Deleted:     false,
		Version:     protocol.NewVersionVector(map[protocol.ShortID]uint64{3: 2, 9: 5}),
	}
}

// TestWireFileInfo_RoundTrip — wire-fileinfo-grammar: a FileInfo survives
// Encode->Decode byte-for-byte (the path comes back canonical NFC).
func TestWireFileInfo_RoundTrip(t *testing.T) {
	cases := []FileInfo{
		sampleFileInfo(),
		func() FileInfo { fi := sampleFileInfo(); fi.Type = TypeSymlink; return fi }(),
		func() FileInfo { fi := sampleFileInfo(); fi.Deleted = true; fi.ContentHash = [32]byte{}; return fi }(),
		{Path: "empty-vv.bin", Type: TypeFile}, // nil VV
	}
	for _, in := range cases {
		t.Run(in.Path, func(t *testing.T) {
			in.Path = mustCanon(t, in.Path) // hold the canonical form so decode matches
			enc, err := EncodeFileInfo(in)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, n, err := DecodeFileInfo(enc)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(enc) {
				t.Errorf("consumed %d bytes, want %d", n, len(enc))
			}
			if !reflect.DeepEqual(got, in) {
				t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, in)
			}
		})
	}
}

// TestWireFileInfo_TruncationRejected: every truncated prefix of a valid encoding is
// rejected with ErrMalformedFileInfo and never panics.
func TestWireFileInfo_TruncationRejected(t *testing.T) {
	enc, err := EncodeFileInfo(sampleFileInfo())
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(enc); n++ {
		_, _, err := DecodeFileInfo(enc[:n])
		if err == nil {
			t.Errorf("truncation to %d bytes was accepted", n)
		}
	}
}

func TestDecodeFileInfo_RejectsBadFileType(t *testing.T) {
	fi := sampleFileInfo()
	enc, _ := EncodeFileInfo(fi)
	// fileType byte sits right after path(2+len) + hash(32) + size(8) + mode(4) + mtime(8).
	ftOff := 2 + len(fi.Path) + 32 + 8 + 4 + 8
	bad := append([]byte{}, enc...)
	bad[ftOff] = 9 // out of range
	if _, _, err := DecodeFileInfo(bad); !errors.Is(err, ErrMalformedFileInfo) {
		t.Errorf("bad fileType err = %v, want ErrMalformedFileInfo", err)
	}
}

// TestDecodeFileInfo_RejectsTraversalPath: a peer advertising a "../escape" path is
// rejected on decode (path-traversal-received-path antipattern). EncodeFileInfo only
// length-checks the path, so we can encode a hostile one and assert decode refuses.
func TestDecodeFileInfo_RejectsTraversalPath(t *testing.T) {
	for _, bad := range []string{"../etc/passwd", "/abs/path", "a/../../b"} {
		fi := sampleFileInfo()
		fi.Path = bad
		enc, err := EncodeFileInfo(fi)
		if err != nil {
			t.Fatalf("encode %q: %v", bad, err)
		}
		if _, _, err := DecodeFileInfo(enc); !errors.Is(err, ErrMalformedFileInfo) {
			t.Errorf("path %q decode err = %v, want ErrMalformedFileInfo", bad, err)
		}
	}
}

// TestWireFileInfos_RoundTrip — the count-prefixed set (INDEX body) round-trips and
// the decoded count matches.
func TestWireFileInfos_RoundTrip(t *testing.T) {
	set := []FileInfo{
		leaf("a/b.txt", "one"),
		leaf("c.bin", "two"),
		func() FileInfo { fi := leaf("d.txt", "three"); return fi.SetDeleted(2) }(),
	}
	enc, err := EncodeFileInfos(set)
	if err != nil {
		t.Fatalf("encode set: %v", err)
	}
	got, err := DecodeFileInfos(enc)
	if err != nil {
		t.Fatalf("decode set: %v", err)
	}
	if !reflect.DeepEqual(got, set) {
		t.Errorf("set round-trip mismatch:\n got %+v\nwant %+v", got, set)
	}
}

func TestDecodeFileInfos_TrailingGarbageRejected(t *testing.T) {
	enc, _ := EncodeFileInfos([]FileInfo{leaf("a.txt", "x")})
	enc = append(enc, 0xFF) // trailing byte
	if _, err := DecodeFileInfos(enc); !errors.Is(err, ErrMalformedFileInfo) {
		t.Errorf("trailing garbage err = %v, want ErrMalformedFileInfo", err)
	}
}

func TestDecodeFileInfos_CountMismatchRejected(t *testing.T) {
	enc, _ := EncodeFileInfos([]FileInfo{leaf("a.txt", "x")})
	// Bump the count prefix to claim 2 entries when only 1 follows.
	enc[3] = 2
	if _, err := DecodeFileInfos(enc); !errors.Is(err, ErrMalformedFileInfo) {
		t.Errorf("count mismatch err = %v, want ErrMalformedFileInfo", err)
	}
}
