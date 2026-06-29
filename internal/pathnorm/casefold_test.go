package pathnorm

import "testing"

func TestFold_CaseAndNormCollision(t *testing.T) {
	// Case variants fold to the same key (XP-4).
	if Fold("File.txt") != Fold("file.txt") || Fold("file.txt") != Fold("FILE.TXT") {
		t.Errorf("case variants did not fold equal: %q %q %q",
			Fold("File.txt"), Fold("file.txt"), Fold("FILE.TXT"))
	}
	// NFC and NFD of the same name fold equal (Fold NFC-normalises first), so the
	// one index catches both case AND normalisation collisions.
	if Fold(nfdResume) != Fold(nfcResume) {
		t.Errorf("NFD/NFC did not fold equal: %q vs %q", Fold(nfdResume), Fold(nfcResume))
	}
	// Distinct names do NOT fold equal.
	if Fold("alpha.txt") == Fold("beta.txt") {
		t.Errorf("distinct names folded equal")
	}
}

func TestFoldIndex_DetectsCollision(t *testing.T) {
	idx := NewFoldIndex()

	if existing, collision := idx.Add("File.txt"); collision {
		t.Errorf("first Add reported a collision with %q", existing)
	}
	// A case-only variant collides with the first.
	existing, collision := idx.Add("file.txt")
	if !collision {
		t.Errorf("case-variant Add did not report a collision")
	}
	if existing != "File.txt" {
		t.Errorf("collision existing = %q, want %q", existing, "File.txt")
	}
	// A normalisation variant of an existing key also collides.
	if _, collision := idx.Add(nfdResume); collision {
		t.Errorf("first résumé Add reported a collision")
	}
	if existing, collision := idx.Add(nfcResume); !collision || existing != nfdResume {
		t.Errorf("NFC variant collision = (%q,%v), want (%q,true)", existing, collision, nfdResume)
	}
	// Re-adding the identical key is not a collision.
	if _, collision := idx.Add("File.txt"); collision {
		t.Errorf("re-adding identical key reported a collision")
	}
	// A genuinely distinct key is fine.
	if _, collision := idx.Add("unique.bin"); collision {
		t.Errorf("distinct key reported a collision")
	}
	if idx.Len() != 3 { // {file.txt fold, résumé fold, unique.bin fold}
		t.Errorf("FoldIndex.Len() = %d, want 3", idx.Len())
	}
}
