package protocol

import (
	"encoding/hex"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// vvOf builds a canonical VersionVector from id:value pairs (test helper).
func vvOf(pairs map[ShortID]uint64) VersionVector { return NewVersionVector(pairs) }

func TestCompare_Cases(t *testing.T) {
	cases := []struct {
		name string
		a, b VersionVector
		want Ordering
	}{
		{"both empty", nil, nil, Equal},
		{"equal single", vvOf(map[ShortID]uint64{1: 1}), vvOf(map[ShortID]uint64{1: 1}), Equal},
		{"a dominates by value", vvOf(map[ShortID]uint64{1: 2}), vvOf(map[ShortID]uint64{1: 1}), Dominates},
		{"a dominated by value", vvOf(map[ShortID]uint64{1: 1}), vvOf(map[ShortID]uint64{1: 2}), DominatedBy},
		{"empty dominated by nonempty", nil, vvOf(map[ShortID]uint64{1: 1}), DominatedBy},
		{"nonempty dominates empty", vvOf(map[ShortID]uint64{1: 1}), nil, Dominates},
		{"a has extra id ⇒ dominates", vvOf(map[ShortID]uint64{1: 1, 2: 2}), vvOf(map[ShortID]uint64{1: 1}), Dominates},
		{"disjoint ids ⇒ concurrent", vvOf(map[ShortID]uint64{1: 1}), vvOf(map[ShortID]uint64{2: 1}), Concurrent},
		{"crossing values ⇒ concurrent", vvOf(map[ShortID]uint64{1: 2, 2: 1}), vvOf(map[ShortID]uint64{1: 1, 2: 2}), Concurrent},
		// SR-10 substrate: a tombstone whose deleter (id 2) bumped its counter
		// Dominates a stale peer whose id-2 counter is absent (=0).
		{"tombstone dominates stale absent", vvOf(map[ShortID]uint64{1: 1, 2: 5}), vvOf(map[ShortID]uint64{1: 1}), Dominates},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Compare(tc.b); got != tc.want {
				t.Errorf("Compare(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func reverseOrdering(o Ordering) Ordering {
	switch o {
	case Dominates:
		return DominatedBy
	case DominatedBy:
		return Dominates
	default:
		return o // Equal, Concurrent are self-dual
	}
}

// TestCompare_Antisymmetry: Compare(a,b) and Compare(b,a) are duals for every
// pair (PR-2 §7) — the property that lets both peers independently reach the same
// classification of every differing leaf.
func TestCompare_Antisymmetry(t *testing.T) {
	// Named pairs.
	pairs := []struct{ a, b VersionVector }{
		{nil, nil},
		{vvOf(map[ShortID]uint64{1: 1}), vvOf(map[ShortID]uint64{1: 2})},
		{vvOf(map[ShortID]uint64{1: 1}), vvOf(map[ShortID]uint64{2: 1})},
		{vvOf(map[ShortID]uint64{1: 2, 2: 1}), vvOf(map[ShortID]uint64{1: 1, 2: 2})},
	}
	for _, p := range pairs {
		ab, ba := p.a.Compare(p.b), p.b.Compare(p.a)
		if ba != reverseOrdering(ab) {
			t.Errorf("antisymmetry: Compare(a,b)=%v but Compare(b,a)=%v (want %v)", ab, ba, reverseOrdering(ab))
		}
	}

	// Randomised property over a small id/value space (deterministic seed).
	rng := rand.New(rand.NewSource(0x5EED))
	randVV := func() VersionVector {
		m := map[ShortID]uint64{}
		for id := ShortID(1); id <= 3; id++ {
			if v := uint64(rng.Intn(4)); v > 0 { // 0 ⇒ absent (no zero counters)
				m[id] = v
			}
		}
		return NewVersionVector(m)
	}
	for i := 0; i < 5000; i++ {
		a, b := randVV(), randVV()
		ab, ba := a.Compare(b), b.Compare(a)
		if ba != reverseOrdering(ab) {
			t.Fatalf("antisymmetry violated: a=%v b=%v Compare(a,b)=%v Compare(b,a)=%v", a, b, ab, ba)
		}
		// Equal ⟺ identical canonical encoding.
		if (ab == Equal) != a.IsEqual(b) {
			t.Fatalf("Equal/IsEqual disagree: a=%v b=%v ab=%v IsEqual=%v", a, b, ab, a.IsEqual(b))
		}
	}
}

func TestMerge_PointwiseMax(t *testing.T) {
	cases := []struct {
		name string
		a, b VersionVector
		want VersionVector
	}{
		{"overlap and disjoint",
			vvOf(map[ShortID]uint64{1: 2, 2: 1}),
			vvOf(map[ShortID]uint64{1: 1, 2: 3, 3: 5}),
			vvOf(map[ShortID]uint64{1: 2, 2: 3, 3: 5})},
		{"empty left", nil, vvOf(map[ShortID]uint64{1: 1}), vvOf(map[ShortID]uint64{1: 1})},
		{"empty right", vvOf(map[ShortID]uint64{1: 5}), nil, vvOf(map[ShortID]uint64{1: 5})},
		{"both empty", nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.a.Merge(tc.b)
			if !got.IsEqual(tc.want) {
				t.Errorf("Merge = %v, want %v", got, tc.want)
			}
			// Merge is commutative on the result.
			if rev := tc.b.Merge(tc.a); !rev.IsEqual(tc.want) {
				t.Errorf("Merge not commutative: b.Merge(a) = %v, want %v", rev, tc.want)
			}
		})
	}
}

func TestBump_PrevPlusOne(t *testing.T) {
	cases := []struct {
		name string
		in   VersionVector
		self ShortID
		want VersionVector
	}{
		{"fresh counter starts at 1", nil, 3, vvOf(map[ShortID]uint64{3: 1})},
		{"existing increments", vvOf(map[ShortID]uint64{3: 1}), 3, vvOf(map[ShortID]uint64{3: 2})},
		{"insert in middle stays sorted", vvOf(map[ShortID]uint64{1: 1, 5: 1}), 3, vvOf(map[ShortID]uint64{1: 1, 3: 1, 5: 1})},
		{"insert at front", vvOf(map[ShortID]uint64{5: 1}), 1, vvOf(map[ShortID]uint64{1: 1, 5: 1})},
		{"insert at end", vvOf(map[ShortID]uint64{1: 1}), 5, vvOf(map[ShortID]uint64{1: 1, 5: 1})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Bump(tc.self); !got.IsEqual(tc.want) {
				t.Errorf("Bump(%d) = %v, want %v", tc.self, got, tc.want)
			}
		})
	}
}

// TestOps_CopyOnWrite: Bump and Merge return fresh backing arrays and never
// mutate the receiver (PR-2 §3, GR-5). Mutating the result must not touch the
// original.
func TestOps_CopyOnWrite(t *testing.T) {
	t.Run("bump existing leaves receiver untouched", func(t *testing.T) {
		orig := vvOf(map[ShortID]uint64{1: 1})
		snap := orig.Copy()
		bumped := orig.Bump(1)
		if !orig.IsEqual(snap) {
			t.Fatalf("receiver changed after Bump: %v, want %v", orig, snap)
		}
		bumped[0].Value = 999 // mutate the result's backing array
		if orig[0].Value != 1 {
			t.Fatalf("mutating Bump result aliased the receiver's backing array: orig=%v", orig)
		}
	})
	t.Run("bump insert leaves receiver untouched", func(t *testing.T) {
		orig := vvOf(map[ShortID]uint64{2: 1})
		snap := orig.Copy()
		_ = orig.Bump(1)
		if !orig.IsEqual(snap) {
			t.Fatalf("receiver changed after insert Bump: %v, want %v", orig, snap)
		}
	})
	t.Run("merge leaves both operands untouched", func(t *testing.T) {
		a := vvOf(map[ShortID]uint64{1: 1})
		b := vvOf(map[ShortID]uint64{1: 5, 2: 2})
		as, bs := a.Copy(), b.Copy()
		merged := a.Merge(b)
		if !a.IsEqual(as) || !b.IsEqual(bs) {
			t.Fatalf("Merge mutated an operand: a=%v (want %v) b=%v (want %v)", a, as, b, bs)
		}
		merged[0].Value = 777
		if a[0].Value != 1 {
			t.Fatalf("mutating Merge result aliased operand a: %v", a)
		}
	})
}

// TestOps_CopyOnWrite_Race shares one vector across goroutines that read it
// (Compare/Get) while others derive new vectors (Bump/Merge). Under -race this
// passing is the assertion that COW ops never mutate the shared receiver.
func TestOps_CopyOnWrite_Race(t *testing.T) {
	shared := vvOf(map[ShortID]uint64{1: 1, 2: 2})
	snap := shared.Copy()
	other := vvOf(map[ShortID]uint64{2: 5, 3: 1})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				_ = shared.Compare(other)
				_ = shared.Get(1)
				_ = shared.IsEqual(other)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id ShortID) {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				_ = shared.Bump(id)
				_ = shared.Merge(other)
			}
		}(ShortID(i))
	}
	wg.Wait()
	if !shared.IsEqual(snap) {
		t.Fatalf("shared vector mutated under concurrent COW ops: %v, want %v", shared, snap)
	}
}

