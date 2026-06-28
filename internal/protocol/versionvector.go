package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Ordering is the result of comparing two version vectors (PR-2 §2). The four
// outcomes are total and antisymmetric: Compare(a,b)==Dominates iff
// Compare(b,a)==DominatedBy, Concurrent iff Concurrent, Equal iff Equal.
type Ordering int

const (
	// Equal: identical history (neither side has a strictly greater counter).
	Equal Ordering = iota
	// Dominates: a happened-after b (a is causally newer).
	Dominates
	// DominatedBy: b happened-after a.
	DominatedBy
	// Concurrent: neither dominates — a conflict.
	Concurrent
)

func (o Ordering) String() string {
	switch o {
	case Equal:
		return "Equal"
	case Dominates:
		return "Dominates"
	case DominatedBy:
		return "DominatedBy"
	case Concurrent:
		return "Concurrent"
	default:
		return fmt.Sprintf("Ordering(%d)", int(o))
	}
}

// Counter is one device's authorship count for a file. The id is a ShortID (the
// high 64 bits of a DeviceID).
type Counter struct {
	ID    ShortID
	Value uint64
}

// VersionVector is a per-file causal clock: a device bumps only its own counter,
// and only on a confirmed local change (SR-6). It is stored canonically — sorted
// ascending by ID, with no zero-value counters and no duplicate IDs — so the
// encoding is byte-deterministic (two semantically equal vectors encode
// identically, the substrate for "converged ⇔ equal root", SR-5) and a missing
// entry reads as 0 (the substrate for tombstone dominance, SR-10).
//
// Every operation is copy-on-write: Bump, Merge, and Copy return a fresh backing
// array and never mutate the receiver, so an immutable snapshot can be shared
// under the reconcile RWMutex without aliasing (PR-2 §3, GR-5). See
// docs/audit/decisions/ws0/versionvector-representation-and-cow-ops.md.
type VersionVector []Counter

// ErrMalformedVersionVector is returned by DecodeVersionVector when the wire
// bytes are not in canonical form (truncated, a zero-value counter, or IDs not
// strictly ascending) — rejected at the trust boundary rather than trusted.
var ErrMalformedVersionVector = errors.New("protocol: malformed version vector encoding")

