package merkle

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// hashBufSize is the read buffer for streaming a file through SHA-256. 64 KiB is a
// good balance of syscalls vs memory and is independent of the 32 KiB transfer
// chunk size (a WS-4 concern).
const hashBufSize = 64 << 10

// HashBytes returns the SHA-256 of b. Used for symlink targets and small blobs.
func HashBytes(b []byte) [32]byte { return sha256.Sum256(b) }

// HashFile streams the file at osPath through SHA-256 and returns the digest. The
// content hash is pure file bytes — independent of name, mode, and mtime — so it
// is identical on Mac and Windows and doubles as the transfer/dedup key (MK-3).
func HashFile(osPath string) ([32]byte, error) {
	h, _, err := HashFileSize(osPath)
	return h, err
}

// HashFileSize streams the file at osPath through SHA-256 and returns BOTH the digest
// and the exact number of bytes that produced it (io.Copy's count). A leaf's Size MUST
// be sourced from here, never from a separate os.Stat/DirEntry.Info(): the digest and a
// stat are two non-atomic reads, so a file rewritten between them yields a torn leaf
// (Size from one file-state, ContentHash from another). A torn leaf is un-transferable —
// the receiver derives numBlocks(Size) blocks but their bytes hash to a different value,
// failing the verify-before-rename forever — and, because the change-detection compare
// keys on ContentHash, it never self-corrects. Deriving Size from the bytes actually
// hashed makes every leaf internally consistent: Size always equals the length of the
// content that hashes to ContentHash, so the transfer range math is always valid and a
// mid-scan rewrite yields a consistent (possibly stale) snapshot the NEXT scan corrects.
// REV-FLAKE-1; decisions/phase7/REV-FLAKE-1-torn-scan-size-hash.md.
func HashFileSize(osPath string) ([32]byte, int64, error) {
	var zero [32]byte
	f, err := os.Open(osPath)
	if err != nil {
		return zero, 0, fmt.Errorf("merkle: hash open %s: %w", osPath, err)
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, hashBufSize)
	n, err := io.CopyBuffer(h, f, buf)
	if err != nil {
		return zero, 0, fmt.Errorf("merkle: hash read %s: %w", osPath, err)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, n, nil
}
