package merkle

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// snapshotVersion tags the on-disk format so a future change is detectable.
const snapshotVersion uint32 = 1

// ErrSnapshotFormat is returned by LoadSnapshot for a corrupt or unknown-version
// snapshot. The caller treats it like a missing snapshot: conservative create-only,
// no synthesized deletions (snapshot-and-deletion-synthesis decision).
var ErrSnapshotFormat = errors.New("merkle: snapshot is corrupt or an unknown version")

// snapshot is the gob-encoded local last-synced state. gob is permitted here
// because this is LOCAL on-disk state we wrote ourselves, never bytes from a peer
// (GR-7). It carries each leaf's VV and deleted flag so the startup diff can
// distinguish "deleted while down" from "never existed" (MK-6).
type snapshot struct {
	Version uint32
	Files   []FileInfo
}

// SaveSnapshot writes the FileInfo set to path atomically: a temp file in the same
// directory is gob-encoded, fsync'd, and renamed over path, then the parent
// directory is fsync'd (SR-1/SR-2 hygiene — a crash mid-write never corrupts the
// last good snapshot, even though this is local state).
func SaveSnapshot(path string, files []FileInfo) (err error) {
	var buf bytes.Buffer
	if encErr := gob.NewEncoder(&buf).Encode(snapshot{Version: snapshotVersion, Files: files}); encErr != nil {
		return fmt.Errorf("merkle: snapshot encode: %w", encErr)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("merkle: snapshot temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName) // leave the previous snapshot untouched on any error
		}
	}()

	if _, err = tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("merkle: snapshot write: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("merkle: snapshot sync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("merkle: snapshot close: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("merkle: snapshot rename: %w", err)
	}
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// LoadSnapshot loads a snapshot written by SaveSnapshot. A missing file is not an
// error — it returns (nil, nil), which SynthesizeDeletions treats as create-only. A
// corrupt or unknown-version file returns ErrSnapshotFormat (same conservative
// handling). The returned set is never decoded from peer bytes (GR-7).
func LoadSnapshot(path string) ([]FileInfo, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("merkle: snapshot read: %w", err)
	}
	var s snapshot
	if decErr := gob.NewDecoder(bytes.NewReader(b)).Decode(&s); decErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrSnapshotFormat, decErr)
	}
	if s.Version != snapshotVersion {
		return nil, fmt.Errorf("%w: version %d", ErrSnapshotFormat, s.Version)
	}
	return s.Files, nil
}
