package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haider-toha/merkle-sync/internal/merkle"
	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// NOTE ON HOSTILE INPUT. Wherever a path is involved these tests fold in the
// Windows-hostile set — reserved device names (CON), reserved chars / NTFS ADS
// (a:b), trailing dot/space, backslash-in-component, NFD vs NFC, and case
// collisions (File.txt vs file.txt). resolve/conflictName are asserted to preserve
// the canonical key and to survive a Mac->Windows->Mac round-trip; the no-clobber
// refusal is exercised on the Mac's case-insensitive APFS (the NTFS $UpCase matrix
// is a Phase-6 windows-latest item, CDD-5).

// ---------- helpers ----------

func devID(seed byte) protocol.DeviceID {
	var d protocol.DeviceID
	for i := range d {
		d[i] = seed + byte(i)
	}
	return d
}

func vv(pairs ...uint64) protocol.VersionVector {
	m := make(map[protocol.ShortID]uint64)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[protocol.ShortID(pairs[i])] = pairs[i+1]
	}
	return protocol.NewVersionVector(m)
}

func liveFI(path, content string, mtimeNS int64, version protocol.VersionVector) merkle.FileInfo {
	return merkle.FileInfo{
		Path:        path,
		ContentHash: merkle.HashBytes([]byte(content)),
		Size:        uint64(len(content)),
		ModTimeNS:   mtimeNS,
		Version:     version,
		Type:        merkle.TypeFile,
	}
}

func tombFI(path string, mtimeNS int64, version protocol.VersionVector) merkle.FileInfo {
	return merkle.FileInfo{Path: path, ModTimeNS: mtimeNS, Version: version, Deleted: true, Type: merkle.TypeFile}
}

// tempEngine builds an Engine over a fresh temp dir with a background runCtx (so the
// puller's report() never blocks), no network, fast intervals.
func tempEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Config{
		FolderID:       "t",
		AbsRoot:        t.TempDir(),
		Self:           devID(0x10),
		RescanInterval: time.Hour, // tests drive rescan explicitly
		RequestTimeout: 2 * time.Second,
		Logf:           t.Logf,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.runCtx = context.Background()
	return e
}

func writeFile(t *testing.T, e *Engine, key, content string) {
	t.Helper()
	osPath := pathnorm.ToOSPath(e.absRoot, key, pathnorm.HostTarget())
	if err := os.MkdirAll(filepath.Dir(osPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(osPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", key, err)
	}
}

// fakeConn is a recording peerConn for engine-level tests without the network.
type fakeConn struct {
	id   protocol.DeviceID
	hi   protocol.Hello
	done chan struct{}
	mu   sync.Mutex
	sent []protocol.Message
}

func newFakeConn(seed byte) *fakeConn {
	return &fakeConn{id: devID(seed), done: make(chan struct{})}
}
func (c *fakeConn) DeviceID() protocol.DeviceID { return c.id }
func (c *fakeConn) Hello() protocol.Hello       { return c.hi }
func (c *fakeConn) Done() <-chan struct{}       { return c.done }
func (c *fakeConn) Send(m protocol.Message) bool {
	c.mu.Lock()
	c.sent = append(c.sent, m)
	c.mu.Unlock()
	return true
}
func (c *fakeConn) count(tp protocol.MsgType) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.sent {
		if m.Type() == tp {
			n++
		}
	}
	return n
}

func (e *Engine) registerFakePeer(fc *fakeConn) *peerState {
	ps := &peerState{
		conn:     fc,
		short:    fc.id.Short(),
		index:    make(map[string]merkle.FileInfo),
		inflight: make(map[string]bool),
		fetchQ:   make(chan fetchTask, 256),
		serveQ:   make(chan protocol.Request, 64),
		resp:     make(map[uint32]chan protocol.Response),
	}
	e.mu.Lock()
	e.peers[fc.id] = ps
	e.mu.Unlock()
	return ps
}

// ---------- WS-4 #5: resolver totality over Compare x content ----------

func TestResolver_Matrix(t *testing.T) {
	x := "xxxx"
	y := "yyyy"
	cases := []struct {
		name   string
		local  *merkle.FileInfo
		remote *merkle.FileInfo
		want   planKind
	}{
		{"local-only", ptr(liveFI("a", x, 1, vv(1, 1))), nil, planNoOp},
		{"remote-only-live → fetch", nil, ptr(liveFI("a", x, 1, vv(2, 1))), planInstall},
		{"remote-only-tombstone → noop (unknown tombstone, CDD-3)", nil, ptr(tombFI("a", 1, vv(2, 1))), planNoOp},
		{"equal VV + equal content → noop (SR-3)", ptr(liveFI("a", x, 1, vv(1, 1))), ptr(liveFI("a", x, 9, vv(1, 1))), planNoOp},
		{"equal VV + diff content → conflict (backstop, CDD-3)", ptr(liveFI("a", x, 1, vv(1, 1))), ptr(liveFI("a", y, 2, vv(1, 1))), planConflict},
		{"dominates → noop", ptr(liveFI("a", x, 1, vv(1, 2))), ptr(liveFI("a", y, 1, vv(1, 1))), planNoOp},
		{"dominated-by live → fetch", ptr(liveFI("a", x, 1, vv(1, 1))), ptr(liveFI("a", y, 1, vv(1, 2))), planInstall},
		{"dominated-by tombstone → apply tombstone", ptr(liveFI("a", x, 1, vv(1, 1))), ptr(tombFI("a", 1, vv(1, 2))), planInstall},
		{"concurrent + equal content → mergeVV (CDD-3)", ptr(liveFI("a", x, 1, vv(1, 1))), ptr(liveFI("a", x, 1, vv(2, 1))), planInstall},
		{"concurrent + diff content → conflict (SR-7)", ptr(liveFI("a", x, 1, vv(1, 1))), ptr(liveFI("a", y, 2, vv(2, 1))), planConflict},
		{"concurrent both tombstone → mergeVV", ptr(tombFI("a", 1, vv(1, 1))), ptr(tombFI("a", 1, vv(2, 1))), planInstall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolve(tc.local, tc.remote, 1)
			if got.kind != tc.want {
				t.Fatalf("kind = %v, want %v (flag %q)", got.kind, tc.want, got.flag)
			}
		})
	}
}

