package reconcile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/haider-toha/merkle-sync/internal/merkle"
	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// BlockSize is the fixed content-addressed transfer/dedup block: 32 KiB (MK-4,
// chunking-fixed-32kib-vs-cdc). A block maps 1:1 to one REQUEST/RESPONSE and is far
// under the 16 MiB MaxFrameLen, so a block is always a single frame.
const BlockSize = 32 * 1024

// Transfer sentinels (branchable with errors.Is, GR-6).
var (
	// ErrVerifyFailed is returned when the reassembled file's whole-file SHA-256 does
	// not equal the leaf content_hash — the integrity backstop that fires BEFORE the
	// atomic rename, so a corrupt reconstruction never replaces the user's file (MK-4).
	ErrVerifyFailed = errors.New("reconcile: reconstructed content hash mismatch")
	// ErrSourceDeclined is returned when the source answers a REQUEST with a non-OK
	// code or a short chunk; the puller abandons that file and leaves dst untouched.
	ErrSourceDeclined = errors.New("reconcile: source declined chunk request")
	// ErrPeerGone is returned when the peer connection closed mid-transfer.
	ErrPeerGone = errors.New("reconcile: peer connection gone")
	// ErrRequestTimeout is returned when a chunk REQUEST is not answered in time.
	ErrRequestTimeout = errors.New("reconcile: chunk request timed out")
	// ErrCaseClobber is the refuse+flag verdict: materialising this key would clobber
	// an existing, fold-equal file under a DIFFERENT canonical key on a case/
	// normalisation-insensitive target (CDD-5, XP-4) — never overwritten.
	ErrCaseClobber = errors.New("reconcile: refused case/normalisation clobber")
	// ErrTypeClash is the refuse+flag verdict for a file-vs-directory divergence: one
	// peer holds a FILE at a path the other holds as a DIRECTORY (a file deleted +
	// recreated as a dir of the same name, or a Mac↔Windows structural divergence).
	// The two are irreconcilable at one path without choosing a loser, so v1 REFUSES to
	// apply either side — both peers keep their own data (no loss), the path is left
	// divergent and FLAGGED, exactly like the case/normalisation no-clobber refuse
	// (CDD-5). The differ reports it via DiffEntry.IsTypeClash (MK-2); auto keep-both
	// (directory wins, file -> .sync-conflict copy) is the logged forward path
	// (decisions/phase7/MK-2-file-vs-dir-typeclash-resolution.md).
	ErrTypeClash = errors.New("reconcile: refused file-vs-directory type clash")
	// ErrMaxPathExceeded is the refuse+flag verdict when a conflict-copy canonical key
	// would exceed Windows MAX_PATH (260) on a Windows target whose long-path support is
	// not confirmed: minting it risks a copy a Windows peer cannot write, so v1 REFUSES
	// the whole conflict (the loser stays at its path, never overwritten — no data lost)
	// and FLAGS it, the same accepted carve-out as ErrCaseClobber / ErrTypeClash
	// (XP-3, decisions/crossplatform/maxpath-longpath-handling.md; PR-3 §6).
	ErrMaxPathExceeded = errors.New("reconcile: refused conflict copy exceeding Windows MAX_PATH")
)

// numBlocks is the number of 32 KiB blocks a file of size bytes splits into (0 for an
// empty file, whose materialisation is an empty temp verified against SHA-256("")).
func numBlocks(size uint64) int {
	if size == 0 {
		return 0
	}
	return int((size + BlockSize - 1) / BlockSize)
}

// validateRequest is the source-side REQUEST guard (CDD-2, SR-12): a length must be
// in (0, MaxChunkLen] and the range must lie within the file's CURRENT size. A
// failing request is declined cleanly (RESPONSE{GENERIC}) by the caller, never
// over-allocated or fatal. Pure; table-tested.
func validateRequest(req protocol.Request, size uint64) bool {
	if req.Length == 0 || req.Length > protocol.MaxChunkLen {
		return false
	}
	if req.Offset > size || req.Offset+uint64(req.Length) > size {
		return false
	}
	return true
}

