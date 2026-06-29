package pathnorm

import "testing"

// hostileNames is the Windows-hostile test table (filename-legality finding, XP-3
// acceptance). Every entry is a single path component. The "must NOT change" group
// is already Windows-legal and must pass EscapeForWindows unchanged.
var hostileNames = []string{
	// reserved characters
	`a:b`, `a*b`, `what?.txt`, `a|b`, `a"b`, `a<b>c`, `back\slash`, `fwd/slash`,
	// control characters (incl. NUL and tab)
	"ctrl\x01char", "tab\tinside", "nul\x00byte", "high\x1fbyte",
	// reserved device stems (case-insensitive, stem-only, incl. extension + superscript)
	`CON`, `con`, `Con.txt`, `NUL`, `NUL.tar.gz`, `COM1`, `COM1.txt`, `LPT9`,
	"COM¹", `AUX`, `PRN.log`,
	// trailing dot / space
	`trailingdot.`, `trailingspace `, `dots...`, `mix. `, `. `,
	// must NOT change (already legal)
	`.hiddenleadingdot`, `normal.txt`, `100%done`, `résumé.txt`, `a.b.c`,
	// escape-lookalikes — must survive losslessly without aliasing a real escape
	`a%3Ab`, `already%20escaped`, `%43ON`, `100%25done`,
}

func TestIsWindowsUnsafe(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{`a:b`, true}, {`a*b`, true}, {`q?`, true}, {`a|b`, true}, {`a"b`, true},
		{`a<b`, true}, {`a>b`, true}, {`back\slash`, true}, {`fwd/slash`, true},
		{"ctrl\x01char", true}, {"\x00", true}, {"tab\ttab", true},
		{`CON`, true}, {`con`, true}, {`Con.txt`, true}, {`NUL`, true},
		{`NUL.tar.gz`, true}, {`COM1`, true}, {`COM9.txt`, true}, {`LPT3`, true},
		{"COM¹", true}, {`AUX`, true}, {`PRN`, true},
		{`name.`, true}, {`name `, true}, {`.`, true}, {` `, true},
		// safe
		{``, false}, {`normal.txt`, false}, {`.hiddenleadingdot`, false},
		{`résumé.txt`, false}, {`100%done`, false}, {`COMfoo`, false},
		{`CONtext`, false}, {`a.CON`, false}, {`COM10`, false}, {`COM0`, false},
		{`LPT`, false}, {`leading.dot.ok`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsWindowsUnsafe(tc.name); got != tc.want {
				t.Errorf("IsWindowsUnsafe(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestEscape_RoundTripAndLegal: every hostile component round-trips
// (Unescape(Escape(x)) == x) and its escaped form is Windows-legal (XP-3, SR-13).
func TestEscape_RoundTripAndLegal(t *testing.T) {
	for _, in := range hostileNames {
		t.Run(in, func(t *testing.T) {
			esc := EscapeForWindows(in)
			if back := UnescapeFromWindows(esc); back != in {
				t.Errorf("round-trip failed: in=%q esc=%q back=%q", in, esc, back)
			}
			if IsWindowsUnsafe(esc) {
				t.Errorf("escaped form is still Windows-unsafe: in=%q esc=%q", in, esc)
			}
		})
	}
}

// TestEscape_Injective: Escape is injective — no two distinct inputs share an
// output (plan WS-1 criterion 2: "escaping total/injective"). The left-inverse
// Unescape(Escape(x))==x already entails this; we assert it directly over the
// hostile table plus escape-lookalikes as a regression guard.
func TestEscape_Injective(t *testing.T) {
	seen := make(map[string]string)
	for _, in := range hostileNames {
		esc := EscapeForWindows(in)
		if prev, ok := seen[esc]; ok && prev != in {
			t.Errorf("non-injective: %q and %q both escape to %q", prev, in, esc)
		}
		seen[esc] = in
	}
}

// TestEscape_PercentFirst pins the load-bearing detail that '%' is escaped to %25
// FIRST, so every '%' in the output begins a real escape and decode is unambiguous.
func TestEscape_PercentFirst(t *testing.T) {
	cases := map[string]string{
		`100%done`:     `100%25done`,
		`a%3Ab`:        `a%253Ab`, // literal "%3A" must not be confused with an escape of ':'
		`a:b`:          `a%3Ab`,
		`%43ON`:        `%2543ON`,
		`back\slash`:   `back%5Cslash`,
		`trailingdot.`: `trailingdot%2E`,
		`CON`:          `%43ON`,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := EscapeForWindows(in); got != want {
				t.Errorf("EscapeForWindows(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// TestUnescape_RobustOnGarbage: an incomplete or non-hex '%' is left literal
// (Unescape never panics or over-reads on adversarial input).
func TestUnescape_RobustOnGarbage(t *testing.T) {
	cases := map[string]string{
		`%`:          `%`,
		`%2`:         `%2`,
		`%2G`:        `%2G`,
		`%ZZ`:        `%ZZ`,
		`ab%`:        `ab%`,
		`%2E`:        `.`,
		`no-percent`: `no-percent`,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := UnescapeFromWindows(in); got != want {
				t.Errorf("UnescapeFromWindows(%q) = %q, want %q", in, got, want)
			}
		})
	}
}
