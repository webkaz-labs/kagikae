package distribution

import "testing"

func TestMiseLowerBoundIsNumeric(t *testing.T) {
	for output, want := range map[string]string{
		"2026.9.3 macos-arm64 (2026-09-03)":  "2026.9.3",
		"2026.10.2 macos-arm64 (2026-10-04)": "2026.10.2", // lexically below 2026.9.3
		"2027.1.0 linux-x64":                 "2027.1.0",
		"2026.9.2 macos-arm64":               "",
		"2025.12.30 macos-arm64":             "",
		"2026.9 macos-arm64":                 "",
		"2026.9.3-rc1 macos-arm64":           "",
		"mise 2026.9.3":                      "",
		"":                                   "",
	} {
		got, err := MiseAtLeast(output, MinimumMise)
		if got != want || (err == nil) != (want != "") {
			t.Fatalf("%q: got %q err %v; want %q", output, got, err, want)
		}
	}
}
