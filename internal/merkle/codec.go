package merkle

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// ErrMalformedFileInfo is returned when wire FileInfo bytes are truncated, carry an
// out-of-range field, or hold a non-canonical path/VV — rejected at the trust
// boundary rather than trusted (GR-7/GR-8).
var ErrMalformedFileInfo = errors.New("merkle: malformed wire FileInfo encoding")

// ----------------------------------------------------------------------------
// Structural encodings (input to the domain-separated structural hash, node.go).
// Recipe: structural-hash-grammar-finalization decision (folds in leaf-shape §D.3
// + the XP-6 2-state mode). All integers big-endian. Identical on Mac and Windows.
// ----------------------------------------------------------------------------

// leafEncoding is the structural body for a file/symlink/tombstone leaf:
//
//	content_hash[32] || modeByte:uint8 || deleted:uint8 || vvEncoding
//
// The NAME is deliberately excluded — it is committed once by the parent's child
// entry (git model), preserving identical-content dedup. Raw mode, mtime, and size
// are excluded (non-portable / volatile / redundant).
func leafEncoding(fi FileInfo) []byte {
	vv := fi.Version.Encode() // uint16 count + sorted (id,value) pairs
	buf := make([]byte, 0, 32+1+1+len(vv))
	buf = append(buf, fi.ContentHash[:]...)
	buf = append(buf, fi.canonicalModeByte())
	if fi.Deleted {
		buf = append(buf, 0x01)
	} else {
		buf = append(buf, 0x00)
	}
	buf = append(buf, vv...)
	return buf
}

// childEntry is one (name, structuralHash) pair in a directory node encoding.
type childEntry struct {
	name string
	hash [32]byte
}

// nodeEncoding is the structural body for a directory node:
//
//	childCount:uint32 || childCount x ( nameLen:uint16 || nameBytes || childHash[32] )
//
// children MUST already be sorted ascending by bytewise compare of nameBytes
// (node.go sorts before calling). n-ary, never duplicate-last (CVE-2012-2459, MK-1).
func nodeEncoding(children []childEntry) []byte {
	buf := make([]byte, 0, 4+len(children)*(2+16+32))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(children)))
	for _, c := range children {
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(c.name)))
		buf = append(buf, c.name...)
		buf = append(buf, c.hash[:]...)
	}
	return buf
}

// ----------------------------------------------------------------------------
// Wire FileInfo grammar (INDEX / INDEX_UPDATE body — wire-fileinfo-grammar decision).
// Carries the FULL FileInfo (incl. the fields the structural hash omits) so a peer
// can place, pre-filter, tiebreak, and apply. Bounds-checked decode.
// ----------------------------------------------------------------------------

// EncodeFileInfo appends the wire encoding of fi:
//
//	pathLen:uint16 || pathBytes || content_hash[32] || size:uint64 || mode:uint32
//	|| mtime:int64 || fileType:uint8 || deleted:uint8 || vvEncoding
func EncodeFileInfo(fi FileInfo) ([]byte, error) {
	if len(fi.Path) > 0xFFFF {
		return nil, fmt.Errorf("%w: path exceeds uint16 length", ErrMalformedFileInfo)
	}
	buf := make([]byte, 0, 2+len(fi.Path)+32+8+4+8+1+1+2)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(fi.Path)))
	buf = append(buf, fi.Path...)
	buf = append(buf, fi.ContentHash[:]...)
	buf = binary.BigEndian.AppendUint64(buf, fi.Size)
	buf = binary.BigEndian.AppendUint32(buf, fi.Mode)
	buf = binary.BigEndian.AppendUint64(buf, uint64(fi.ModTimeNS))
	buf = append(buf, byte(fi.Type))
	if fi.Deleted {
		buf = append(buf, 0x01)
	} else {
		buf = append(buf, 0x00)
	}
	buf = append(buf, fi.Version.Encode()...)
	return buf, nil
}

