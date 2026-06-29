package pathnorm

import (
	"fmt"
	"strings"
)

// MaxPath is the classic Windows MAX_PATH limit (260). Beyond it a path needs the
// \\?\ extended prefix, which Go's os.fixLongPath adds itself when needed — we
// never hand-prepend it (maxpath-longpath-handling decision). WouldExceedMaxPath
// feeds the WS-4 refuse+flag fallback when long-path support is not confirmed.
const MaxPath = 260

// reservedChars are the characters Windows forbids in a filename component
// (Microsoft, Naming Files, Paths, and Namespaces). '/' and '\' are included: they
// can never appear inside a canonical component anyway (components are split on
// '/'), but escaping them defends a component that somehow carries one. ':' is the
// most dangerous — name:stream addresses an NTFS alternate data stream, so a
// literal a:b writes a *stream* of a, not a file a:b (filename-legality finding).
const reservedChars = `<>:"/\|?*`

// reservedStems is the set of Windows reserved device names, compared
// case-insensitively against a component's stem (the part before the first '.').
// "NUL.txt" and "NUL.tar.gz" are both equivalent to "NUL". The ISO/IEC 8859-1
// superscripts (U+00B9 ¹, U+00B2 ², U+00B3 ³) are recognised by Windows as digits
// in COM#/LPT# device names.
var reservedStems = func() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("COM%d", i)] = true
		m[fmt.Sprintf("LPT%d", i)] = true
	}
	for _, sup := range []string{"¹", "²", "³"} {
		m["COM"+sup] = true
		m["LPT"+sup] = true
	}
	return m
}()

func isReservedChar(b byte) bool { return strings.IndexByte(reservedChars, b) >= 0 }

// isControl reports whether b is NUL or an ASCII control character (1-31).
func isControl(b byte) bool { return b <= 31 }

// stemOf returns the part of name before its first '.' (the device-name stem).
func stemOf(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i]
	}
	return name
}

// isReservedStem reports whether name's stem is a Windows reserved device name
// (case-insensitive). strings.ToUpper is ASCII-correct for the reserved stems and
// leaves the superscripts (which uppercase to themselves) intact.
func isReservedStem(name string) bool {
	return reservedStems[strings.ToUpper(stemOf(name))]
}

// IsWindowsUnsafe reports whether a single path component cannot be written
// verbatim on Windows. It is computed on EVERY OS (so a Mac can predict the
// escaped form and a Windows peer escapes deterministically). A component is unsafe
// if it (a) contains a reserved character, (b) contains a control character,
// (c) is a reserved device stem, or (d) ends in a space or a period. (XP-3,
// illegal-name-strategy decision.)
func IsWindowsUnsafe(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; isReservedChar(c) || isControl(c) {
			return true
		}
	}
	if isReservedStem(name) {
		return true
	}
	last := name[len(name)-1]
	return last == ' ' || last == '.'
}

func hex2(b byte) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{'%', digits[b>>4], digits[b&0x0F]})
}

// EscapeForWindows transforms a single path component into a reversible,
// Windows-legal on-disk form. The canonical tree key keeps the ORIGINAL name; only
// the on-disk form on a Windows target is escaped, so SR-13 (same canonical key on
// both OSes) holds while Windows still gets a writable name.
//
// The scheme is total and reversible:
//  1. every literal '%' becomes "%25" first, so every '%' in the output begins a
//     2-hex escape and decode is unambiguous;
//  2. each reserved or control byte becomes '%' + two uppercase hex digits;
//  3. a trailing space or period is escaped (the final byte becomes "%XX"), so the
//     on-disk name ends in a legal hex digit;
//  4. a reserved device stem has its first byte escaped (CON -> %43ON), so it no
//     longer matches the reserved set yet still decodes back.
//
// UTF-8 multi-byte sequences are preserved untouched (their bytes are all >= 0x80,
// never reserved/control/'%'), so a legal Unicode name like resume.txt (NFC)
// passes through unchanged.
func EscapeForWindows(name string) string {
	if name == "" {
		return name
	}
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '%':
			b.WriteString("%25")
		case isReservedChar(c) || isControl(c):
			b.WriteString(hex2(c))
		default:
			b.WriteByte(c)
		}
	}
	s := b.String()
	// (3) trailing space/period: escape the final byte. One pass suffices because
	// the replacement makes the new final byte a hex digit (never ' ' or '.').
	if n := len(s); n > 0 && (s[n-1] == ' ' || s[n-1] == '.') {
		s = s[:n-1] + hex2(s[n-1])
	}
	// (4) reserved device stem: escape the first byte (always ASCII for these
	// stems). Guard s[0] != '%' so we never double-escape.
	if isReservedStem(name) && len(s) > 0 && s[0] != '%' {
		s = hex2(s[0]) + s[1:]
	}
	return s
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// UnescapeFromWindows reverses EscapeForWindows, decoding every "%XX" triplet back
// to its byte. A '%' not followed by two hex digits is left literal (robust on
// arbitrary input), but a value produced by EscapeForWindows always decodes exactly
// (Unescape(Escape(x)) == x), which also makes Escape injective.
func UnescapeFromWindows(s string) string {
	if !strings.ContainsRune(s, '%') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if h, ok1 := unhex(s[i+1]); ok1 {
				if l, ok2 := unhex(s[i+2]); ok2 {
					b.WriteByte(h<<4 | l)
					i += 2
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
