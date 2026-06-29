package merkle

import "github.com/haider-toha/merkle-sync/internal/protocol"

// FileType is the kind of a tree leaf. Directories are internal tree NODES, not
// leaves, but Dir is kept in the enum for the wire grammar's forward-compat
// (wire-fileinfo-grammar decision); a leaf's Type is only File or Symlink.
type FileType uint8

const (
	TypeFile    FileType = 0
	TypeDir     FileType = 1 // never a leaf in v1 (empty dirs not synced, CDD-8)
	TypeSymlink FileType = 2
)

// FileInfo is the per-path two-way-sync leaf (MK-3, leaf-shape decision). A bare
// content hash answers "do the bytes differ"; these extra fields answer the three
// questions two-way sync needs: who is causally newer (Version), concurrent vs
// causal (Version.Compare), and deleted vs never-created (Deleted tombstone).
//
// The STRUCTURAL hash commits to only a subset — content_hash, a portable 2-state
// mode, deleted, and the version vector — and EXCLUDES raw mode, mtime, and size
// (structural-hash-grammar-finalization decision). The full fields live here for
// transfer planning (Size), conflict tiebreaking (ModTimeNS, SR-4 — never for
// ordering), and best-effort advisory apply (Mode).
type FileInfo struct {
	Path        string                 // canonical forward-slash relative NFC key (identity / tree position)
	ContentHash [32]byte               // SHA-256 of file bytes; symlink: hash of target; tombstone: 32x0x00
	Size        uint64                 // bytes; scanner pre-filter + transfer hint; NOT hashed
	Mode        uint32                 // full advisory POSIX mode; NOT hashed (only the 2-state derived below is)
	ModTimeNS   int64                  // mtime ns; conflict tiebreaker ONLY (SR-4); NOT hashed
	Version     protocol.VersionVector // causal clock; bumped only on confirmed local authorship (SR-6)
	Deleted     bool                   // tombstone: a delete is a versioned event, not an absence (SR-9)
	Type        FileType               // File | Dir | Symlink
}

// executable reports whether the portable executable bit is set: any of the
// owner/group/other x bits on a regular file. Symlinks and dirs are never
// "executable" in the 2-state sense (mode-symlink-mapping decision).
func (fi FileInfo) executable() bool {
	return fi.Type == TypeFile && fi.Mode&0o111 != 0
}

// canonicalModeByte is the portable 2-state mode the structural hash commits to:
// bit 0 = executable, bit 1 = is-symlink. Raw POSIX bits are NOT hashed because
// NTFS cannot represent them — hashing raw mode manufactures a permanent cross-OS
// root difference for identical bytes (XP-6). Hashing only {exec, type} both
// converges Mac<->Windows and still propagates a chmod +x.
func (fi FileInfo) canonicalModeByte() byte {
	var b byte
	if fi.executable() {
		b |= 0x01
	}
	if fi.Type == TypeSymlink {
		b |= 0x02
	}
	return b
}

// IsTombstone reports whether this leaf is a deletion marker.
func (fi FileInfo) IsTombstone() bool { return fi.Deleted }

// SetDeleted returns a tombstone copy of fi: content zeroed, size 0, deleted set,
// and self's version-vector counter bumped (a delete is a versioned local
// authorship event, SR-9). The flipped deleted byte plus the bumped VV make the
// tombstone's structural hash distinct from the pre-delete leaf, so the deletion
// shows in the diff and changes the root (WS-1 criterion 5). The receiver is not
// mutated — Version.Bump is copy-on-write (GR-5).
func (fi FileInfo) SetDeleted(self protocol.ShortID) FileInfo {
	out := fi
	out.ContentHash = [32]byte{}
	out.Size = 0
	out.Deleted = true
	out.Version = fi.Version.Bump(self)
	return out
}
