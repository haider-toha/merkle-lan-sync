package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// ---------- helpers ----------

func mustGenIdentity(t *testing.T) *Identity {
	t.Helper()
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	return id
}

func fixedHash(seed byte) [32]byte {
	var h [32]byte
	for i := range h {
		h[i] = seed + byte(i)
	}
	return h
}

// waitForKind reads events until one of the wanted kind arrives, discarding other
// kinds. Fails the test on timeout.
func waitForKind(t *testing.T, ch <-chan Event, kind EventKind, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-ch:
			if e.Kind == kind {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %v event", kind)
		}
	}
}

// connectedPair brings up listener A and dials it from B with each allow-listing
// the other, and returns both transports plus the server-side (A→B) and
// client-side (B→A) Conn handles after both observe PeerConnected.
func connectedPair(t *testing.T, ctx context.Context) (a, b *Transport, idA, idB *Identity, connAB, connBA *Conn) {
	t.Helper()
	idA = mustGenIdentity(t)
	idB = mustGenIdentity(t)
	a = New(ctx, idA, NewAllowlist(idB.DeviceID))
	b = New(ctx, idB, NewAllowlist(idA.DeviceID))
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	addr, err := a.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	dialErr := make(chan error, 1)
	go func() { dialErr <- b.Dial("tcp", addr.String()) }()

	evA := waitForKind(t, a.Events(), PeerConnected, 5*time.Second)
	evB := waitForKind(t, b.Events(), PeerConnected, 5*time.Second)
	if err := <-dialErr; err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if evA.DeviceID != idB.DeviceID {
		t.Fatalf("A pinned %s, want B %s", evA.DeviceID, idB.DeviceID)
	}
	if evB.DeviceID != idA.DeviceID {
		t.Fatalf("B pinned %s, want A %s", evB.DeviceID, idA.DeviceID)
	}
	return a, b, idA, idB, evA.Conn, evB.Conn
}

// rawPeer dials A's listener directly as a bare tls.Client using me's cert
// (which A must already allow), completes the TLS handshake, then sends a HELLO
// carrying helloDeviceID and consumes A's HELLO. The returned live tls.Conn is
// used to write raw, possibly-malicious, bytes that the transport's own sender
// guards would never emit.
func rawPeer(t *testing.T, ctx context.Context, addr string, me, peerA *Identity, helloDeviceID protocol.DeviceID) *tls.Conn {
	t.Helper()
	cfg := clientTLSConfig(me, NewAllowlist(peerA.DeviceID))
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("rawPeer dial: %v", err)
	}
	conn := tls.Client(raw, cfg)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := conn.HandshakeContext(ctx); err != nil {
		t.Fatalf("rawPeer handshake: %v", err)
	}
	if err := protocol.WriteMessage(conn, protocol.Hello{ProtoVersion: ProtoVersion, DeviceID: helloDeviceID}); err != nil {
		t.Fatalf("rawPeer write HELLO: %v", err)
	}
	if _, err := readHello(conn); err != nil {
		t.Logf("rawPeer read peer HELLO (tolerated): %v", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn
}

func writeFragmented(t *testing.T, conn net.Conn, data []byte, chunk int) {
	t.Helper()
	for off := 0; off < len(data); off += chunk {
		end := off + chunk
		if end > len(data) {
			end = len(data)
		}
		if _, err := conn.Write(data[off:end]); err != nil {
			t.Fatalf("fragmented write at %d: %v", off, err)
		}
	}
}

// ---------- identity ----------

func TestGenerateIdentity_DistinctDeterministicLeaf(t *testing.T) {
	a := mustGenIdentity(t)
	b := mustGenIdentity(t)
	if a.DeviceID == b.DeviceID {
		t.Fatal("two freshly generated identities share a DeviceID")
	}
	if a.Certificate.Leaf == nil {
		t.Fatal("Certificate.Leaf not populated")
	}
	if got := protocol.DeviceIDFromCert(a.Certificate.Certificate[0]); got != a.DeviceID {
		t.Fatalf("DeviceID %s != hash of DER %s", a.DeviceID, got)
	}
	if got := protocol.DeviceIDFromCert(a.Certificate.Leaf.Raw); got != a.DeviceID {
		t.Fatalf("Leaf.Raw hash %s != DeviceID %s", got, a.DeviceID)
	}
}

func TestLoadOrCreateIdentity_PersistReload(t *testing.T) {
	dir := t.TempDir()
	id1, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("first LoadOrCreate: %v", err)
	}
	id2, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("reload LoadOrCreate: %v", err)
	}
	if id1.DeviceID != id2.DeviceID {
		t.Fatalf("DeviceID changed across reload: %s -> %s", id1.DeviceID, id2.DeviceID)
	}
	for _, name := range []string{certFileName, keyFileName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, keyFileName))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key perms = %v, want 0600", perm)
		}
	}
}

