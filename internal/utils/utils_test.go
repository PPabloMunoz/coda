package utils

import (
	"errors"
	"testing"
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
