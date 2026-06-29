package merkle

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/haider-toha/merkle-sync/internal/pathnorm"
	"github.com/haider-toha/merkle-sync/internal/protocol"
)

// Scan walks the directory tree rooted at absRoot and returns the FileInfo set for
// every regular file and symlink (directories are tree NODES, not leaves, and empty
// directories are not synced — CDD-8). Content hashes are pure file bytes, so the
// same folder scanned twice yields the identical set and therefore the identical
// root hash (WS-1 criterion 1). Symlinks are typed leaves whose content is the
// SHA-256 of the normalised forward-slash NFC target; they are NOT followed
// (symlink-following-on-apply antipattern). Version vectors are left empty: the
// initial scan is not authorship (SR-6, CDD-3); the reconcile layer (WS-4) seeds
// and bumps.
func Scan(absRoot string) ([]FileInfo, error) {
	var out []FileInfo
	err := filepath.WalkDir(absRoot, func(osPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("merkle: scan walk %s: %w", osPath, err)
		}
		if osPath == absRoot {
			return nil // the root itself is not a leaf
		}
		if d.IsDir() {
			return nil // descend, but a directory is a node, not a leaf
		}
		key, err := pathnorm.FromOSPath(absRoot, osPath, pathnorm.HostTarget())
		if err != nil {
			return fmt.Errorf("merkle: scan canonicalise %s: %w", osPath, err)
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("merkle: scan stat %s: %w", osPath, err)
		}

		fi := FileInfo{
			Path:      key,
			Mode:      uint32(info.Mode()),
			ModTimeNS: info.ModTime().UnixNano(),
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, lerr := os.Readlink(osPath)
			if lerr != nil {
				return fmt.Errorf("merkle: scan readlink %s: %w", osPath, lerr)
			}
			norm := pathnorm.NormalizeComponent(filepath.ToSlash(target))
			fi.Type = TypeSymlink
			fi.ContentHash = HashBytes([]byte(norm))
			fi.Size = uint64(len(norm))
		default:
			h, herr := HashFile(osPath)
			if herr != nil {
				return herr
			}
			fi.Type = TypeFile
			fi.ContentHash = h
			fi.Size = uint64(info.Size())
		}
		out = append(out, fi)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RescanCandidate is the cheap pre-filter WS-4's incremental rescan uses to decide
// whether a path needs re-hashing: if neither size nor mtime changed, the content
// is probably unchanged and the expensive content hash can be skipped. It is only a
// HINT (AL-11, SR-4): identity is ALWAYS content_hash, never size/mtime. The
// periodic full rescan (Scan, which always hashes) is the source of truth (SR-11),
// so a content change hidden behind an unchanged size+mtime is still caught by the
// next full scan — the pre-filter may skip a re-hash, it never decides identity.
func RescanCandidate(prev FileInfo, curSize uint64, curMTimeNS int64) bool {
	return prev.Size != curSize || prev.ModTimeNS != curMTimeNS
}

// SynthesizeDeletions reconciles a loaded snapshot (prev) against a fresh scan
// (cur) to recover deletions that happened while the daemon was down (MK-6, R-5).
// It returns cur augmented with tombstones:
//
//   - a live path in prev that is ABSENT from cur -> a synthesized tombstone
//     (SetDeleted bumps self's VV: a delete is a versioned local-authorship event);
//   - an existing tombstone in prev still absent from cur -> carried forward
//     UNCHANGED (never re-bumped — so a restart does not re-stamp a peer-authored
//     tombstone as a fresh local delete, CDD-7.1);
//   - a path present in cur -> taken from cur as-is (a path that reappeared over a
//     prev tombstone is a legitimate new local create; the tombstone is dropped).
//
// When prev is empty (a missing or corrupt snapshot), absence is genuinely
// ambiguous, so NO deletions are synthesized — create-only fallback (MK-6 step 3),
// which avoids manufacturing mass deletions on first run (mass-delete antipattern).
func SynthesizeDeletions(prev, cur []FileInfo, self protocol.ShortID) []FileInfo {
	if len(prev) == 0 {
		return cur
	}
	present := make(map[string]struct{}, len(cur))
	for _, fi := range cur {
		present[fi.Path] = struct{}{}
	}
	out := make([]FileInfo, len(cur), len(cur)+len(prev))
	copy(out, cur)
	for _, p := range prev {
		if _, ok := present[p.Path]; ok {
			continue // still on disk (live or re-created) — cur's entry wins
		}
		if p.Deleted {
			out = append(out, p) // carry the existing tombstone forward unchanged
			continue
		}
		out = append(out, p.SetDeleted(self)) // deleted while down -> new tombstone
	}
	return out
}