// TestEncode_GoldenVector pins the exact wire bytes (R-1: a non-deterministic or
// non-big-endian encoding poisons every downstream hash). Counters {1:2, 5:3}:
// u16 count=2, then (id u64, value u64) ascending.
func TestEncode_GoldenVector(t *testing.T) {
	vv := vvOf(map[ShortID]uint64{1: 2, 5: 3})
	const want = "0002" +
		"0000000000000001" + "0000000000000002" +
		"0000000000000005" + "0000000000000003"
	if got := hex.EncodeToString(vv.Encode()); got != want {
		t.Fatalf("Encode = %s, want %s", got, want)
	}

	// Empty vector encodes to just a zero count.
	if got := hex.EncodeToString(VersionVector(nil).Encode()); got != "0000" {
		t.Fatalf("empty Encode = %s, want 0000", got)
	}

	// Round-trip via DecodeVersionVector, with bytes consumed reported.
	dec, n, err := DecodeVersionVector(vv.Encode())
	if err != nil {
		t.Fatalf("DecodeVersionVector: %v", err)
	}
	if n != 2+2*16 {
		t.Fatalf("consumed = %d, want %d", n, 2+2*16)
	}
	if !dec.IsEqual(vv) {
		t.Fatalf("round-trip = %v, want %v", dec, vv)
	}
}

