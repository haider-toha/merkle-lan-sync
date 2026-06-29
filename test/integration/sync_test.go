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

// PR-3 (Phase 7, skeptic #2 §1): a concurrent delete-vs-modification where the DELETE
// WINS the tiebreak must still preserve the losing modification as a .sync-conflict copy
// on BOTH peers (SR-7/SR-9, finding §5) — no data loss. Delete-wins is forced
// deterministically: a tombstone inherits the deleted file's mtime, so giving A's file a
// FAR-FUTURE mtime before the delete makes A's tombstone beat B's now-stamped edit. The
// loser-custodian (B) must copy its edit to the conflict path BEFORE the winning delete
// removes it; pre-fix the synchronous tombstone os.Remove destroyed it first (the bug).
func TestConflict_DeleteVsModify_NoLossBothPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	write(t, dirA, "f.txt", "v1")
	write(t, dirB, "f.txt", "v1")

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

	// Far-future mtime on A's file; wait for A's rescan to record it (a quiet mtime-only
	// update — no rebroadcast, so B stays at its own now-stamped version).
	future := time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dirA, "f.txt"), future, future); err != nil {
		t.Fatal(err)
	}
	waitRecordedMtimeAtLeast(t, a, "f.txt", time.Date(2199, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano(), budgetAuthor)

	// Stop A (snapshot now carries the future mtime), delete on A's disk while down, and
	// CONCURRENTLY edit f.txt on the still-running B.
	stop(t, a)
	if err := os.Remove(filepath.Join(dirA, "f.txt")); err != nil {
		t.Fatal(err)
	}
	const edited = "v2-from-B-the-losing-modification"
	bBefore := b.eng.RootHash()
	write(t, b.dir, "f.txt", edited)
	waitRootChanged(t, b, bBefore, budgetAuthor)

	a = restartNode(t, ctx, a) // startupReconcile synthesizes the future-dated tombstone
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

	// Delete won ⇒ the losing modification survives as a recoverable copy on BOTH peers.
	for name, n := range map[string]*node{"A": a, "B": b} {
		if !hasContentSomewhere(t, n.dir, edited) {
			t.Fatalf("node %s LOST the losing modification %q (delete-wins must keep a conflict copy)", name, edited)
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

// MK-6 / R-5 named acceptance test (deletion-across-restart): create+sync on A and B,
// STOP A, delete the file on A's disk WHILE A IS DOWN, RESTART A. A's live watcher/rescan
// never observed the delete — only the startup snapshot diff (SynthesizeDeletions) can
// recover it. On reconnect the synthesized tombstone propagates: B removes its copy and
// the file is NOT resurrected on A. This is the cross-peer, cross-restart behaviour the
// unit tests (SynthesizeDeletions/restoreVVs/snapshot round-trip) cannot prove on their
// own (skeptic #1 §1 / skeptic #3 §1).
func TestRestart_SynthesizesDeletionFromSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	write(t, dirA, "doomed.txt", "data")
	write(t, dirB, "doomed.txt", "data") // B independently holds the same file

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)
	if _, ok := read(t, a.dir, "doomed.txt"); !ok {
		t.Fatal("precondition: A should hold doomed.txt before stop (so its snapshot has it)")
	}

	// Stop A (shutdown persists the snapshot containing doomed.txt), then delete on disk.
	stop(t, a)
	if err := os.Remove(filepath.Join(dirA, "doomed.txt")); err != nil {
		t.Fatal(err)
	}

	// Restart over the SAME folder + identity + snapshot ⇒ startupReconcile diffs the
	// snapshot (has doomed.txt) against the scan (absent) and synthesizes the tombstone.
	a = restartNode(t, ctx, a)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

	if _, ok := read(t, a.dir, "doomed.txt"); ok {
		t.Fatal("file resurrected on the restarted deleter A")
	}
	if _, ok := read(t, b.dir, "doomed.txt"); ok {
		t.Fatal("stale peer B did not apply the deletion synthesized from A's snapshot")
	}
}

// PR-4 obligation #5 (skeptic #2 §2): a NOT-YET-ACKED tombstone — authored while running,
// retained because no peer has acknowledged it, persisted to the snapshot, then RELOADED
// unchanged at restart — must survive the restart and still dominate a stale peer through
// the full reconcile/broadcast path, with no resurrection. This is distinct from
// TestRestart_SynthesizesDeletionFromSnapshot, which mints a FRESH tombstone at restart
// from a delete-while-down: here the tombstone already exists in the snapshot and is
// carried forward by SynthesizeDeletions, exercising the "snapshot stores tombstones so a
// restart doesn't forget a not-yet-acked deletion" path end-to-end (finding §7, OQ-5).
func TestRestart_PendingTombstoneSurvivesAndNoResurrection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	write(t, dirA, "doomed.txt", "data") // A authors it; B will RECEIVE it (so B descends from A {A:1})

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)
	if _, ok := read(t, b.dir, "doomed.txt"); !ok {
		t.Fatal("precondition: B should have received doomed.txt before the delete (so it is a true stale pre-delete holder)")
	}

	// Stop B FIRST so A deletes with NO peer connected ⇒ the tombstone is RETAINED
	// (canGC keeps it when no peer is known) — i.e. it stays a not-yet-acked pending delete.
	stop(t, b)
	withFile := a.eng.RootHash()
	if err := os.Remove(filepath.Join(dirA, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	waitRootChanged(t, a, withFile, budgetAuthor) // A's rescan tombstones it {A:2}; retained (no peer)

	// Stop A: its shutdown persists the snapshot CONTAINING the pending (un-acked) tombstone.
	stop(t, a)

	// Restart BOTH over the same folders/identities/snapshots. A loads the pending tombstone
	// (SynthesizeDeletions carries it forward unchanged); B loads the stale live doomed.txt {A:1}.
	a = restartNode(t, ctx, a)
	b = restartNode(t, ctx, b)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

	if _, ok := read(t, a.dir, "doomed.txt"); ok {
		t.Fatal("pending tombstone did not survive the restart — file resurrected on the deleter A")
	}
	if _, ok := read(t, b.dir, "doomed.txt"); ok {
		t.Fatal("stale peer B did not apply the reloaded pending tombstone (resurrection)")
	}
}

// MK-6 recreate-over-tombstone across a restart (skeptic #1 §2): A's persisted snapshot
// holds a tombstone for a path, the file is RECREATED on A's disk while A is down, then A
// restarts. The recreate must come back with a version vector that DOMINATES the persisted
// tombstone, so a peer still holding a tombstone ADOPTS the recreate instead of re-deleting
// it. Under the pre-fix code the recreate kept an empty VV and was DominatedBy the peer's
// tombstone ⇒ silently re-deleted (loss of the local re-creation).
//
// To make the precondition deterministic we delete the file INDEPENDENTLY on each node
// while they are DISCONNECTED: with no peer, neither node GCs its tombstone (the ack-gated
// GC retains when no peer is known), so A's snapshot truly retains its tombstone and B
// holds a concurrent tombstone whose non-empty VV would dominate an empty-VV recreate. (A
// delete performed while connected would be GC'd off both sides once acked, collapsing the
// case to a cold-start reseed and NOT exercising restoreVVs.)
func TestRestart_RecreateOverTombstoneSurvives(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	write(t, dirA, "doomed.txt", "v1")
	write(t, dirB, "doomed.txt", "v1")

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB) // started but never connected to a until the end

	aWithFile, bWithFile := a.eng.RootHash(), b.eng.RootHash()
	if err := os.Remove(filepath.Join(dirA, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dirB, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	waitRootChanged(t, a, aWithFile, budgetAuthor) // A authors + retains its tombstone (no peer ⇒ no GC)
	waitRootChanged(t, b, bWithFile, budgetAuthor) // B authors + retains a concurrent tombstone

	// Stop A (snapshot now holds A's tombstone), recreate the file on A's disk WHILE DOWN,
	// then restart.
	stop(t, a)
	const recreated = "recreated-while-down"
	if err := os.WriteFile(filepath.Join(dirA, "doomed.txt"), []byte(recreated), 0o644); err != nil {
		t.Fatal(err)
	}
	a = restartNode(t, ctx, a)

	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

	// The recreate must survive on BOTH peers — as the live file, or (if the concurrent
	// tombstone happens to win the mtime tiebreak) as a .sync-conflict copy. It must NOT be
	// silently re-deleted. hasContentSomewhere is robust to which side wins the tiebreak.
	for name, n := range map[string]*node{"A": a, "B": b} {
		if !hasContentSomewhere(t, n.dir, recreated) {
			t.Fatalf("node %s: recreate %q was re-deleted across the restart (data loss)", name, recreated)
		}
	}
}

// MK-6 delete-while-down vs a concurrent remote edit (skeptic #3 §3): A deletes a file
// while down; concurrently B edits the SAME file. A's synthesized tombstone is Concurrent
// with B's edited version (a delete-vs-edit conflict). The data-loss-free outcome is
// modify-beats-delete (Syncthing-style): B's edited bytes must survive on BOTH peers —
// as the live file when the edit wins, or as a .sync-conflict copy when a stale tombstone
// mtime wins (the byte-holder mints the copy). The assertion checks the bytes are present
// SOMEWHERE on both peers, so it is immune to the conflict-winner mtime/ShortID ordering.
func TestRestart_DeleteWhileDownVsRemoteEdit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dirA, dirB := t.TempDir(), t.TempDir()
	write(t, dirA, "f.txt", "v1")
	write(t, dirB, "f.txt", "v1")

	a := startNode(t, ctx, dirA)
	b := startNode(t, ctx, dirB)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

	// Stop A, then CONCURRENTLY: delete f.txt on A's disk (recovered via the snapshot diff
	// at restart) and edit f.txt on the still-running B (a genuine concurrent authorship).
	stop(t, a)
	if err := os.Remove(filepath.Join(dirA, "f.txt")); err != nil {
		t.Fatal(err)
	}
	const edited = "v2-from-B"
	bBefore := b.eng.RootHash()
	write(t, b.dir, "f.txt", edited)
	waitRootChanged(t, b, bBefore, budgetAuthor) // B authored its edit while A was down

	a = restartNode(t, ctx, a)
	connect(t, a, b)
	waitConverged(t, a, b, budgetConverge)

	// No data loss: B's edited bytes are recoverable on BOTH peers (live file or copy).
	for name, n := range map[string]*node{"A": a, "B": b} {
		if !hasContentSomewhere(t, n.dir, edited) {
			t.Fatalf("node %s lost B's concurrent edit %q across the delete-while-down conflict", name, edited)
		}
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