// atomicWriteVerify materialises content at dstOSPath without ever leaving a corrupt
// or partial file there (SR-1/SR-2, MK-4): it streams fill(...) into a temp on dst's
// own filesystem while hashing, asserts the whole-file SHA-256 equals expected BEFORE
// committing, then tmp.Sync -> os.Rename -> parent-dir fsync. On ANY error the temp is
// discarded and dst is left untouched (a kill mid-fill leaves only the temp, which a
// re-run replaces). The verify-before-rename is what makes a killed transfer safe.
func atomicWriteVerify(dstOSPath string, expected [32]byte, fill func(w io.Writer) error) (err error) {
	dir := filepath.Dir(dstOSPath)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return fmt.Errorf("reconcile: mkdir %s: %w", dir, mkErr)
	}
	tmp, err := os.CreateTemp(dir, ".msync-*.tmp")
	if err != nil {
		return fmt.Errorf("reconcile: create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close() // a second Close after the happy path is skipped (committed)
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	if err = fill(io.MultiWriter(tmp, h)); err != nil {
		return err
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	if got != expected {
		return fmt.Errorf("%w: got %x want %x", ErrVerifyFailed, got, expected)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("reconcile: fsync temp: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("reconcile: close temp: %w", err)
	}
	if err = os.Rename(tmpName, dstOSPath); err != nil {
		return fmt.Errorf("reconcile: rename temp to %s: %w", dstOSPath, err)
	}
	committed = true
	if d, derr := os.Open(dir); derr == nil { // flush the rename itself (SR-2)
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// probeCaseSensitive reports whether absRoot's filesystem is case-SENSITIVE, by
// creating two temp names differing only in case and observing whether they are two
// distinct files (the same technique Syncthing uses). On any probe failure it returns
// false (assume insensitive ⇒ ENFORCE no-clobber — the safe direction, CDD-5).
func probeCaseSensitive(absRoot string) bool {
	lower := filepath.Join(absRoot, ".msync-caseprobe-x")
	upper := filepath.Join(absRoot, ".msync-caseprobe-X")
	if err := os.WriteFile(lower, []byte("x"), 0o600); err != nil {
		return false
	}
	defer os.Remove(lower)
	if err := os.WriteFile(upper, []byte("X"), 0o600); err != nil {
		return false
	}
	defer os.Remove(upper)
	b, err := os.ReadFile(lower)
	if err != nil {
		return false
	}
	// On an insensitive FS the upper write overwrote the lower file ⇒ lower now reads
	// "X". On a sensitive FS they are independent ⇒ lower still reads "x".
	return string(b) != "X"
}

// noClobberConflict is the filesystem's OWN verdict on whether materialising canonKey
// would clobber a different logical file (CDD-5). On a case-sensitive target distinct
// keys coexist, so it never refuses. Otherwise it lists the real target directory and,
// if an existing entry canonicalises to a DIFFERENT key whose basename folds equal to
// canonKey's, returns that key + true (refuse + flag). A directory that does not yet
// exist, or an entry equal to canonKey itself (a normal update), is not a clobber. The
// fold errs toward over-refuse (safe); the listing is the FS's actual namespace, so a
// fold/probe miss fails safe to refuse, never to clobber.
func (e *Engine) noClobberConflict(canonKey string) (string, bool) {
	if e.caseSensitive {
		return "", false
	}
	host := pathnorm.HostTarget()
	osPath := pathnorm.ToOSPath(e.absRoot, canonKey, host)
	dir := filepath.Dir(osPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	targetFold := pathnorm.Fold(path.Base(canonKey))
	for _, ent := range entries {
		existKey, kerr := pathnorm.FromOSPath(e.absRoot, filepath.Join(dir, ent.Name()), host)
		if kerr != nil || existKey == "" {
			continue
		}
		if existKey == canonKey {
			return "", false // same logical file — an update, allowed
		}
		if pathnorm.Fold(path.Base(existKey)) == targetFold {
			return existKey, true // a different key folds equal — would clobber
		}
	}
	return "", false
}

// localSource finds an on-disk regular file (other than excludeKey) whose content
// hashes to hash, for content-addressed local reuse (MK-4 §3): this is what makes a
// rename cost ZERO network transfer (the new path reuses the still-present old file's
// bytes — PR-5). The candidate is re-hashed on disk so a stale index never feeds a
// wrong copy. Runs under RLock only to snapshot the candidate key (zero I/O held).
func (e *Engine) localSource(hash [32]byte, excludeKey string) (string, bool) {
	e.mu.RLock()
	var candidate string
	for key, fi := range e.files {
		if key == excludeKey || fi.Deleted || fi.Type != merkle.TypeFile {
			continue
		}
		if fi.ContentHash == hash {
			candidate = key
			break
		}
	}
	e.mu.RUnlock()
	if candidate == "" {
		return "", false
	}
	osPath := pathnorm.ToOSPath(e.absRoot, candidate, pathnorm.HostTarget())
	if h, err := merkle.HashFile(osPath); err != nil || h != hash {
		return "", false
	}
	return osPath, true
}

// materialise makes the on-disk file at leaf.Path hold leaf's content, then reports a
// completion to the engine loop (which updates the FileInfo map under the lock; the
// recorded leaf is the echo record a later re-hash is compared against). It runs in the
// per-peer puller goroutine, OFF the engine select
// loop (CDD-1). Order: idempotence skip -> no-clobber refuse -> local reuse -> network
// fetch. v1 materialises regular files; a non-regular leaf (symlink) is flagged and
// skipped (XP-6, N14 — symlinks lossy/deferred).
//
// It returns whether the content landed (ok). The caller (runFetch) uses the return to
// gate a coupled conflict's winner step on the loser copy actually succeeding; the
// reported completion (the loop's record/advertise) is orthogonal to the return.
func (e *Engine) materialise(ctx context.Context, ps *peerState, leaf merkle.FileInfo, advertise bool) bool {
	if leaf.Type != merkle.TypeFile {
		e.logf("skip non-regular leaf %q (type %d) — symlink sync is a v1 limitation (XP-6)", leaf.Path, leaf.Type)
		e.report(completion{leaf: leaf, ok: false, advertise: advertise, peer: ps.short})
		return false
	}
	host := pathnorm.HostTarget()
	osPath := pathnorm.ToOSPath(e.absRoot, leaf.Path, host)

	// 1. Idempotence (SR-3): the file already holds exactly this content.
	if h, err := merkle.HashFile(osPath); err == nil && h == leaf.ContentHash {
		e.report(completion{leaf: leaf, ok: true, advertise: advertise, peer: ps.short})
		return true
	}
	// 2. No-clobber by the filesystem's verdict (CDD-5).
	if other, clash := e.noClobberConflict(leaf.Path); clash {
		e.logf("%v: %q would clobber existing %q on this filesystem — refused", ErrCaseClobber, leaf.Path, other)
		e.report(completion{leaf: leaf, ok: false, clobber: true, advertise: advertise, peer: ps.short})
		return false
	}
	// 3. Local content-addressed reuse (zero network — rename / dedup).
	if srcOS, ok := e.localSource(leaf.ContentHash, leaf.Path); ok {
		err := atomicWriteVerify(osPath, leaf.ContentHash, func(w io.Writer) error {
			f, oerr := os.Open(srcOS)
			if oerr != nil {
				return oerr
			}
			defer f.Close()
			_, cerr := io.Copy(w, f)
			return cerr
		})
		if err == nil {
			e.report(completion{leaf: leaf, ok: true, advertise: advertise, peer: ps.short})
			return true
		}
		e.logf("local reuse for %q failed (%v); fetching over the wire", leaf.Path, err)
	}
	// 4. Network fetch, stop-and-wait, verify-before-commit.
	err := atomicWriteVerify(osPath, leaf.ContentHash, func(w io.Writer) error {
		return e.fetchOverWire(ctx, ps, leaf, w)
	})
	if err != nil {
		e.logf("fetch %q from %d failed: %v", leaf.Path, ps.short, err)
	}
	ok := err == nil
	e.report(completion{leaf: leaf, ok: ok, advertise: advertise, peer: ps.short})
	return ok
}

// fetchOverWire pulls leaf's bytes one 32 KiB block at a time (stop-and-wait: ≤1
// outstanding REQUEST per peer, the back-pressure bound — CDD-1) and writes them to w
// (which the caller is hashing for the verify). The puller clamps Length to MaxChunkLen
// (CDD-2). A declined or short chunk aborts the file (dst stays untouched).
func (e *Engine) fetchOverWire(ctx context.Context, ps *peerState, leaf merkle.FileInfo, w io.Writer) error {
	nb := numBlocks(leaf.Size)
	for i := 0; i < nb; i++ {
		offset := uint64(i) * BlockSize
		length := uint32(BlockSize)
		if remaining := leaf.Size - offset; remaining < uint64(length) {
			length = uint32(remaining)
		}
		if length > protocol.MaxChunkLen {
			length = protocol.MaxChunkLen
		}
		resp, err := e.requestChunk(ctx, ps, leaf, offset, length)
		if err != nil {
			return err
		}
		if resp.Code != protocol.CodeOK {
			return fmt.Errorf("%w: code=%d offset=%d", ErrSourceDeclined, resp.Code, offset)
		}
		if uint32(len(resp.Data)) != length {
			return fmt.Errorf("%w: got %d bytes want %d at offset %d", ErrSourceDeclined, len(resp.Data), length, offset)
		}
		if _, err := w.Write(resp.Data); err != nil {
			return err
		}
	}
	return nil
}

// requestChunk sends one REQUEST and waits for its RESPONSE (routed by the engine loop
// to a per-reqID channel). It is the stop-and-wait unit; the response channel is
// buffered (depth 1) and the puller is always waiting, so the engine loop never blocks
// delivering. Send is non-blocking; a full outbound buffer would shed the peer, but
// stop-and-wait keeps the buffer near-empty so that never happens under normal flow.
func (e *Engine) requestChunk(ctx context.Context, ps *peerState, leaf merkle.FileInfo, offset uint64, length uint32) (protocol.Response, error) {
	reqID := ps.nextReq.Add(1)
	ch := make(chan protocol.Response, 1)
	ps.respMu.Lock()
	ps.resp[reqID] = ch
	ps.respMu.Unlock()
	defer func() {
		ps.respMu.Lock()
		delete(ps.resp, reqID)
		ps.respMu.Unlock()
	}()

	req := protocol.Request{ReqID: reqID, Path: leaf.Path, ContentHash: leaf.ContentHash, Offset: offset, Length: length}
	if !ps.conn.Send(req) {
		return protocol.Response{}, ErrPeerGone
	}
	timer := time.NewTimer(e.requestTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		return protocol.Response{}, ctx.Err()
	case <-ps.conn.Done():
		return protocol.Response{}, ErrPeerGone
	case <-timer.C:
		return protocol.Response{}, ErrRequestTimeout
	}
}

// serveRequest answers a peer's REQUEST from local disk, OFF the engine loop in the
// per-peer server goroutine (CDD-1). It validates the request (CDD-2) and declines
// cleanly with a typed RESPONSE code on any problem, KEEPING the connection — a bad
// request from a buggy/hostile peer is never fatal (SR-12).
func (e *Engine) serveRequest(ps *peerState, req protocol.Request) {
	decline := func(code protocol.ErrorCode) {
		ps.conn.Send(protocol.Response{ReqID: req.ReqID, Code: code})
	}
	canonKey, err := pathnorm.CanonicalizeSlash(req.Path)
	if err != nil {
		decline(protocol.CodeGeneric)
		return
	}
	osPath := pathnorm.ToOSPath(e.absRoot, canonKey, pathnorm.HostTarget())
	info, err := os.Stat(osPath)
	if err != nil || info.IsDir() {
		decline(protocol.CodeNoSuchFile)
		return
	}
	if !validateRequest(req, uint64(info.Size())) {
		decline(protocol.CodeGeneric)
		return
	}
	f, err := os.Open(osPath)
	if err != nil {
		decline(protocol.CodeNoSuchFile)
		return
	}
	defer f.Close()
	data := make([]byte, req.Length)
	if _, err := f.ReadAt(data, int64(req.Offset)); err != nil {
		decline(protocol.CodeGeneric)
		return
	}
	ps.conn.Send(protocol.Response{ReqID: req.ReqID, Code: protocol.CodeOK, Data: data})
}