// DecodeFileInfo parses one wire FileInfo from the front of b, returning it and the
// number of bytes consumed. It validates lengths, the fileType range, the deleted
// byte, and re-canonicalises the path (rejecting absolute/traversal); the VV decoder
// rejects a non-canonical vector. Adversarial input yields ErrMalformedFileInfo,
// never a panic or over-read.
func DecodeFileInfo(b []byte) (FileInfo, int, error) {
	var fi FileInfo
	r := reader{b: b}

	pathLen := int(r.u16())
	pathBytes := r.take(pathLen)
	fi.ContentHash = r.array32()
	fi.Size = r.u64()
	fi.Mode = r.u32()
	fi.ModTimeNS = int64(r.u64())
	ft := r.u8()
	del := r.u8()
	if r.err != nil {
		return FileInfo{}, 0, r.err
	}

	if ft > byte(TypeSymlink) {
		return FileInfo{}, 0, fmt.Errorf("%w: fileType 0x%02x out of range", ErrMalformedFileInfo, ft)
	}
	if del > 1 {
		return FileInfo{}, 0, fmt.Errorf("%w: deleted byte 0x%02x not boolean", ErrMalformedFileInfo, del)
	}
	vv, n, err := protocol.DecodeVersionVector(b[r.off:])
	if err != nil {
		return FileInfo{}, 0, fmt.Errorf("%w: %v", ErrMalformedFileInfo, err)
	}
	r.off += n

	key, err := pathnorm.CanonicalizeSlash(string(pathBytes))
	if err != nil {
		return FileInfo{}, 0, fmt.Errorf("%w: %v", ErrMalformedFileInfo, err)
	}
	fi.Path = key
	fi.Type = FileType(ft)
	fi.Deleted = del == 1
	fi.Version = vv
	return fi, r.off, nil
}

// EncodeFileInfos appends count:uint32 followed by count wire FileInfos. This is
// exactly the INDEX/INDEX_UPDATE Body (the WS-0 envelope carries the same count).
func EncodeFileInfos(set []FileInfo) ([]byte, error) {
	buf := make([]byte, 0, 4+len(set)*64)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(set)))
	for _, fi := range set {
		enc, err := EncodeFileInfo(fi)
		if err != nil {
			return nil, err
		}
		buf = append(buf, enc...)
	}
	return buf, nil
}

// DecodeFileInfos parses the count-prefixed set EncodeFileInfos produced. It
// requires the bytes to be exactly consumed (no trailing garbage) and the decoded
// count to match the prefix.
func DecodeFileInfos(b []byte) ([]FileInfo, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("%w: need 4 bytes for count, have %d", ErrMalformedFileInfo, len(b))
	}
	count := int(binary.BigEndian.Uint32(b))
	off := 4
	out := make([]FileInfo, 0, count)
	for i := 0; i < count; i++ {
		fi, n, err := DecodeFileInfo(b[off:])
		if err != nil {
			return nil, fmt.Errorf("%w (entry %d)", err, i)
		}
		off += n
		out = append(out, fi)
	}
	if off != len(b) {
		return nil, fmt.Errorf("%w: %d trailing bytes after %d entries", ErrMalformedFileInfo, len(b)-off, count)
	}
	return out, nil
}

// reader is a small bounds-checked big-endian decoder with a sticky error, so a
// truncated wire FileInfo yields ErrMalformedFileInfo instead of a panic.
type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) fail() {
	if r.err == nil {
		r.err = ErrMalformedFileInfo
	}
}
func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.off+n > len(r.b) {
		r.fail()
		return nil
	}
	v := r.b[r.off : r.off+n]
	r.off += n
	return v
}
func (r *reader) u8() byte {
	v := r.take(1)
	if v == nil {
		return 0
	}
	return v[0]
}
func (r *reader) u16() uint16 {
	v := r.take(2)
	if v == nil {
		return 0
	}
	return binary.BigEndian.Uint16(v)
}
func (r *reader) u32() uint32 {
	v := r.take(4)
	if v == nil {
		return 0
	}
	return binary.BigEndian.Uint32(v)
}
func (r *reader) u64() uint64 {
	v := r.take(8)
	if v == nil {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}
func (r *reader) array32() [32]byte {
	var a [32]byte
	copy(a[:], r.take(32))
	return a
}