// NewVersionVector builds a canonical VersionVector from a map, dropping
// zero-value counters and sorting ascending by ID. An empty result is nil.
func NewVersionVector(m map[ShortID]uint64) VersionVector {
	if len(m) == 0 {
		return nil
	}
	out := make(VersionVector, 0, len(m))
	for id, v := range m {
		if v == 0 {
			continue // a zero counter is equivalent to absent; never stored
		}
		out = append(out, Counter{ID: id, Value: v})
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns the counter value for id, or 0 if id is absent.
func (vv VersionVector) Get(id ShortID) uint64 {
	i := sort.Search(len(vv), func(i int) bool { return vv[i].ID >= id })
	if i < len(vv) && vv[i].ID == id {
		return vv[i].Value
	}
	return 0
}

// Copy returns a deep copy with its own backing array (nil for an empty vector).
func (vv VersionVector) Copy() VersionVector {
	if len(vv) == 0 {
		return nil
	}
	out := make(VersionVector, len(vv))
	copy(out, vv)
	return out
}

// Bump returns a new vector with self's counter set to prev+1 (a fresh counter
// starts at 1), copy-on-write. The receiver is never mutated. Bump fires only on
// confirmed local authorship, never on applying a received file (SR-6).
func (vv VersionVector) Bump(self ShortID) VersionVector {
	i := sort.Search(len(vv), func(i int) bool { return vv[i].ID >= self })
	if i < len(vv) && vv[i].ID == self {
		out := vv.Copy() // fresh backing array, then mutate the copy only
		out[i].Value++
		return out
	}
	// Insert a new counter at the sorted position, value 1.
	out := make(VersionVector, 0, len(vv)+1)
	out = append(out, vv[:i]...)
	out = append(out, Counter{ID: self, Value: 1})
	out = append(out, vv[i:]...)
	return out
}

// Merge returns the pointwise maximum of the two vectors (copy-on-write). Used
// when accepting an update so the local vector reflects the received history,
// and in the cold-start reseed before a wiped device asserts authorship (PR-2,
// vv-counter-seeding).
func (vv VersionVector) Merge(other VersionVector) VersionVector {
	out := make(VersionVector, 0, len(vv)+len(other))
	i, j := 0, 0
	for i < len(vv) && j < len(other) {
		switch {
		case vv[i].ID < other[j].ID:
			out = append(out, vv[i])
			i++
		case vv[i].ID > other[j].ID:
			out = append(out, other[j])
			j++
		default:
			v := vv[i].Value
			if other[j].Value > v {
				v = other[j].Value
			}
			out = append(out, Counter{ID: vv[i].ID, Value: v})
			i++
			j++
		}
	}
	out = append(out, vv[i:]...)
	out = append(out, other[j:]...)
	if len(out) == 0 {
		return nil
	}
	return out
}

// Compare classifies the causal relationship between vv and other, treating a
// missing entry as 0 (PR-2 §2). It walks the two sorted vectors in lock-step.
func (vv VersionVector) Compare(other VersionVector) Ordering {
	aGreater, bGreater := false, false
	i, j := 0, 0
	for i < len(vv) && j < len(other) {
		switch {
		case vv[i].ID < other[j].ID:
			// vv has this id; other is missing it (=0). No zero values are
			// stored, so vv[i].Value > 0 ⇒ vv is strictly greater here.
			aGreater = true
			i++
		case vv[i].ID > other[j].ID:
			bGreater = true
			j++
		default:
			if vv[i].Value > other[j].Value {
				aGreater = true
			} else if vv[i].Value < other[j].Value {
				bGreater = true
			}
			i++
			j++
		}
		if aGreater && bGreater {
			return Concurrent
		}
	}
	if i < len(vv) {
		aGreater = true // vv has trailing ids (all > 0) other lacks
	}
	if j < len(other) {
		bGreater = true
	}
	switch {
	case !aGreater && !bGreater:
		return Equal
	case aGreater && !bGreater:
		return Dominates
	case !aGreater && bGreater:
		return DominatedBy
	default:
		return Concurrent
	}
}

// IsEqual reports whether the two canonical vectors are element-wise identical
// (equivalent to Compare(other)==Equal, but cheaper).
func (vv VersionVector) IsEqual(other VersionVector) bool {
	if len(vv) != len(other) {
		return false
	}
	for i := range vv {
		if vv[i] != other[i] {
			return false
		}
	}
	return true
}

// Encode serialises the vector as: count (uint16, big-endian) followed by count
// (id uint64, value uint64) pairs in ascending id order. This is the exact
// grammar committed to by the structural hash and the wire FileInfo
// (leaf-shape §D.3); the canonical no-zero/sorted invariant makes it
// deterministic and identical on Mac and Windows. The count fits a uint16 by
// the no-duplicate-id invariant at LAN device scale.
func (vv VersionVector) Encode() []byte {
	buf := make([]byte, 0, 2+len(vv)*16)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(vv)))
	for _, c := range vv {
		buf = binary.BigEndian.AppendUint64(buf, uint64(c.ID))
		buf = binary.BigEndian.AppendUint64(buf, c.Value)
	}
	return buf
}

// DecodeVersionVector parses the Encode grammar from the front of b and returns
// the vector plus the number of bytes consumed (so a composite codec can
// continue). It rejects non-canonical encodings (truncation, zero-value
// counters, or non-ascending ids) with ErrMalformedVersionVector — adversarial
// wire bytes are validated, not trusted.
func DecodeVersionVector(b []byte) (VersionVector, int, error) {
	if len(b) < 2 {
		return nil, 0, fmt.Errorf("%w: need 2 bytes for count, have %d", ErrMalformedVersionVector, len(b))
	}
	count := int(binary.BigEndian.Uint16(b))
	need := 2 + count*16
	if len(b) < need {
		return nil, 0, fmt.Errorf("%w: need %d bytes for %d counters, have %d", ErrMalformedVersionVector, need, count, len(b))
	}
	if count == 0 {
		return nil, need, nil
	}
	out := make(VersionVector, count)
	off := 2
	var prev ShortID
	for i := 0; i < count; i++ {
		id := ShortID(binary.BigEndian.Uint64(b[off:]))
		off += 8
		val := binary.BigEndian.Uint64(b[off:])
		off += 8
		if val == 0 {
			return nil, 0, fmt.Errorf("%w: counter %d has zero value", ErrMalformedVersionVector, i)
		}
		if i > 0 && id <= prev {
			return nil, 0, fmt.Errorf("%w: counter ids not strictly ascending at index %d", ErrMalformedVersionVector, i)
		}
		prev = id
		out[i] = Counter{ID: id, Value: val}
	}
	return out, need, nil
}