func TestResolver_ConcurrentEqualMerges(t *testing.T) {
	local := liveFI("a", "same", 1, vv(1, 1))
	remote := liveFI("a", "same", 1, vv(2, 1))
	p := resolve(&local, &remote, 1)
	if p.kind != planInstall || p.install.Deleted {
		t.Fatalf("want mergeVV install, got %v", p.kind)
	}
	if p.install.Version.Compare(vv(1, 1, 2, 1)) != protocol.Equal {
		t.Fatalf("merged VV = %v, want {1:1,2:1}", p.install.Version)
	}
}

func TestResolver_EqualVVDiffContentConflicts(t *testing.T) {
	local := liveFI("a", "left", 5, vv(1, 1))
	remote := liveFI("a", "right", 9, vv(1, 1)) // SAME VV, different bytes
	if p := resolve(&local, &remote, 1); p.kind != planConflict {
		t.Fatalf("equal-VV differing-content must conflict (never silent overwrite), got %v", p.kind)
	}
}

func TestResolver_UnknownTombstoneNoOp(t *testing.T) {
	remote := tombFI("ghost", 1, vv(2, 5))
	p := resolve(nil, &remote, 1)
	if p.kind != planNoOp {
		t.Fatalf("advertised tombstone for an unknown path must be a no-op (no re-mint), got %v", p.kind)
	}
}

// Windows-hostile paths flow through resolve unchanged (it is path-agnostic).
func TestResolver_PreservesHostilePaths(t *testing.T) {
	for _, hostile := range []string{"dir/CON", "weird/a:b", "x/résumé.pdf", "trail/name ", "back/a\\b"} {
		remote := liveFI(hostile, "data", 1, vv(2, 1))
		p := resolve(nil, &remote, 1)
		if p.kind != planInstall || p.install.Path != hostile {
			t.Fatalf("hostile path %q not preserved: kind=%v path=%q", hostile, p.kind, p.install.Path)
		}
	}
}

func ptr(fi merkle.FileInfo) *merkle.FileInfo { return &fi }

// ---------- WS-4 #2: conflict winner is total + commutative; copy name deterministic ----------

func TestW_Commutative(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		a := randFI(r, "a")
		b := randFI(r, "b") // distinct content (distinct path content seeds distinct hashes)
		// Antisymmetry: for distinct content, exactly one wins.
		if aWins(a, b) == aWins(b, a) {
			t.Fatalf("not antisymmetric: aWins both ways equal for a=%+v b=%+v", a, b)
		}
		// Commutative winner: both argument orders pick the same physical content.
		if winner(a, b).ContentHash != winner(b, a).ContentHash {
			t.Fatalf("winner not commutative for a/b")
		}
	}
}

func randFI(r *rand.Rand, tag string) merkle.FileInfo {
	content := tag + string(rune('A'+r.Intn(26))) + string(rune('a'+r.Intn(26)))
	return liveFI("f", content, int64(r.Intn(3)), vv(uint64(1+r.Intn(3)), uint64(1+r.Intn(3))))
}

