package pathnorm

import "golang.org/x/text/cases"

// folder is the shared Unicode full-case-fold transformer. cases.Fold is stateless
// and safe for concurrent use.
var folder = cases.Fold()

// Fold returns the case-folded, NFC-normalised form of a single path component.
// Two components whose Fold is equal would collide on a case-insensitive or
// normalisation-insensitive target (File.txt vs file.txt; resume.txt as NFC vs
// NFD, since both are NFC by the time they reach Fold). This is XP-4 *detection*
// only — keys stay case-sensitive NFC; the no-clobber enforcement is the
// filesystem's own verdict on the WS-4 apply path (CDD-5).
//
// CDD-5 note: this fold is not provably identical to NTFS $UpCase or APFS
// equality; it is a detection optimisation, and it errs toward over-detection
// (the safe direction), never toward missing a clash that then clobbers.
func Fold(component string) string {
	return folder.String(NormalizeComponent(component))
}

// FoldIndex detects collisions among a set of canonical keys (or, per directory,
// among component names) that share a folded form. It maps a folded key to the
// first concrete key seen for it.
type FoldIndex struct {
	m map[string]string
}

// NewFoldIndex returns an empty collision index.
func NewFoldIndex() *FoldIndex { return &FoldIndex{m: make(map[string]string)} }

// Add registers key. If a different key with the same folded form is already
// present, it returns that existing key and collision=true (the caller refuses +
// flags the second, never clobbers — XP-4); the index is left pointing at the
// first-seen key. Re-adding the identical key is not a collision.
func (fi *FoldIndex) Add(key string) (existing string, collision bool) {
	f := Fold(key)
	if prev, ok := fi.m[f]; ok {
		if prev == key {
			return "", false
		}
		return prev, true
	}
	fi.m[f] = key
	return "", false
}

// Len reports how many distinct folded keys are registered.
func (fi *FoldIndex) Len() int { return len(fi.m) }
