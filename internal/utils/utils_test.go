package utils

import (
	"errors"
	"testing"
	"unicode/utf8"
)

type validMBIDTest struct {
	name    string
	id      string
	wantErr error
}

func TestValidMBID(t *testing.T) {
	tests := []validMBIDTest{
		{"valid", "d5b32d03-2aaf-4baf-ba5d-3721d095b833", nil},
		{"too short", "12345", ErrInvalidMBID},
		{"empty", "", ErrInvalidMBID},
		{"uppercase rejected", "D5B32D03-2AAF-4BAF-BA5D-3721D095B833", ErrInvalidMBID},
		{"missing dashes", "d5b32d032aaf4bafba5d3721d095b833", ErrInvalidMBID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMBID(tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateMBID(%q) = %v, want %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

type nullIfEmptyTest struct {
	name string
	in   string
	want *string
}

func TestNullIfEmpty(t *testing.T) {
	tests := []nullIfEmptyTest{
		{"empty", "", nil},
		{"spaces", "   ", nil},
		{"trimmed", " a ", new("a")},
		{"plain", "a", new("a")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NullIfEmpty(tt.in)
			if got == nil || tt.want == nil {
				if got != tt.want {
					t.Errorf("NullIfEmpty(%q) = %v, want %v", tt.in, got, tt.want)
				}
				return
			}
			if *got != *tt.want {
				t.Errorf("NullIfEmpty(%q) = %q, want %q", tt.in, *got, *tt.want)
			}
		})
	}
}

func TestSanitizeSegment(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"spaces only", "   ", ""},
		{"plain", "Abbey Road", "Abbey_Road"},
		{"slashes become underscore", "AC/DC", "AC_DC"},
		{"backslash becomes underscore", `a\b`, "a_b"},
		{"illegal chars", `a:b*c?d"e<f>g|h`, "a_b_c_d_e_f_g_h"},
		{"control chars", "a\x01b\x7fc", "a_b_c"},
		{"trims dots", ".hidden.", "hidden"},
		{"collapses underscores", "a__b___c", "a_b_c"},
		{"leading/trailing spaces trimmed", "  hello world  ", "hello_world"},
		{"reserved bare", "CON", "CON_"},
		{"reserved lower", "nul", "nul_"},
		{"reserved with ext", "COM1.txt", "COM1_.txt"},
		{"reserved dotted", "aux.foo.bar", "aux_.foo.bar"},
		{"non-reserved prefix", "Console", "Console"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeSegment(tt.in); got != tt.want {
				t.Errorf("SanitizeSegment(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeSegmentMaxLength(t *testing.T) {
	long := ""
	for range 150 {
		long += "a"
	}
	got := SanitizeSegment(long)
	if len(got) != 100 {
		t.Errorf("len = %d, want 100 bytes", len(got))
	}
	if got == "" {
		t.Error("expected non-empty truncated segment")
	}
}

func TestSanitizeSegmentByteCap(t *testing.T) {
	// 150 CJK characters: 150 runes but 450 bytes. Must fit in 100 bytes
	// without splitting UTF-8 and without exceeding NAME_MAX.
	long := ""
	for range 150 {
		long += "あ" // 3 bytes in UTF-8
	}
	got := SanitizeSegment(long)
	if len(got) > 100 {
		t.Errorf("len = %d bytes, want <= 100", len(got))
	}
	if got == "" {
		t.Fatal("expected non-empty truncated segment")
	}
	for _, r := range got {
		if r == utf8.RuneError {
			t.Fatalf("truncated mid-rune: %q", got)
		}
	}
	// 33 full 3-byte runes = 99 bytes; the 34th would exceed 100.
	if want := 99; len(got) != want {
		t.Errorf("len = %d, want %d (whole runes only)", len(got), want)
	}

	emoji := ""
	for range 40 {
		emoji += "😀" // 4 bytes in UTF-8
	}
	got = SanitizeSegment(emoji)
	if len(got) > 100 {
		t.Errorf("emoji len = %d bytes, want <= 100", len(got))
	}
	if len(got)%4 != 0 {
		t.Errorf("emoji len = %d, want a multiple of 4 (whole runes)", len(got))
	}
}

func TestGetFileFormat(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"mp3", "/music/song.mp3", "mp3"},
		{"uppercase", "/music/song.FLAC", "flac"},
		{"no extension", "/music/song", ""},
		{"dotfile", "/music/.hidden", "hidden"},
		{"multiple dots", "/music/archive.tar.gz", "gz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetFileFormat(tt.path); got != tt.want {
				t.Errorf("GetFileFormat(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsTruthy(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "yes", "Yes", "on", "ON", "  true  "}
	for _, in := range truthy {
		if !isTruthy(in) {
			t.Errorf("isTruthy(%q) = false, want true", in)
		}
	}
	falsy := []string{"", "0", "false", "no", "off", "maybe", "  "}
	for _, in := range falsy {
		if isTruthy(in) {
			t.Errorf("isTruthy(%q) = true, want false", in)
		}
	}
}