func TestConflict_CopyNameDeterministic(t *testing.T) {
	// Equal mtime so the tiebreak falls to author; both peers compute the same loser
	// (larger ShortID) and the same UTC-truncated timestamp ⇒ byte-identical name.
	loser := liveFI("docs/résumé.txt", "mine", 1_700_000_000_500_000_000, vv(7, 3))
	winr := liveFI("docs/résumé.txt", "theirs", 1_700_000_000_500_000_000, vv(3, 3))

	n1, err := conflictName(loser, loserAuthor(loser, winr))
	if err != nil {
		t.Fatalf("conflictName: %v", err)
	}
	// Recompute "on the other peer" (argument order swapped everywhere): identical.
	n2, err := conflictName(loserOf(winr, loser), loserAuthor(loserOf(winr, loser), winner(winr, loser)))
	if err != nil {
		t.Fatalf("conflictName 2: %v", err)
	}
	if n1 != n2 {
		t.Fatalf("conflict name not symmetric: %q vs %q", n1, n2)
	}
	if !strings.Contains(n1, ".sync-conflict-") || !strings.HasSuffix(n1, ".txt") {
		t.Fatalf("unexpected conflict name shape: %q", n1)
	}
	// UTC truncation to whole seconds: the nanosecond part never appears.
	ts := time.Unix(0, loser.ModTimeNS).UTC().Truncate(time.Second).Format("20060102-150405")
	if !strings.Contains(n1, ts) {
		t.Fatalf("name %q missing UTC-truncated timestamp %q", n1, ts)
	}
}

// TZ-independence: the same loser yields the same name regardless of $TZ (UTC).
func TestConflict_CopyNameTZIndependent(t *testing.T) {
	loser := liveFI("a/b.txt", "x", 1_700_000_123_000_000_000, vv(9, 2))
	t.Setenv("TZ", "America/Los_Angeles")
	a, _ := conflictName(loser, loserAuthor(loser, liveFI("a/b.txt", "y", 1, vv(1, 1))))
	t.Setenv("TZ", "Asia/Kolkata")
	b, _ := conflictName(loser, loserAuthor(loser, liveFI("a/b.txt", "y", 1, vv(1, 1))))
	if a != b {
		t.Fatalf("conflict name depends on TZ: %q vs %q", a, b)
	}
}

// A conflict copy of a Windows-hostile name is itself a canonical key that survives
// Mac->Windows->Mac (SR-13, XP-3) — the conflict-copy path is never an un-writable name.
func TestConflict_CopyNameWindowsRoundTrips(t *testing.T) {
	for _, hostile := range []string{"CON.txt", "a:b.dat", "deep/dir/résumé.pdf"} {
		loser := liveFI(hostile, "mine", 1_700_000_000_000_000_000, vv(5, 1))
		name, err := conflictName(loser, loserAuthor(loser, liveFI(hostile, "theirs", 1, vv(1, 1))))
		if err != nil {
			t.Fatalf("conflictName(%q): %v", hostile, err)
		}
		const root = "/sync"
		osWin := pathnorm.ToOSPath(root, name, pathnorm.Windows)
		back, err := pathnorm.FromOSPath(root, osWin, pathnorm.Windows)
		if err != nil {
			t.Fatalf("round-trip %q via %q: %v", name, osWin, err)
		}
		if back != name {
			t.Fatalf("conflict name did not round-trip Mac->Windows->Mac: %q -> %q -> %q", name, osWin, back)
		}
		if strings.ContainsAny(filepath.Base(osWin), `:*?"<>|`) {
			t.Fatalf("on-disk Windows conflict name still has a reserved char: %q", osWin)
		}
	}
}

// ---------- WS-4 #9: REQUEST validation + clean decline ----------

func TestValidateRequest(t *testing.T) {
	const size = 100
	cases := []struct {
		name string
		req  protocol.Request
		ok   bool
	}{
		{"valid head", protocol.Request{Offset: 0, Length: 32}, true},
		{"valid tail", protocol.Request{Offset: 90, Length: 10}, true},
		{"zero length", protocol.Request{Offset: 0, Length: 0}, false},
		{"over MaxChunkLen", protocol.Request{Offset: 0, Length: protocol.MaxChunkLen + 1}, false},
		{"offset past size", protocol.Request{Offset: 200, Length: 1}, false},
		{"range past size", protocol.Request{Offset: 90, Length: 20}, false},
		{"exact whole file", protocol.Request{Offset: 0, Length: size}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRequest(tc.req, size); got != tc.ok {
				t.Fatalf("validateRequest = %v, want %v", got, tc.ok)
			}
		})
	}
}