func TestLoadOrCreateIdentity_InconsistentRefuses(t *testing.T) {
	dir := t.TempDir()
	// Only the cert is present (key missing): must refuse, not silently regenerate
	// a new DeviceID.
	if err := os.WriteFile(filepath.Join(dir, certFileName), []byte("not a real cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateIdentity(dir); err == nil {
		t.Fatal("expected an error for an inconsistent (cert-only) identity dir")
	}
}

// TestLoadOrCreateIdentity_HostileDirName: the config dir is operator-chosen, not
// a synced canonical path, but it must still tolerate spaces / Unicode (the
// Mac-runnable axis). Windows reserved-name directories (CON, NUL...) are a
// Windows-only concern out of WS-2 scope (see CROSS_PLATFORM_CHECKLIST).
func TestLoadOrCreateIdentity_HostileDirName(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"config dir with spaces", "ünïcödé-résumé", "dots..and..dots"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(base, name)
			id, err := LoadOrCreateIdentity(dir)
			if err != nil {
				t.Fatalf("LoadOrCreate(%q): %v", dir, err)
			}
			id2, err := LoadOrCreateIdentity(dir)
			if err != nil || id2.DeviceID != id.DeviceID {
				t.Fatalf("reload mismatch for %q: err=%v", dir, err)
			}
		})
	}
}

// ---------- allow-list ----------

func TestAllowlist_AddRemoveAllowed(t *testing.T) {
	a := mustGenIdentity(t).DeviceID
	b := mustGenIdentity(t).DeviceID
	al := NewAllowlist(a)
	cases := []struct {
		name string
		op   func()
		id   protocol.DeviceID
		want bool
	}{
		{"seeded allowed", func() {}, a, true},
		{"unseeded denied", func() {}, b, false},
		{"add b", func() { al.Add(b) }, b, true},
		{"remove a", func() { al.Remove(a) }, a, false},
		{"remove idempotent", func() { al.Remove(a) }, a, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.op()
			if got := al.Allowed(tc.id); got != tc.want {
				t.Fatalf("Allowed(%s) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestAllowlist_ConcurrentRaceSafe(t *testing.T) {
	al := NewAllowlist()
	ids := make([]protocol.DeviceID, 8)
	for i := range ids {
		ids[i] = mustGenIdentity(t).DeviceID
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(3)
		go func(id protocol.DeviceID) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				al.Add(id)
			}
		}(id)
		go func(id protocol.DeviceID) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				al.Remove(id)
			}
		}(id)
		go func(id protocol.DeviceID) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = al.Allowed(id)
			}
		}(id)
	}
	wg.Wait()
}

// ---------- TLS pinning (criterion 3) ----------

