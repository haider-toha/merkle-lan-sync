package pathnorm

import "testing"

func TestNormalizeComponent_NFC(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"NFD to NFC", nfdResume, nfcResume},
		{"already NFC unchanged", nfcResume, nfcResume},
		{"ascii unchanged", "normal.txt", "normal.txt"},
		{"empty unchanged", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeComponent(tc.in); got != tc.want {
				t.Errorf("NormalizeComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsNFC(t *testing.T) {
	if IsNFC(nfdResume) {
		t.Errorf("IsNFC(NFD) = true, want false")
	}
	if !IsNFC(nfcResume) {
		t.Errorf("IsNFC(NFC) = false, want true")
	}
}