func TestServeRequest_OversizeDeclinedConnSurvives(t *testing.T) {
	e := tempEngine(t)
	writeFile(t, e, "f.txt", "hello world")
	fc := newFakeConn(0x20)
	ps := e.registerFakePeer(fc)

	// Oversize length is declined cleanly with GENERIC; the conn is not torn down.
	e.serveRequest(ps, protocol.Request{ReqID: 1, Path: "f.txt", Offset: 0, Length: protocol.MaxFrameLen})
	// A subsequent VALID request on the same conn is served OK — proving survival.
	e.serveRequest(ps, protocol.Request{ReqID: 2, Path: "f.txt", Offset: 0, Length: 5})

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if len(fc.sent) != 2 {
		t.Fatalf("want 2 responses, got %d", len(fc.sent))
	}
	r1 := fc.sent[0].(protocol.Response)
	if r1.Code != protocol.CodeGeneric || len(r1.Data) != 0 {
		t.Fatalf("oversize REQUEST not declined cleanly: %+v", r1)
	}
	r2 := fc.sent[1].(protocol.Response)
	if r2.Code != protocol.CodeOK || string(r2.Data) != "hello" {
		t.Fatalf("valid REQUEST after decline not served: %+v", r2)
	}
}

func TestServeRequest_MissingFileDeclined(t *testing.T) {
	e := tempEngine(t)
	fc := newFakeConn(0x21)
	ps := e.registerFakePeer(fc)
	e.serveRequest(ps, protocol.Request{ReqID: 1, Path: "nope.txt", Offset: 0, Length: 1})
	r := fc.sent[0].(protocol.Response)
	if r.Code != protocol.CodeNoSuchFile {
		t.Fatalf("missing file: want CodeNoSuchFile, got %v", r.Code)
	}
}

// ---------- WS-4 #3: killed transfer leaves no corrupt file (atomic write + verify) ----------

func TestNumBlocks(t *testing.T) {
	cases := map[uint64]int{0: 0, 1: 1, BlockSize: 1, BlockSize + 1: 2, 3 * BlockSize: 3, 3*BlockSize - 1: 3}
	for size, want := range cases {
		if got := numBlocks(size); got != want {
			t.Fatalf("numBlocks(%d) = %d, want %d", size, got, want)
		}
	}
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".msync-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestAtomicWriteVerify_KillMidStreamLeavesDstUntouched(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(dst, []byte("OLD-COMPLETE"), 0o644); err != nil {
		t.Fatal(err)
	}
	expected := merkle.HashBytes([]byte("NEW-COMPLETE"))
	err := atomicWriteVerify(dst, expected, func(w io.Writer) error {
		_, _ = w.Write([]byte("NEW-")) // partial write...
		return errors.New("killed mid-stream")
	})
	if err == nil {
		t.Fatal("expected error from killed fill")
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "OLD-COMPLETE" {
		t.Fatalf("dst corrupted by killed transfer: %q", got)
	}
	noTempFiles(t, dir)
}

func TestAtomicWriteVerify_HashMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "f.txt")
	os.WriteFile(dst, []byte("OLD"), 0o644)
	expected := merkle.HashBytes([]byte("WANT"))
	err := atomicWriteVerify(dst, expected, func(w io.Writer) error {
		_, _ = w.Write([]byte("CORRUPT-DIFFERENT")) // wrong bytes ⇒ hash mismatch
		return nil
	})
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("want ErrVerifyFailed, got %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "OLD" {
		t.Fatalf("dst replaced despite verify failure: %q", got)
	}
	noTempFiles(t, dir)
}

func TestAtomicWriteVerify_SuccessAndReRunCompletes(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "sub", "f.txt") // also exercises parent mkdir
	want := "the-real-bytes"
	fill := func(w io.Writer) error { _, e := w.Write([]byte(want)); return e }
	if err := atomicWriteVerify(dst, merkle.HashBytes([]byte(want)), fill); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	// Re-run (redelivery) is harmless and still yields the same bytes.
	if err := atomicWriteVerify(dst, merkle.HashBytes([]byte(want)), fill); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	noTempFiles(t, filepath.Dir(dst))
}

// ---------- WS-4 #4: received file ⇒ zero outbound broadcasts; genuine edit ⇒ one ----------

