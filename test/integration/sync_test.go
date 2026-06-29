package integration

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// WS-4 #1: two divergent instances converge to identical root hashes (at quiescence).
func TestTwoNode_Converge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	// Diverge the two folders before either engine starts (the startup scan sees them).
	write(t, dirA, "docs/a.txt", "alpha")
	write(t, dirA, "shared.txt", "from-A")
	write(t, dirB, "docs/b.txt", "bravo")

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	connect(t, a, b)
	waitConverged(t, a, b, 15*time.Second)

	// Every file is present on both sides with the right bytes (no loss, no mangling).
	for _, tc := range []struct{ rel, want string }{
		{"docs/a.txt", "alpha"},
		{"shared.txt", "from-A"},
		{"docs/b.txt", "bravo"},
	} {
		for name, n := range map[string]*node{"A": a, "B": b} {
			got, ok := read(t, n.dir, tc.rel)
			if !ok || got != tc.want {
				t.Fatalf("node %s: %q = %q (ok=%v), want %q", name, tc.rel, got, ok, tc.want)
			}
		}
	}
}

// WS-4 #2: simultaneous edits to one file produce a .sync-conflict copy with neither
// version lost, and the copy filename is byte-identical on both peers.
func TestConflict_NeitherVersionLostSymmetricName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := startNode(t, ctx, t.TempDir())
	b := startNode(t, ctx, t.TempDir())
	emptyA, emptyB := a.eng.RootHash(), b.eng.RootHash()

	// Author DIFFERENT content for the same path on each node WHILE DISCONNECTED, so
	// neither edit causally precedes the other (a genuine Concurrent conflict).
	const xA, yB = "content-from-A", "content-from-B-different"
	write(t, a.dir, "f.txt", xA)
	write(t, b.dir, "f.txt", yB)
	waitRootChanged(t, a, emptyA, 5*time.Second)
	waitRootChanged(t, b, emptyB, 5*time.Second)

	connect(t, a, b)
	waitConverged(t, a, b, 20*time.Second)

	// Both peers must hold BOTH versions: f.txt (the winner) + exactly one identically
	// named .sync-conflict copy (the loser). Neither byte-set is lost.
	copiesA := conflictCopies(t, a.dir, ".")
	copiesB := conflictCopies(t, b.dir, ".")
	if len(copiesA) != 1 || len(copiesB) != 1 {
		t.Fatalf("want exactly one conflict copy per node, got A=%v B=%v", copiesA, copiesB)
	}
	if copiesA[0] != copiesB[0] {
		t.Fatalf("conflict-copy filename differs between peers: A=%q B=%q", copiesA[0], copiesB[0])
	}

	for name, n := range map[string]*node{"A": a, "B": b} {
		winner, _ := read(t, n.dir, "f.txt")
		loser, _ := read(t, n.dir, copiesA[0])
		got := []string{winner, loser}
		sort.Strings(got)
		want := []string{xA, yB}
		sort.Strings(want)
		if got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("node %s lost a version: have %v, want both of %v", name, got, want)
		}
	}
}

// WS-4 #6: a deletion propagates and a stale peer cannot resurrect it. B holds the
// pre-delete file and never saw the delete; on reconnect the tombstone Dominates B's
// stale version ⇒ B deletes locally and the file is NOT re-created on the deleter A.
func TestDeletion_NoResurrection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	write(t, dirA, "doomed.txt", "data")
	write(t, dirB, "doomed.txt", "data") // B independently holds the same file

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	withFile := a.eng.RootHash()

	// A deletes the file while DISCONNECTED from B (B is the soon-to-be-stale peer).
	if err := os.Remove(filepath.Join(dirA, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	waitRootChanged(t, a, withFile, 5*time.Second) // A's rescan synthesises the tombstone

	connect(t, a, b)
	waitConverged(t, a, b, 15*time.Second)

	if _, ok := read(t, a.dir, "doomed.txt"); ok {
		t.Fatal("file resurrected on the deleter A")
	}
	if _, ok := read(t, b.dir, "doomed.txt"); ok {
		t.Fatal("stale peer B did not apply the deletion")
	}
}

// WS-4 #11: two instances doing simultaneous large transfers in BOTH directions
// converge within a timeout — a hang would be the back-pressure deadlock. Stop-and-
// wait pull bounds the outbound queue so neither side wedges (CDD-1).
func TestBackpressure_BidirectionalConverges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	bigA := randomBytes(2<<20, 1) // 2 MiB, distinct per side
	bigB := randomBytes(2<<20, 2)
	if err := os.WriteFile(filepath.Join(dirA, "bigA.bin"), bigA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "bigB.bin"), bigB, 0o644); err != nil {
		t.Fatal(err)
	}

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	connect(t, a, b)
	waitConverged(t, a, b, 40*time.Second)

	// Both large files crossed in both directions, byte-exact (verify-after-reconstruct).
	if got, _ := read(t, b.dir, "bigA.bin"); got != string(bigA) {
		t.Fatal("bigA.bin did not transfer A->B intact")
	}
	if got, _ := read(t, a.dir, "bigB.bin"); got != string(bigB) {
		t.Fatal("bigB.bin did not transfer B->A intact")
	}
}

func randomBytes(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	_, _ = r.Read(b)
	return b
}
