package merkle

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSnapshot_RoundTrip — the local snapshot persists and reloads the FileInfo set
// (including VV + deleted), the substrate for deletion-across-restart (MK-6).
func TestSnapshot_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.snapshot")
	set := []FileInfo{
		leaf("a/b.txt", "x"),
		func() FileInfo { return leaf("gone.txt", "y").SetDeleted(2) }(),
		{Path: "empty-vv", Type: TypeFile},
	}
	if err := SaveSnapshot(path, set); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, set) {
		t.Errorf("snapshot round-trip mismatch:\n got %+v\nwant %+v", got, set)
	}
}

// TestSnapshot_MissingReturnsNil — a missing snapshot is (nil, nil), which drives
// the create-only fallback (no synthesized deletions).
func TestSnapshot_MissingReturnsNil(t *testing.T) {
	got, err := LoadSnapshot(filepath.Join(t.TempDir(), "does-not-exist.snapshot"))
	if err != nil {
		t.Errorf("missing snapshot returned error: %v", err)
	}
	if got != nil {
		t.Errorf("missing snapshot returned %v, want nil", got)
	}
}

// TestSnapshot_CorruptRejected — a corrupt snapshot is ErrSnapshotFormat (treated
// like missing: conservative create-only, never a crash).
func TestSnapshot_CorruptRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.snapshot")
	if err := os.WriteFile(path, []byte("not a gob stream at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(path); !errors.Is(err, ErrSnapshotFormat) {
		t.Errorf("corrupt snapshot err = %v, want ErrSnapshotFormat", err)
	}
}

// TestSnapshot_AtomicNoTempLeft — a successful save leaves no temp file behind, and
// re-saving overwrites cleanly (SR-1/SR-2 hygiene for local state).
func TestSnapshot_AtomicNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.snapshot")
	for i := 0; i < 3; i++ {
		if err := SaveSnapshot(path, []FileInfo{leaf("f.txt", "v")}); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || (len(e.Name()) > 9 && e.Name()[:9] == ".snapshot") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly the snapshot file, got %d entries: %v", len(entries), entries)
	}
}