func TestApply_ZeroOutboundBroadcasts(t *testing.T) {
	e := tempEngine(t)
	fc := newFakeConn(0x30)
	_ = e.registerFakePeer(fc)

	// Simulate a completed apply: the file is on disk and recorded, with expected-hash.
	writeFile(t, e, "f.txt", "applied")
	applied := liveFI("f.txt", "applied", 100, vv(2, 1)) // authored by the PEER
	e.mu.Lock()
	e.files["f.txt"] = applied
	e.expected["f.txt"] = applied.ContentHash
	e.rebuildLocked()
	e.mu.Unlock()

	// The watcher fires on our own atomic write — must NOT be treated as authorship.
	e.onLocalChange("f.txt")
	if n := fc.count(protocol.MsgIndexUpdate); n != 0 {
		t.Fatalf("apply echo produced %d outbound INDEX_UPDATE, want 0 (SR-6/SR-8)", n)
	}

	// A GENUINE local edit (different bytes) during/after the apply window IS detected.
	writeFile(t, e, "f.txt", "edited-by-user")
	e.onLocalChange("f.txt")
	if n := fc.count(protocol.MsgIndexUpdate); n != 1 {
		t.Fatalf("genuine edit produced %d INDEX_UPDATE, want exactly 1", n)
	}
	// And the broadcast bumped OUR counter on top of the peer's history.
	e.mu.RLock()
	got := e.files["f.txt"].Version
	e.mu.RUnlock()
	if got.Get(e.selfShort) == 0 {
		t.Fatalf("local edit did not bump our counter: %v", got)
	}
}

