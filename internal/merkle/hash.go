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
	var zero [32]byte
	f, err := os.Open(osPath)
	if err != nil {
		return zero, fmt.Errorf("merkle: hash open %s: %w", osPath, err)
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, hashBufSize)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return zero, fmt.Errorf("merkle: hash read %s: %w", osPath, err)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}
