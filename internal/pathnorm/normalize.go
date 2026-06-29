package pathnorm

import "golang.org/x/text/unicode/norm"

// NormalizeComponent returns the NFC (Normalization Form C, composed) form of a
// single path component. NFC is the canonical Unicode form for the tree, the wire,
// and the structural hash (XP-2, unicode-canonical-form decision): macOS/APFS is
// normalisation-preserving (it returns whatever form an app wrote — NFC or NFD),
// while Windows/NTFS and Linux/ext4 are normalisation-sensitive (NFC and NFD are
// two different files). Without one canonical form the same logical file shows up
// as two leaves and never converges (SR-5). norm.NFC.String is a pure function
// with deterministic, OS-independent Unicode tables, so Mac and Windows agree
// byte-for-byte.
//
// Normalisation is applied PER COMPONENT (never across '/') so the separator can
// never be affected.
func NormalizeComponent(s string) string {
	return norm.NFC.String(s)
}

// IsNFC reports whether s is already in NFC form (used in tests and the idempotent
// canonical-form check).
func IsNFC(s string) bool {
	return norm.NFC.IsNormalString(s)
}