func TestApply_IdempotentRedelivery(t *testing.T) {
	e := tempEngine(t)
	fc := newFakeConn(0x31)
	ps := e.registerFakePeer(fc)

	writeFile(t, e, "f.txt", "data")
	leaf := liveFI("f.txt", "data", 1, vv(2, 1))
	e.mu.Lock()
	e.files["f.txt"] = leaf
	e.mu.Unlock()

	// Redelivery: materialise a leaf whose content already matches on disk ⇒ no
	// network REQUEST, no rewrite — a literal no-op (SR-3).
	e.materialise(context.Background(), ps, leaf, false)
	select {
	case c := <-e.completions:
		if !c.ok {
			t.Fatalf("idempotent materialise reported failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no completion")
	}
	if n := fc.count(protocol.MsgRequest); n != 0 {
		t.Fatalf("idempotent apply sent %d REQUESTs, want 0", n)
	}
}

// ---------- WS-4 #8: rename = zero network transfer via local content-addressed reuse ----------

func TestRename_NoNetworkTransfer(t *testing.T) {
	e := tempEngine(t)
	fc := newFakeConn(0x40)
	ps := e.registerFakePeer(fc)

	// The "old" file still on disk + recorded; its bytes are the rename source.
	writeFile(t, e, "old.txt", "the-payload-bytes")
	old := liveFI("old.txt", "the-payload-bytes", 1, vv(2, 1))
	e.mu.Lock()
	e.files["old.txt"] = old
	e.mu.Unlock()

	// Materialise the SAME content at a NEW path (the rename target). It must be
	// satisfied by local content-addressed reuse — zero network REQUEST (PR-5/MK-4).
	newLeaf := liveFI("new.txt", "the-payload-bytes", 1, vv(2, 1))
	e.materialise(context.Background(), ps, newLeaf, false)
	select {
	case c := <-e.completions:
		if !c.ok {
			t.Fatal("rename materialise failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no completion")
	}
	if n := fc.count(protocol.MsgRequest); n != 0 {
		t.Fatalf("rename cost %d network REQUESTs, want 0 (local reuse)", n)
	}
	osNew := pathnorm.ToOSPath(e.absRoot, "new.txt", pathnorm.HostTarget())
	if got, _ := os.ReadFile(osNew); string(got) != "the-payload-bytes" {
		t.Fatalf("new path content = %q", got)
	}
}

// ---------- WS-4 #7: no-clobber by the filesystem's own verdict (APFS case; NTFS → Phase 6) ----------

func TestApply_RefusesCaseClobber(t *testing.T) {
	e := tempEngine(t)
	if e.caseSensitive {
		t.Skip("case-sensitive filesystem: case keys coexist, clobber refusal applies to insensitive targets (NTFS/APFS) — Phase 6")
	}
	fc := newFakeConn(0x50)
	ps := e.registerFakePeer(fc)

	// An existing file occupies the case-folded slot.
	writeFile(t, e, "File.txt", "keep-me")
	e.mu.Lock()
	e.files["File.txt"] = liveFI("File.txt", "keep-me", 1, vv(1, 1))
	e.mu.Unlock()

	// Materialising a DIFFERENT canonical key that folds equal must be refused, not
	// clobber the existing file (CDD-5, XP-4).
	e.materialise(context.Background(), ps, liveFI("file.txt", "would-clobber", 2, vv(2, 1)), false)
	select {
	case c := <-e.completions:
		if c.ok || !c.clobber {
			t.Fatalf("expected a refused clobber, got ok=%v clobber=%v", c.ok, c.clobber)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no completion")
	}
	// The original file's bytes are intact (nothing clobbered).
	osPath := pathnorm.ToOSPath(e.absRoot, "File.txt", pathnorm.HostTarget())
	if got, _ := os.ReadFile(osPath); string(got) != "keep-me" {
		t.Fatalf("existing file was clobbered: %q", got)
	}
}

// ---------- MK-2 (Phase 7): file-vs-directory type clash is refused + flagged ----------

// TestReconcile_RefusesFileVsDirTypeClash — when the local tree has a FILE at a path
// the peer holds as a DIRECTORY (or vice versa), reconcileWithPeer must REFUSE: it
// flags the clash, enqueues NO fetch, produces NO completion, and leaves local data
// untouched — never feeding the differ's directory side to resolve as a false absence
// (which previously livelocked on an impossible mkdir/rename, MK-2 refutation). Both
// directions are exercised. (decisions/phase7/MK-2-file-vs-dir-typeclash-resolution.md)
func TestReconcile_RefusesFileVsDirTypeClash(t *testing.T) {
	cases := []struct {
		name        string
		localKey    string // a file we hold locally (on disk + in e.files)
		peerKey     string // what the peer advertises in its index
		untouched   string // the local on-disk path that must survive unchanged
		wantContent string // its expected bytes afterwards
		wantLogText string // the flag the engine must surface
	}{
		{
			name:        "local-file-vs-peer-dir",
			localKey:    "foo",         // local: foo is a FILE
			peerKey:     "foo/bar.txt", // peer: foo is a DIRECTORY
			untouched:   "foo",
			wantContent: "my-file-bytes",
			wantLogText: "is a FILE locally but a DIRECTORY on the peer",
		},
		{
			name:        "local-dir-vs-peer-file",
			localKey:    "foo/bar.txt", // local: foo is a DIRECTORY
			peerKey:     "foo",         // peer: foo is a FILE
			untouched:   "foo/bar.txt",
			wantContent: "my-dir-child-bytes",
			wantLogText: "is a DIRECTORY locally but a FILE on the peer",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := tempEngine(t)
			var (
				logMu   sync.Mutex
				logs    []string
				clashed bool
			)
			e.logf = func(format string, args ...any) {
				logMu.Lock()
				defer logMu.Unlock()
				line := fmt.Sprintf(format, args...)
				logs = append(logs, line)
				if strings.Contains(line, ErrTypeClash.Error()) {
					clashed = true
				}
			}

			// Local side: the file exists on disk AND in the recorded state.
			writeFile(t, e, tc.untouched, tc.wantContent)
			e.mu.Lock()
			e.files[tc.localKey] = liveFI(tc.localKey, tc.wantContent, 1, vv(1, 1))
			e.expected[tc.localKey] = merkle.HashBytes([]byte(tc.wantContent))
			e.mu.Unlock()

			// Peer side: advertises the clashing shape.
			fc := newFakeConn(0x70)
			ps := e.registerFakePeer(fc)
			ps.index[tc.peerKey] = liveFI(tc.peerKey, "peer-bytes", 2, vv(2, 1))

			e.reconcileWithPeer(ps)

			// No impossible install was enqueued, and nothing is in flight.
			if n := len(ps.fetchQ); n != 0 {
				t.Errorf("a fetch was enqueued for a type clash (%d queued) — must refuse, not attempt", n)
			}
			if n := len(ps.inflight); n != 0 {
				t.Errorf("inflight is non-empty (%d) after a refused clash", n)
			}
			// No completion was produced (no retry-livelock fuel).
			select {
			case c := <-e.completions:
				t.Fatalf("a completion was produced for a refused clash: %+v", c)
			default:
			}
			// The clash was flagged loudly (ErrTypeClash, with the direction + path).
			logMu.Lock()
			defer logMu.Unlock()
			if !clashed {
				t.Errorf("clash was not flagged with %v; logs=%v", ErrTypeClash, logs)
			}
			foundDir := false
			for _, l := range logs {
				if strings.Contains(l, tc.wantLogText) && strings.Contains(l, "foo") {
					foundDir = true
				}
			}
			if !foundDir {
				t.Errorf("expected a flag log %q mentioning the path; logs=%v", tc.wantLogText, logs)
			}
			// No data loss: the local file is byte-for-byte intact.
			osPath := pathnorm.ToOSPath(e.absRoot, tc.untouched, pathnorm.HostTarget())
			if got, _ := os.ReadFile(osPath); string(got) != tc.wantContent {
				t.Errorf("local data changed by a refused clash: got %q want %q", got, tc.wantContent)
			}
		})
	}
}

// ---------- WS-4 #10: rescan is the source of truth; debounce coalesces ----------

func TestRescan_RecoversDroppedEvent(t *testing.T) {
	e := tempEngine(t)
	fc := newFakeConn(0x60)
	_ = e.registerFakePeer(fc)

	// A file appears with NO watcher event delivered (simulating a dropped event).
	writeFile(t, e, "late.txt", "appeared-silently")
	e.rescan() // the periodic full scan is the safety net (SR-11)

	e.mu.RLock()
	fi, ok := e.files["late.txt"]
	e.mu.RUnlock()
	if !ok || fi.Deleted {
		t.Fatal("rescan did not detect the silently-created file")
	}
	if fi.Version.Get(e.selfShort) == 0 {
		t.Fatal("rescan-detected create was not stamped as local authorship")
	}
	if n := fc.count(protocol.MsgIndexUpdate); n != 1 {
		t.Fatalf("rescan create broadcast %d updates, want 1", n)
	}
}

func TestRescan_DetectsRenameAsDeleteCreate(t *testing.T) {
	e := tempEngine(t)
	// Seed a/one.txt as a known file.
	writeFile(t, e, "a/one.txt", "payload")
	e.rescan() // picks up a/one.txt as a create

	// Rename a/ -> b/ on disk (the directory subtree reparents).
	if err := os.Rename(filepath.Join(e.absRoot, "a"), filepath.Join(e.absRoot, "b")); err != nil {
		t.Fatal(err)
	}
	e.rescan()

	e.mu.RLock()
	defer e.mu.RUnlock()
	oldFI, hadOld := e.files["a/one.txt"]
	newFI, hadNew := e.files["b/one.txt"]
	if !hadOld || !oldFI.Deleted {
		t.Fatal("old path not tombstoned after rename")
	}
	if !hadNew || newFI.Deleted {
		t.Fatal("new path not created after rename")
	}
	if newFI.ContentHash != merkle.HashBytes([]byte("payload")) {
		t.Fatal("reparented file lost its content hash")
	}
}

func TestDebounce_CoalescesBurst(t *testing.T) {
	in := make(chan string, 64)
	out := make(chan string, 64)
	clk := &manualClock{}
	clk.set(time.Unix(1000, 0))
	tick := make(chan time.Time)

	d := &debouncer{
		window: 150 * time.Millisecond,
		in:     in,
		emit:   func(k string) { out <- k },
		now:    clk.now,
		tick:   tick,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.run(ctx)

	// A burst of 10 events for ONE path, all within the window (clock not advanced).
	for i := 0; i < 10; i++ {
		in <- "f.txt"
	}
	// A tick before the window elapses ⇒ nothing settled yet.
	tick <- clk.now()
	select {
	case k := <-out:
		t.Fatalf("emitted %q before the quiet window elapsed", k)
	case <-time.After(50 * time.Millisecond):
	}
	// Advance past the window, then one tick ⇒ exactly ONE settled emission.
	clk.advance(200 * time.Millisecond)
	tick <- clk.now()
	select {
	case k := <-out:
		if k != "f.txt" {
			t.Fatalf("settled %q, want f.txt", k)
		}
	case <-time.After(time.Second):
		t.Fatal("no settled emission after the quiet window")
	}
	select {
	case k := <-out:
		t.Fatalf("burst not coalesced: got a second emission %q", k)
	case <-time.After(50 * time.Millisecond):
	}
}

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *manualClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }
func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
func (c *manualClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

// ---------- WS-4 #6: tombstone propagation + anti-resurrection + ack-gated GC ----------

func TestCanGC(t *testing.T) {
	tomb := tombFI("a", 1, vv(1, 6, 2, 3)) // deleted at {1:6,2:3}
	cases := []struct {
		name string
		peer map[string]merkle.FileInfo
		want bool
	}{
		{"peer has not advertised the path", map[string]merkle.FileInfo{}, false},
		{"peer still holds the live pre-delete file", map[string]merkle.FileInfo{"a": liveFI("a", "x", 1, vv(1, 5, 2, 3))}, false},
		{"peer advertises a stale tombstone (dominated-by ours)", map[string]merkle.FileInfo{"a": tombFI("a", 1, vv(1, 5, 2, 3))}, false},
		{"peer advertises an equal tombstone (acked)", map[string]merkle.FileInfo{"a": tombFI("a", 1, vv(1, 6, 2, 3))}, true},
		{"peer advertises a newer tombstone", map[string]merkle.FileInfo{"a": tombFI("a", 1, vv(1, 7, 2, 3))}, true},
		{"peer concurrent tombstone (not yet merged)", map[string]merkle.FileInfo{"a": tombFI("a", 1, vv(1, 5, 2, 4))}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canGC(tomb, tc.peer); got != tc.want {
				t.Fatalf("canGC = %v, want %v", got, tc.want)
			}
		})
	}
}

// The tombstone's presence is load-bearing for anti-resurrection: with the tombstone
// retained, a stale peer's pre-delete file is Dominated ⇒ NoOp (no resurrection); had
// the tombstone been GC'd prematurely, the same advertisement would resolve to a fetch
// (resurrection) — proving the ack-gate (canGC) is what prevents it.
func TestTombstone_NoResurrectionAndPrematureGCNegative(t *testing.T) {
	stalePeerFile := liveFI("f.txt", "old", 1, vv(1, 1)) // peer never saw the delete

	// With the tombstone retained (we deleted at {1:2}):
	tomb := tombFI("f.txt", 1, vv(1, 2))
	if p := resolve(&tomb, &stalePeerFile, 1); p.kind != planNoOp {
		t.Fatalf("retained tombstone must Dominate a stale peer ⇒ NoOp, got %v", p.kind)
	}
	// Premature-GC negative: had we GC'd the tombstone (local now nil), the SAME stale
	// advertisement resurrects the file (planInstall) — the ack-gate is load-bearing.
	if p := resolve(nil, &stalePeerFile, 1); p.kind != planInstall {
		t.Fatalf("premature GC must let the file resurrect (planInstall), got %v — gate not load-bearing", p.kind)
	}
}

func TestGCTombstones_AckGated(t *testing.T) {
	e := tempEngine(t)
	tomb := tombFI("f.txt", 1, vv(uint64(e.selfShort), 2))
	e.mu.Lock()
	e.files["f.txt"] = tomb
	e.mu.Unlock()

	// No peer known ⇒ retain (never a timer).
	e.mu.Lock()
	changed := e.gcTombstonesLocked()
	e.mu.Unlock()
	if changed {
		t.Fatal("GC'd a tombstone with no peer known (must retain)")
	}

	// Peer present but has NOT acked ⇒ retain.
	fc := newFakeConn(0x70)
	ps := e.registerFakePeer(fc)
	e.mu.Lock()
	if e.gcTombstonesLocked() {
		e.mu.Unlock()
		t.Fatal("GC'd before the peer acked")
	}
	// Peer now advertises the same tombstone (ack) ⇒ GC proceeds.
	ps.index["f.txt"] = tomb
	got := e.gcTombstonesLocked()
	_, still := e.files["f.txt"]
	e.mu.Unlock()
	if !got || still {
		t.Fatalf("ack-gated GC failed: changed=%v stillPresent=%v", got, still)
	}
}

// ---------- VV lifecycle helpers ----------

func TestRestoreVVs(t *testing.T) {
	self := protocol.ShortID(5)
	prev := []merkle.FileInfo{
		liveFI("keep", "same", 1, vv(9, 4)),   // unchanged ⇒ keep history
		liveFI("edit", "before", 1, vv(9, 2)), // changed while down ⇒ bump
		tombFI("gone", 1, vv(9, 7)),           // a tombstone (handled by SynthesizeDeletions, not here)
	}
	cur := []merkle.FileInfo{
		liveFI("keep", "same", 2, nil),  // scanner leaves VV empty
		liveFI("edit", "after", 2, nil), // content differs from prev
		liveFI("fresh", "new", 2, nil),  // brand-new ⇒ empty VV (CDD-3)
	}
	out := restoreVVs(prev, cur, self)
	byPath := map[string]merkle.FileInfo{}
	for _, fi := range out {
		byPath[fi.Path] = fi
	}
	if byPath["keep"].Version.Compare(vv(9, 4)) != protocol.Equal {
		t.Fatalf("unchanged file lost its history: %v", byPath["keep"].Version)
	}
	if byPath["edit"].Version.Compare(vv(9, 2, uint64(self), 1)) != protocol.Equal {
		t.Fatalf("changed file not bumped on top of history: %v", byPath["edit"].Version)
	}
	if len(byPath["fresh"].Version) != 0 {
		t.Fatalf("new file must seed empty VV (initial scan is not authorship): %v", byPath["fresh"].Version)
	}
}

func TestDropFromVV(t *testing.T) {
	in := vv(1, 3, 2, 5, 3, 7)
	out := dropFromVV(in, 2)
	if out.Get(2) != 0 || out.Get(1) != 3 || out.Get(3) != 7 {
		t.Fatalf("dropFromVV(2) = %v, want {1:3,3:7}", out)
	}
	// Copy-on-write: the receiver is unchanged.
	if in.Get(2) != 5 {
		t.Fatal("dropFromVV mutated the receiver")
	}
}

// ---------- case-sensitivity probe sanity ----------

func TestProbeCaseSensitive_NoPanicAndConsistent(t *testing.T) {
	dir := t.TempDir()
	a := probeCaseSensitive(dir)
	b := probeCaseSensitive(dir)
	if a != b {
		t.Fatalf("case probe inconsistent: %v vs %v", a, b)
	}
	// Probe must not leave its temp files behind.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), "caseprobe") {
			t.Fatalf("probe left a temp file: %s", e.Name())
		}
	}
}