func TestPinVerifier(t *testing.T) {
	allowed := mustGenIdentity(t)
	denied := mustGenIdentity(t)
	verify := pinVerifier(NewAllowlist(allowed.DeviceID))

	csWith := func(id *Identity) tls.ConnectionState {
		return tls.ConnectionState{PeerCertificates: []*x509.Certificate{id.Certificate.Leaf}}
	}
	cases := []struct {
		name    string
		cs      tls.ConnectionState
		wantErr error
	}{
		{"allow-listed device passes", csWith(allowed), nil},
		{"unknown device rejected", csWith(denied), ErrUntrustedDevice},
		{"no peer certificate rejected", tls.ConnectionState{}, ErrNoPeerCert},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verify(tc.cs)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("verify = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("verify = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestTLS_PinsIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, idA, idB, connAB, connBA := connectedPair(t, ctx)

	if connAB.DeviceID() != idB.DeviceID {
		t.Fatalf("server-side conn pinned %s, want %s", connAB.DeviceID(), idB.DeviceID)
	}
	if connBA.DeviceID() != idA.DeviceID {
		t.Fatalf("client-side conn pinned %s, want %s", connBA.DeviceID(), idA.DeviceID)
	}
	// The in-band HELLO DeviceID must equal the TLS-pinned one (defence in depth).
	if connAB.Hello().DeviceID != idB.DeviceID || connBA.Hello().DeviceID != idA.DeviceID {
		t.Fatal("HELLO DeviceID did not match the TLS-pinned identity")
	}
}

func TestTLS_WrongFingerprintRejected(t *testing.T) {
	cases := []struct {
		name               string
		aAllowsB, bAllowsA bool
	}{
		{"server rejects unlisted client", false, true},
		{"client rejects unlisted server", true, false},
		{"mutual reject", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			idA := mustGenIdentity(t)
			idB := mustGenIdentity(t)
			allowA := NewAllowlist()
			allowB := NewAllowlist()
			if tc.aAllowsB {
				allowA.Add(idB.DeviceID)
			}
			if tc.bAllowsA {
				allowB.Add(idA.DeviceID)
			}
			a := New(ctx, idA, allowA)
			b := New(ctx, idB, allowB)
			defer func() { _ = a.Close(); _ = b.Close() }()

			addr, err := a.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("Listen: %v", err)
			}
			if err := b.Dial("tcp", addr.String()); err == nil {
				t.Fatal("Dial succeeded; want a rejected handshake")
			}
			// The handshake aborted before any frame: A must never emit PeerConnected.
			select {
			case e := <-a.Events():
				if e.Kind == PeerConnected {
					t.Fatalf("A emitted PeerConnected for a rejected peer %s", e.DeviceID)
				}
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

func TestHELLO_DeviceIDMismatchDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idA := mustGenIdentity(t)
	idB := mustGenIdentity(t)     // the real cert the peer presents (A trusts it)
	idWrong := mustGenIdentity(t) // a different DeviceID the peer lies about in HELLO

	a := New(ctx, idA, NewAllowlist(idB.DeviceID))
	defer func() { _ = a.Close() }()
	addr, err := a.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	// TLS pins idB (allow-listed) but the HELLO claims idWrong -> establish drops.
	conn := rawPeer(t, ctx, addr.String(), idB, idA, idWrong.DeviceID)

	select {
	case e := <-a.Events():
		if e.Kind == PeerConnected {
			t.Fatalf("A connected despite HELLO DeviceID mismatch (pinned idB, HELLO idWrong)")
		}
	case <-time.After(500 * time.Millisecond):
	}

	// A must have closed the connection.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	for {
		_, rerr := conn.Read(buf)
		if errors.Is(rerr, os.ErrDeadlineExceeded) {
			t.Fatal("connection still open after HELLO DeviceID mismatch")
		}
		if rerr != nil {
			return // EOF / closed: A dropped the peer
		}
	}
}

func TestHello_CarriesEngineFields(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idA := mustGenIdentity(t)
	idB := mustGenIdentity(t)
	rootA := fixedHash(0xAA)

	a := New(ctx, idA, NewAllowlist(idB.DeviceID), WithHello(func() protocol.Hello {
		// DeviceID here is deliberately wrong; the transport must override it with
		// its own identity.
		return protocol.Hello{ProtoVersion: ProtoVersion, FolderID: "folderX", RootHash: rootA, FeatureFlags: 0x5, DeviceID: idB.DeviceID}
	}))
	b := New(ctx, idB, NewAllowlist(idA.DeviceID))
	defer func() { _ = a.Close(); _ = b.Close() }()

	addr, err := a.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = b.Dial("tcp", addr.String()) }()

	ev := waitForKind(t, b.Events(), PeerConnected, 5*time.Second)
	h := ev.Conn.Hello()
	if h.DeviceID != idA.DeviceID {
		t.Fatalf("HELLO DeviceID = %s, want A's own %s (transport must override the provider)", h.DeviceID, idA.DeviceID)
	}
	if h.FolderID != "folderX" || h.RootHash != rootA || h.FeatureFlags != 0x5 {
		t.Fatalf("engine HELLO fields not carried: %+v", h)
	}
}

// ---------- framing over TLS (criteria 1 & 2) ----------

// TestConn_SplitFrameSurvives: a single frame written one byte at a time across
// the TLS session reassembles correctly on the receiver (SR-12 / GR-8, through a
// real tls.Conn). The payload is itself a Windows-hostile path (backslash + NFC
// Unicode) to prove the byte pipe does not mangle path-bearing fields.
func TestConn_SplitFrameSurvives(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idA := mustGenIdentity(t)
	idB := mustGenIdentity(t)
	a := New(ctx, idA, NewAllowlist(idB.DeviceID))
	defer func() { _ = a.Close() }()
	addr, err := a.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	conn := rawPeer(t, ctx, addr.String(), idB, idA, idB.DeviceID)
	_ = waitForKind(t, a.Events(), PeerConnected, 5*time.Second)

	req := protocol.Request{
		ReqID:       7,
		Path:        `dir\sub\résumé.txt`,
		ContentHash: fixedHash(0xAB),
		Offset:      42,
		Length:      4096,
	}
	var buf bytes.Buffer
	if err := protocol.WriteMessage(&buf, req); err != nil {
		t.Fatalf("encode REQUEST: %v", err)
	}
	// One byte per Write => one TLS record per byte => the receiver's io.ReadFull
	// must reassemble across many reads.
	writeFragmented(t, conn, buf.Bytes(), 1)

	ev := waitForKind(t, a.Events(), PeerMessage, 5*time.Second)
	got, ok := ev.Message.(protocol.Request)
	if !ok {
		t.Fatalf("delivered %T, want protocol.Request", ev.Message)
	}
	if got.ReqID != req.ReqID || got.Path != req.Path || got.ContentHash != req.ContentHash || got.Offset != req.Offset || got.Length != req.Length {
		t.Fatalf("split frame did not round-trip:\n got %+v\nwant %+v", got, req)
	}
}

// TestConn_MalformedLengthDropsPeerCleanly: an oversized length prefix from one
// peer drops THAT peer with ErrFrameTooLarge while a second peer's traffic is
// unaffected — proving per-conn isolation (no shared-stream desync), SR-12.
func TestConn_MalformedLengthDropsPeerCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idA := mustGenIdentity(t)
	idM := mustGenIdentity(t) // malicious
	idG := mustGenIdentity(t) // good
	a := New(ctx, idA, NewAllowlist(idM.DeviceID, idG.DeviceID))
	defer func() { _ = a.Close() }()
	addr, err := a.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	connM := rawPeer(t, ctx, addr.String(), idM, idA, idM.DeviceID)
	connG := rawPeer(t, ctx, addr.String(), idG, idA, idG.DeviceID)

	// Wait until A has registered both peers.
	seen := map[protocol.DeviceID]bool{}
	for len(seen) < 2 {
		e := waitForKind(t, a.Events(), PeerConnected, 5*time.Second)
		seen[e.DeviceID] = true
	}

	// M writes a raw 4-byte length of 0xFFFFFFFF (>> MaxFrameLen). The transport's
	// own WriteFrame would refuse to emit this; we bypass it deliberately.
	if _, err := connM.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF}); err != nil {
		t.Fatalf("write oversized length: %v", err)
	}
	// G sends a perfectly valid message.
	if err := protocol.WriteMessage(connG, protocol.Ping{}); err != nil {
		t.Fatalf("write PING: %v", err)
	}

	var (
		sawMDisc, sawGMsg, sawGDisc bool
		mDiscErr                    error
	)
	deadline := time.After(3 * time.Second)
	for !(sawMDisc && sawGMsg) {
		select {
		case e := <-a.Events():
			switch {
			case e.Kind == PeerDisconnected && e.DeviceID == idM.DeviceID:
				sawMDisc, mDiscErr = true, e.Err
			case e.Kind == PeerMessage && e.DeviceID == idG.DeviceID:
				sawGMsg = true
			case e.Kind == PeerDisconnected && e.DeviceID == idG.DeviceID:
				sawGDisc = true
			}
		case <-deadline:
			t.Fatalf("timeout: sawMDisc=%v sawGMsg=%v sawGDisc=%v", sawMDisc, sawGMsg, sawGDisc)
		}
	}
	if !errors.Is(mDiscErr, protocol.ErrFrameTooLarge) {
		t.Fatalf("M disconnect cause = %v, want ErrFrameTooLarge", mDiscErr)
	}
	// Grace window: confirm the good peer is NOT collaterally dropped.
	graceful := time.After(300 * time.Millisecond)
	for {
		select {
		case e := <-a.Events():
			if e.Kind == PeerDisconnected && e.DeviceID == idG.DeviceID {
				sawGDisc = true
			}
		case <-graceful:
			if sawGDisc {
				t.Fatal("good peer G was dropped by M's malformed frame (stream desync)")
			}
			// Prove G is still live end-to-end.
			if err := protocol.WriteMessage(connG, protocol.Ping{}); err != nil {
				t.Fatalf("good peer no longer writable: %v", err)
			}
			ev := waitForKind(t, a.Events(), PeerMessage, 2*time.Second)
			if ev.DeviceID != idG.DeviceID {
				t.Fatalf("post-drop message from %s, want good peer %s", ev.DeviceID, idG.DeviceID)
			}
			return
		}
	}
}

