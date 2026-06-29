package pathnorm

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Target selects the filesystem naming convention of a path's destination. It is
// an explicit parameter (not a runtime.GOOS gate) so the Windows escape path is
// exercised by tests running on the Mac (pathnorm-api-and-target-model decision).
type Target int

const (
	// Unix: POSIX naming — no Windows-unsafe escaping; '/' separator.
	Unix Target = iota
	// Windows: escape Windows-unsafe components reversibly; '\' separator.
	Windows
)

// HostTarget returns the Target matching the OS this binary runs on.
func HostTarget() Target {
	if runtime.GOOS == "windows" {
		return Windows
	}
	return Unix
}

func (t Target) sep() string {
	if t == Windows {
		return `\`
	}
	return "/"
}

// ErrNotCanonical is returned for an input that cannot be a canonical key: an
// absolute path, or one that escapes the sync root via "..".
var ErrNotCanonical = errors.New("pathnorm: path is absolute or escapes the root")

// stripVolumePrefix removes a leading \\?\ extended-length prefix, a \\ UNC
// prefix, or a drive-letter (C:) prefix from a forward-slash string, so the
// canonical key is always a clean relative path (XP-1, maxpath-longpath decision).
// The canonical key never stores such a prefix.
func stripVolumePrefix(s string) string {
	// \\?\  or  //?/  (after ToSlash)
	if len(s) >= 4 && (s[:4] == `//?/` || s[:4] == `\\?\`) {
		s = s[4:]
	}
	// drive letter "C:" possibly now at the front
	if len(s) >= 2 && s[1] == ':' && isDriveLetter(s[0]) {
		s = s[2:]
	}
	return s
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// canonFromComponents validates and NFC-normalises forward-slash components into a
// canonical key. It rejects an absolute path or any "../" escape (path-traversal
// defence, antipattern path-traversal-received-path); "." and empty components are
// dropped; the result is path.Clean'd.
func canonFromComponents(slash string) (string, error) {
	slash = stripVolumePrefix(slash)
	if strings.HasPrefix(slash, "/") {
		return "", fmt.Errorf("%w: %q", ErrNotCanonical, slash)
	}
	cleaned := path.Clean(slash)
	switch {
	case cleaned == ".":
		return "", nil // the root
	case cleaned == ".." || strings.HasPrefix(cleaned, "../"):
		return "", fmt.Errorf("%w: %q", ErrNotCanonical, slash)
	}
	parts := strings.Split(cleaned, "/")
	for i, p := range parts {
		parts[i] = NormalizeComponent(p)
	}
	return strings.Join(parts, "/"), nil
}

// Canonicalize converts a host OS-native RELATIVE path to a canonical key:
// forward-slash, NFC per component, prefix-stripped, cleaned. It does NOT unescape
// (the input is a logical name in canonical character space, not a Windows-escaped
// on-disk name — use FromOSPath for an escaped on-disk name). Returns ErrNotCanonical
// for an absolute or root-escaping path.
func Canonicalize(osRelPath string) (string, error) {
	return canonFromComponents(filepath.ToSlash(osRelPath))
}

// CanonicalizeSlash is Canonicalize for an already-forward-slash key (e.g. one
// arriving from the wire); it validates and NFC-normalises it. It is idempotent:
// CanonicalizeSlash(CanonicalizeSlash(x)) == CanonicalizeSlash(x).
func CanonicalizeSlash(slashRel string) (string, error) {
	return canonFromComponents(slashRel)
}

// ToOSPath builds the on-disk path for target, rooted at absRoot, from a canonical
// key. On a Windows target every component is escaped reversibly (EscapeForWindows);
// the separator and join follow target. The absRoot and separators are never
// escaped (per-component, platform-gated — CDD-6). For the host's real filesystem,
// pass HostTarget(): the result then matches filepath semantics and, because absRoot
// is absolute, Go's os.fixLongPath adds the \\?\ prefix itself when length requires
// it — we never hand-prepend it (maxpath-longpath decision).
func ToOSPath(absRoot, canonKey string, target Target) string {
	sep := target.sep()
	rel := canonKey
	if canonKey != "" {
		comps := strings.Split(canonKey, "/")
		if target == Windows {
			for i, c := range comps {
				comps[i] = EscapeForWindows(c)
			}
		}
		rel = strings.Join(comps, sep)
	}
	if absRoot == "" {
		return rel
	}
	root := strings.TrimRight(absRoot, sep)
	if rel == "" {
		return root
	}
	return root + sep + rel
}

// relativize strips absRoot from osPath in target's separator space.
func relativize(absRoot, osPath string, target Target) (string, error) {
	if target == HostTarget() {
		rel, err := filepath.Rel(absRoot, osPath)
		if err != nil {
			return "", fmt.Errorf("pathnorm: relativize %q against %q: %w", osPath, absRoot, err)
		}
		return filepath.ToSlash(rel), nil
	}
	sep := target.sep()
	root := strings.TrimRight(absRoot, sep)
	switch {
	case osPath == root:
		return "", nil
	case strings.HasPrefix(osPath, root+sep):
		rel := osPath[len(root)+len(sep):]
		return strings.ReplaceAll(rel, sep, "/"), nil
	default:
		return "", fmt.Errorf("pathnorm: %q is not under root %q", osPath, absRoot)
	}
}

// FromOSPath is the inverse of ToOSPath: it relativises osPath against absRoot,
// unescapes each component on a Windows target (UnescapeFromWindows), and
// NFC-normalises into a canonical key. It is the scanner's OS-path -> canonical-key
// function (call with HostTarget()), and the cross-target round-trip in tests
// (call with Windows on the Mac). Returns ErrNotCanonical if the result would
// escape the root.
func FromOSPath(absRoot, osPath string, target Target) (string, error) {
	rel, err := relativize(absRoot, osPath, target)
	if err != nil {
		return "", err
	}
	rel = stripVolumePrefix(rel)
	if rel == "" {
		return "", nil
	}
	comps := strings.Split(rel, "/")
	for i, c := range comps {
		if target == Windows {
			c = UnescapeFromWindows(c)
		}
		comps[i] = c
	}
	return canonFromComponents(strings.Join(comps, "/"))
}

// WouldExceedMaxPath reports whether the would-be Windows on-disk path for canonKey
// under absRoot exceeds MAX_PATH (260). It feeds the WS-4 refuse+flag fallback when
// Windows long-path support is not confirmed (maxpath-longpath decision). The
// actual >260 write behaviour is a Phase-6 windows-latest check.
func WouldExceedMaxPath(absRoot, canonKey string) bool {
	return len(ToOSPath(absRoot, canonKey, Windows)) > MaxPath
}
