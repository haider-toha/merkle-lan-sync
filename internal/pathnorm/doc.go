// Package pathnorm is the canonical path / Unicode / case layer of Merkle Sync —
// the Mac<->Windows boundary that makes "the same logical file maps to the same
// canonical key and the same hash on both operating systems" true (SR-13, the
// substrate for SR-5 convergence).
//
// # Canonical key
//
// Every path stored in the tree, used as a map key, or sent on the wire is a
// canonical key: forward-slash, relative to the sync root, NFC-normalised per
// component (XP-1, XP-2, GR-12). The root is the only absolute path. Canonicalize
// converts a host OS-native relative path to a canonical key; CanonicalizeSlash
// does the same for an already-forward-slash key arriving from the wire. Both
// strip any \\?\ / UNC / drive-letter prefix, reject absolute or root-escaping
// (..) paths, and normalise each component to NFC. macOS hands back filenames in
// whatever form an app wrote (APFS is normalisation-preserving), so normalising at
// the boundary is mandatory, not optional (unicode-canonical-form decision).
//
// # OS boundary and Windows-unsafe names
//
// ToOSPath / FromOSPath convert between a canonical key and the on-disk path for a
// Target (Unix or Windows). On a Windows target every path component is escaped to
// a reversible, Windows-legal on-disk form (EscapeForWindows): reserved characters
// (< > : " / \ | ? *), control characters, reserved device stems
// (CON/PRN/AUX/NUL/COM#/LPT#), and trailing dot/space are percent-escaped; the
// canonical key always keeps the original NFC name, so a Mac->Windows->Mac
// round-trip is lossless (illegal-name-strategy decision, XP-3). Escaping is total
// and injective: % is escaped to %25 first, so Unescape(Escape(x)) == x.
//
// The Target is an explicit parameter, not a runtime.GOOS gate, so the Windows
// escape path is exercised by the test suite running on the Mac — only the actual
// on-disk write of an escaped name is a Phase-6 windows-latest tail.
//
// # Case / normalisation collisions
//
// Fold and FoldIndex implement the XP-4 detection mechanism: keys stay
// case-sensitive NFC, and a side fold index (cases.Fold of the NFC name) detects
// two keys that would collide on a case-insensitive / normalisation-insensitive
// target (File.txt vs file.txt; resume.txt as NFC vs NFD). Detection only — the
// no-clobber enforcement is the filesystem's own verdict on the WS-4 apply path
// (CDD-5).
package pathnorm