// TestConn_HostilePathPayloadsRoundTrip: Windows-hostile path strings survive the
// TLS byte pipe unchanged — the load-bearing cross-platform property for transport
// (a pipe that "helpfully" normalised a backslash, NUL, NFD form, or reserved name
// would break convergence, SR-13).
func TestConn_HostilePathPayloadsRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, _, _, _, _, connBA := connectedPair(t, ctx)

	cases := []struct {
		name string
		path string
	}{
		{"backslashes", `a\b\c`},
		{"windows reserved chars", `a<b>c:d"e|f?g*h`},
		{"reserved device name CON", `CON`},
		{"reserved name with extension", `NUL.txt`},
		{"com port name", `COM1`},
		{"trailing dot", `name.`},
		{"trailing space", `name `},
		{"nfd e-acute (decomposed)", "re\u0301sume\u0301"},
		{"nfc e-acute (composed)", "r\u00e9sum\u00e9"},
		{"case upper", "File.txt"},
		{"case lower", "file.txt"},
		{"control chars", "a\x01\x1f\x1bb"},
		{"embedded nul", "a\x00b"},
		{"forward-slash canonical", "dir/sub/file.txt"},
		{"unicode scripts", "目录/файл/ünïcödé.dat"},
		{"empty path", ""},
		{"max-ish unicode", "𝔘𝔫𝔦𝔠𝔬𝔡𝔢/emoji-😀.bin"},
	}

	for i, tc := range cases {
		req := protocol.Request{ReqID: uint32(i), Path: tc.path, ContentHash: fixedHash(byte(i)), Offset: uint64(i), Length: uint32(i)}
		if !connBA.Send(req) {
			t.Fatalf("case %q: Send was shed (buffer too small?)", tc.name)
		}
	}
	for i, tc := range cases {
		ev := waitForKind(t, a.Events(), PeerMessage, 5*time.Second)
		got, ok := ev.Message.(protocol.Request)
		if !ok {
			t.Fatalf("case %d (%q): delivered %T, want Request", i, tc.name, ev.Message)
		}
		if got.ReqID != uint32(i) {
			t.Fatalf("ordering/correlation broken: got ReqID %d at position %d", got.ReqID, i)
		}
		if got.Path != tc.path {
			t.Fatalf("case %q: path mangled by the wire:\n got %q (% x)\nwant %q (% x)", tc.name, got.Path, got.Path, tc.path, tc.path)
		}
	}
}

