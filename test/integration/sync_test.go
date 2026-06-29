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
	waitConverged(t, a, b, budgetConverge)

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
	waitRootChanged(t, a, emptyA, budgetAuthor)
	waitRootChanged(t, b, emptyB, budgetAuthor)

	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

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
	waitRootChanged(t, a, withFile, budgetAuthor) // A's rescan synthesises the tombstone

	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

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

	// Large static files: relax the rescan (no mid-transfer re-hash churn on the loop)
	// and give the per-chunk request generous headroom (de-flake decision).
	a := startNode(t, ctx, dirA, withRescan(time.Second), withRequestTimeout(20*time.Second))
	b := startNode(t, ctx, dirB, withRescan(time.Second), withRequestTimeout(20*time.Second))
	connect(t, a, b)
	waitConverged(t, a, b, budgetLarge)

	// Both large files crossed in both directions, byte-exact (verify-after-reconstruct).
	if got, _ := read(t, b.dir, "bigA.bin"); got != string(bigA) {
		t.Fatal("bigA.bin did not transfer A->B intact")
	}
	if got, _ := read(t, a.dir, "bigB.bin"); got != string(bigB) {
		t.Fatal("bigB.bin did not transfer B->A intact")
	}
}

// WS-4 #8 / PR-5: a rename propagates as create-new + delete-old with NO data loss —
// both peers converge to the new path holding the original bytes, with the old path
// gone on both sides. (The zero-network-transfer property of a rename — the new path
// reuses the still-present old bytes — is proven separately by the unit test
// internal/reconcile TestRename_NoNetworkTransfer; here the integration invariant is
// convergence + no-loss across two live engines.)
func TestRename_PropagatesNoLoss(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	const payload = "rename-payload-0123456789-keep-every-byte"
	write(t, dirA, "old.txt", payload)

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)
	if got, ok := read(t, b.dir, "old.txt"); !ok || got != payload {
		t.Fatalf("precondition: B should hold old.txt=%q, got %q ok=%v", payload, got, ok)
	}

	// Rename on A while connected; A's rescan emits create(new.txt)+delete(old.txt),
	// ordered creates-before-deletes so a peer never transiently loses the only copy.
	if err := os.Rename(filepath.Join(dirA, "old.txt"), filepath.Join(dirA, "new.txt")); err != nil {
		t.Fatal(err)
	}
	waitConverged(t, a, b, budgetConverge)

	for name, n := range map[string]*node{"A": a, "B": b} {
		if got, ok := read(t, n.dir, "new.txt"); !ok || got != payload {
			t.Fatalf("node %s: new.txt=%q ok=%v, want %q (rename lost bytes)", name, got, ok, payload)
		}
		if _, ok := read(t, n.dir, "old.txt"); ok {
			t.Fatalf("node %s: old.txt still present after rename (stale copy not removed)", name)
		}
	}
}

// WS-4 #3 / SR-1 / SR-2: a transfer severed mid-stream leaves NO corrupt or partial
// destination file and NO leftover temp on the receiver (verify-before-rename), and
// recovers byte-exact on reconnect. B pulls a 4 MiB file from A through a loopback
// proxy that cuts the wire after 96 KiB — past TLS+HELLO+INDEX, deep inside the
// 128-block chunk stream (decisions/phase6/killed-transfer-fault-injection.md).
func TestKilledTransfer_NoCorruptFileThenRecovers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	big := randomBytes(4<<20, 7) // 4 MiB ⇒ 128 blocks; a single early cut leaves most untransferred
	if err := os.WriteFile(filepath.Join(dirA, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	a := startNode(t, ctx, dirA, withRescan(time.Second), withRequestTimeout(20*time.Second)) // source
	b := startNode(t, ctx, dirB, withRescan(time.Second), withRequestTimeout(20*time.Second)) // receiver / puller
	a.tp.Allowlist().Add(b.id.DeviceID)
	b.tp.Allowlist().Add(a.id.DeviceID)

	proxy := startCutProxy(t, a.addr, 96*1024)
	if err := b.tp.Dial("tcp", proxy.addr()); err != nil {
		t.Fatalf("dial via proxy: %v", err)
	}

	select {
	case <-proxy.cut:
	case <-time.After(20 * time.Second):
		t.Fatal("transfer never reached the mid-stream cut threshold")
	}

	// SR-1 triad (a)+(b): on the receiver the dst is absent (never partial) and no temp
	// lingers. Poll a short settle window for the aborted puller to discard its temp.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, dstThere := read(t, b.dir, "big.bin")
		temps := tempFiles(t, b.dir)
		if !dstThere && len(temps) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after kill: big.bin present=%v leftover temps=%v (want absent + none)", dstThere, temps)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// SR-1 triad (c): reconnect directly (bypassing the dead proxy) ⇒ the transfer
	// completes and the file is byte-exact.
	if err := b.tp.Dial("tcp", a.addr); err != nil {
		t.Fatalf("reconnect dial: %v", err)
	}
	waitConverged(t, a, b, budgetLarge)
	if got, _ := read(t, b.dir, "big.bin"); got != string(big) {
		t.Fatal("big.bin did not recover byte-exact after reconnect")
	}
}

func randomBytes(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	_, _ = r.Read(b)
	return b
}