// TestDecode_TrailingBytes: DecodeVersionVector consumes only its own region and
// reports the offset so a composite codec (the WS-1 wireFileInfo) can continue.
func TestDecode_TrailingBytes(t *testing.T) {
	vv := vvOf(map[ShortID]uint64{9: 4})
	buf := append(vv.Encode(), 0xAA, 0xBB) // trailing bytes belonging to a larger record
	dec, n, err := DecodeVersionVector(buf)
	if err != nil {
		t.Fatalf("DecodeVersionVector: %v", err)
	}
	if !dec.IsEqual(vv) || n != 2+16 {
		t.Fatalf("dec=%v n=%d, want %v n=%d", dec, n, vv, 2+16)
	}
}

func TestNewVersionVector_Normalize(t *testing.T) {
	// Zero values dropped; result sorted ascending by id.
	got := NewVersionVector(map[ShortID]uint64{5: 1, 1: 2, 9: 0, 3: 0})
	want := vvOf(map[ShortID]uint64{1: 2, 5: 1})
	if !got.IsEqual(want) {
		t.Fatalf("normalize = %v, want %v", got, want)
	}
	// All-zero map ⇒ nil.
	if got := NewVersionVector(map[ShortID]uint64{1: 0, 2: 0}); got != nil {
		t.Fatalf("all-zero normalize = %v, want nil", got)
	}
}

func TestGet(t *testing.T) {
	vv := vvOf(map[ShortID]uint64{1: 5, 9: 2})
	for _, tc := range []struct {
		id   ShortID
		want uint64
	}{{1, 5}, {9, 2}, {4, 0}, {0, 0}, {100, 0}} {
		if got := vv.Get(tc.id); got != tc.want {
			t.Errorf("Get(%d) = %d, want %d", tc.id, got, tc.want)
		}
	}
	if got := VersionVector(nil).Get(1); got != 0 {
		t.Errorf("empty Get = %d, want 0", got)
	}
}

func TestDecodeVersionVector_RejectsMalformed(t *testing.T) {
	counter := func(id, val uint64) []byte {
		b := make([]byte, 16)
		for i := 0; i < 8; i++ {
			b[7-i] = byte(id >> (8 * i))
			b[15-i] = byte(val >> (8 * i))
		}
		return b
	}
	count := func(n uint16) []byte { return []byte{byte(n >> 8), byte(n)} }

	cases := []struct {
		name string
		in   []byte
	}{
		{"truncated count", []byte{0x00}},
		{"count claims 1 but no counter bytes", count(1)},
		{"count claims 1 but only partial counter", append(count(1), 0x00, 0x00)},
		{"zero-value counter", append(count(1), counter(7, 0)...)},
		{"non-ascending ids", append(count(2), append(counter(5, 1), counter(3, 1)...)...)},
		{"duplicate ids", append(count(2), append(counter(5, 1), counter(5, 1)...)...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeVersionVector(tc.in)
			if !errors.Is(err, ErrMalformedVersionVector) {
				t.Fatalf("err = %v, want ErrMalformedVersionVector", err)
			}
		})
	}

	// A well-formed encoding decodes cleanly.
	good := append(count(1), counter(7, 4)...)
	dec, n, err := DecodeVersionVector(good)
	if err != nil || n != 18 || !dec.IsEqual(vvOf(map[ShortID]uint64{7: 4})) {
		t.Fatalf("good decode: dec=%v n=%d err=%v", dec, n, err)
	}
}