// ---------- lifecycle / leaks (criterion 4) ----------

func TestConnChurn_NoGoroutineLeak(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA := mustGenIdentity(t)
	allowA := NewAllowlist()
	a := New(ctx, idA, allowA)
	defer func() { _ = a.Close() }()

	addr, err := a.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	// Engine-role drainer: routes A's connect/disconnect events so emit never
	// blocks and the test can sequence iterations.
	connA := make(chan protocol.DeviceID, 32)
	discA := make(chan protocol.DeviceID, 32)
	go func() {
		for {
			select {
			case e := <-a.Events():
				switch e.Kind {
				case PeerConnected:
					connA <- e.DeviceID
				case PeerDisconnected:
					discA <- e.DeviceID
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	waitDevice := func(ch <-chan protocol.DeviceID, want protocol.DeviceID) {
		deadline := time.After(5 * time.Second)
		for {
			select {
			case got := <-ch:
				if got == want {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for device %s", want)
			}
		}
	}

	// Stabilise, then record the baseline (A listening + drainer running, no conns).
	base := stableGoroutines(150 * time.Millisecond)

	const N = 15
	for i := 0; i < N; i++ {
		idB := mustGenIdentity(t)
		allowA.Add(idB.DeviceID)
		b := New(ctx, idB, NewAllowlist(idA.DeviceID))
		if err := b.Dial("tcp", addr.String()); err != nil {
			t.Fatalf("iter %d Dial: %v", i, err)
		}
		waitDevice(connA, idB.DeviceID)
		if err := b.Close(); err != nil { // closes B -> A sees EOF -> drops its conn
			t.Fatalf("iter %d Close B: %v", i, err)
		}
		waitDevice(discA, idB.DeviceID)
		allowA.Remove(idB.DeviceID)
	}

	// Every per-conn goroutine (both sides) must be reaped: back to baseline.
	assertGoroutinesReturn(t, base, 3*time.Second)
}

// TestTransport_CloseIsIdempotent: Close may be called many times and from many
// goroutines without panic; the second call returns promptly.
func TestTransport_CloseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	a, b, _, _, _, _ := connectedPair(t, ctx)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = a.Close() }()
		go func() { defer wg.Done(); _ = b.Close() }()
	}
	wg.Wait()
}

// TestConn_CtxCancelTearsDown: cancelling the context passed to New tears the
// transport down (no explicit Close) and reaps goroutines.
func TestConn_CtxCancelTearsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := stableGoroutines(100 * time.Millisecond)

	idA := mustGenIdentity(t)
	idB := mustGenIdentity(t)
	a := New(ctx, idA, NewAllowlist(idB.DeviceID))
	b := New(ctx, idB, NewAllowlist(idA.DeviceID))
	addr, err := a.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go func() { _ = b.Dial("tcp", addr.String()) }()
	_ = waitForKind(t, b.Events(), PeerConnected, 5*time.Second)

	cancel() // no explicit Close
	// Wait for both transports' goroutines to drain via the ctx watcher.
	_ = a.Close() // also waits; idempotent with the watcher-triggered teardown
	_ = b.Close()
	assertGoroutinesReturn(t, base, 3*time.Second)
}

// stableGoroutines waits until the goroutine count is stable across two samples
// and returns it. This is a bounded poll for a steady state, not a sleep standing
// in for synchronisation.
func stableGoroutines(window time.Duration) int {
	prev := runtime.NumGoroutine()
	ticker := time.NewTicker(window)
	defer ticker.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		<-ticker.C
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return prev
}

// assertGoroutinesReturn polls (with a deadline) until the goroutine count drops
// back to target. Goroutine teardown is inherently asynchronous in the runtime, so
// the standard leak-check shape is a bounded retry against a hard deadline (this is
// what go.uber.org/goleak does internally), not a fixed sleep.
func assertGoroutinesReturn(t *testing.T, target int, timeout time.Duration) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(timeout)
	var n int
	for time.Now().Before(deadline) {
		runtime.GC()
		n = runtime.NumGoroutine()
		if n <= target {
			return
		}
		<-ticker.C
	}
	t.Fatalf("goroutine leak: NumGoroutine=%d did not return to baseline %d within %v", n, target, timeout)
}
