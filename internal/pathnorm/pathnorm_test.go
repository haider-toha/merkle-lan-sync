package pathnorm

import (
	"errors"
	"strings"
	"testing"
)

const (
	nfdResume = "résumé.txt" // e + combining acute (NFD)
	nfcResume = "résumé.txt"   // é precomposed (NFC)
)

func TestCanonicalize_NFC(t *testing.T) {
	got, err := CanonicalizeSlash(nfdResume)
	if err != nil {
		t.Fatalf("CanonicalizeSlash(NFD) error: %v", err)
	}
	if got != nfcResume {
		t.Errorf("CanonicalizeSlash(NFD) = %q, want NFC %q", got, nfcResume)
	}
	if !IsNFC(got) {
		t.Errorf("result %q is not NFC", got)
	}
	// idempotent
	again, _ := CanonicalizeSlash(got)
	if again != got {
		t.Errorf("not idempotent: %q -> %q", got, again)
	}
}

func TestCanonicalize_SlashAndClean(t *testing.T) {
	cases := map[string]string{
		"a/b/c.txt": "a/b/c.txt",
		"a//b":      "a/b",
		"./a/b":     "a/b",
		"a/./b":     "a/b",
		"a/b/":      "a/b",
		"a/c/../b":  "a/b", // interior .. that does not escape collapses
		".":         "",    // the root
		"":          "",
		"dir":       "dir",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := CanonicalizeSlash(in)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if got != want {
				t.Errorf("CanonicalizeSlash(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestCanonicalize_RejectsTraversal(t *testing.T) {
	bad := []string{"../x", "..", "a/../../b", "/abs/path", "/", "../../etc/passwd"}
	for _, in := range bad {
		t.Run(in, func(t *testing.T) {
			if _, err := CanonicalizeSlash(in); !errors.Is(err, ErrNotCanonical) {
				t.Errorf("CanonicalizeSlash(%q) err = %v, want ErrNotCanonical", in, err)
			}
		})
	}
}

func TestStripVolumePrefix(t *testing.T) {
	cases := map[string]string{
		`//?/C:/sync/a`: `/sync/a`, // \\?\ (slashified) + drive stripped
		`C:/sync/a`:     `/sync/a`,
		`a/b`:           `a/b`,
		`d:relative`:    `relative`,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := stripVolumePrefix(in); got != want {
				t.Errorf("stripVolumePrefix(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// pathRoundTripComponents are single canonical components (no '/') exercised in the
// full Mac->wire->Windows->wire->Mac path round-trip. "fwd/slash" is intentionally
// excluded — '/' is the canonical separator and can never be inside a component.
var pathRoundTripComponents = []string{
	`a:b`, `a*b`, `what?.txt`, `a|b`, `a"b`, `back\slash`,
	"ctrl\x01char", "tab\tinside", "nul\x00byte",
	`CON`, `con`, `Con.txt`, `NUL.tar.gz`, `COM1.txt`, "COM¹", `AUX`,
	`trailingdot.`, `trailingspace `, `dots...`, `mix. `,
	`normal.txt`, `100%done`, nfcResume, `a%3Ab`, `already%20escaped`,
}

// TestWindowsHostileRoundTrip is the SR-13 acceptance gate: a Windows-hostile name
// under a normal parent survives Canonicalize -> ToOSPath(Windows) ->
// FromOSPath(Windows) -> CanonicalizeSlash with the canonical key preserved, the
// escaped OS form is Windows-legal per component, and the result is idempotent.
// Runs on the Mac via the explicit Windows Target.
func TestWindowsHostileRoundTrip(t *testing.T) {
	const winRoot = `C:\sync`
	parents := []string{"", "dir", "a/b"}
	for _, parent := range parents {
		for _, comp := range pathRoundTripComponents {
			logical := comp
			if parent != "" {
				logical = parent + "/" + comp
			}
			name := parent + "|" + comp
			t.Run(name, func(t *testing.T) {
				key, err := CanonicalizeSlash(logical)
				if err != nil {
					t.Fatalf("CanonicalizeSlash(%q): %v", logical, err)
				}
				osPath := ToOSPath(winRoot, key, Windows)
				// every escaped component (after the root) must be Windows-legal
				rel := strings.TrimPrefix(osPath, winRoot+`\`)
				for _, escComp := range strings.Split(rel, `\`) {
					if IsWindowsUnsafe(escComp) {
						t.Errorf("escaped component %q (from key %q) is Windows-unsafe; osPath=%q", escComp, key, osPath)
					}
				}
				back, err := FromOSPath(winRoot, osPath, Windows)
				if err != nil {
					t.Fatalf("FromOSPath(%q): %v", osPath, err)
				}
				if back != key {
					t.Errorf("key not preserved: key=%q osPath=%q back=%q", key, osPath, back)
				}
				if again, _ := CanonicalizeSlash(back); again != key {
					t.Errorf("not idempotent: key=%q back=%q again=%q", key, back, again)
				}
			})
		}
	}
}

func TestToOSPath_UnixTargetNoEscape(t *testing.T) {
	// On a Unix target, components are not escaped; the separator is '/'.
	got := ToOSPath("/home/u/sync", "a:b/c.txt", Unix)
	want := "/home/u/sync/a:b/c.txt"
	if got != want {
		t.Errorf("ToOSPath(Unix) = %q, want %q", got, want)
	}
}

func TestToOSPath_WindowsSeparatorAndEscape(t *testing.T) {
	got := ToOSPath(`C:\sync`, "dir/a:b", Windows)
	want := `C:\sync\dir\a%3Ab`
	if got != want {
		t.Errorf("ToOSPath(Windows) = %q, want %q", got, want)
	}
}

func TestFromOSPath_HostRoundTrip(t *testing.T) {
	// On the host target, a real OS path under the root relativises and canonicalises.
	root := t.TempDir()
	osPath := ToOSPath(root, "sub/file.txt", HostTarget())
	key, err := FromOSPath(root, osPath, HostTarget())
	if err != nil {
		t.Fatalf("FromOSPath: %v", err)
	}
	if key != "sub/file.txt" {
		t.Errorf("host round-trip = %q, want %q", key, "sub/file.txt")
	}
}

func TestWouldExceedMaxPath(t *testing.T) {
	short := "a/b/c.txt"
	if WouldExceedMaxPath(`C:\sync`, short) {
		t.Errorf("short path flagged as exceeding MAX_PATH")
	}
	long := strings.Repeat("longcomponent/", 30) + "file.txt" // > 260 under the root
	if !WouldExceedMaxPath(`C:\sync`, long) {
		t.Errorf("long path not flagged as exceeding MAX_PATH (len=%d)", len(ToOSPath(`C:\sync`, long, Windows)))
	}
}
