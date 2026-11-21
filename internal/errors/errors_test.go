package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
)

func TestSentinels(t *testing.T) {
	cases := []struct {
		err     *AppError
		code    string
		message string
	}{
		{ErrNotFound, "NOT_FOUND", "Resource not found"},
		{ErrUnauthorized, "UNAUTHORIZED", "Authentication required"},
		{ErrForbidden, "FORBIDDEN", "Access denied"},
		{ErrAlreadyExists, "ALREADY_EXISTS", "Resource already exists"},
		{ErrInvalidID, "INVALID_ID", "The id must be a valid uuid"},
		{ErrInvalidInput, "INVALID_INPUT", "Invalid input"},
		{ErrMusicBrainzFailed, "MUSICBRAINZ_FAILED", "MusicBrainz failed"},
		{ErrInternal, "INTERNAL_ERROR", "An unexpected error occurred"},
	}
	for _, tc := range cases {
		if tc.err.Code != tc.code {
			t.Errorf("Code = %q, want %q", tc.err.Code, tc.code)
		}
		if tc.err.Message != tc.message {
			t.Errorf("Message = %q, want %q", tc.err.Message, tc.message)
		}
		if tc.err.Error() != tc.message {
			t.Errorf("Error() = %q, want %q", tc.err.Error(), tc.message)
		}
		if tc.err.Unwrap() != nil {
			t.Errorf("sentinel Unwrap() = %v, want nil", tc.err.Unwrap())
		}
	}
}

func TestErrorDoesNotExposeCause(t *testing.T) {
	cause := stderrors.New("super secret db details")
	err := Wrap(ErrInternal, cause)
	if err.Error() != "An unexpected error occurred" {
		t.Errorf("Error() = %q, want safe message only", err.Error())
	}
	if strings.Contains(err.Error(), "super secret") {
		t.Errorf("Error() leaks cause: %q", err.Error())
	}
}

func TestWrapPreservesCodeAndMessage(t *testing.T) {
	cause := stderrors.New("disk exploded")
	wrapped := Wrap(ErrNotFound, cause)

	if wrapped == ErrNotFound {
		t.Error("Wrap must return a new instance, not the sentinel itself")
	}
	if wrapped.Code != ErrNotFound.Code {
		t.Errorf("Code = %q, want %q", wrapped.Code, ErrNotFound.Code)
	}
	if wrapped.Message != ErrNotFound.Message {
		t.Errorf("Message = %q, want %q", wrapped.Message, ErrNotFound.Message)
	}
	if !stderrors.Is(wrapped.Unwrap(), cause) {
		t.Errorf("Unwrap() = %v, want %v", wrapped.Unwrap(), cause)
	}

	// Sentinel must be untouched: no global mutable state.
	if ErrNotFound.Cause != nil {
		t.Errorf("sentinel mutated: Cause = %v, want nil", ErrNotFound.Cause)
	}
}

func TestWrapNilCause(t *testing.T) {
	wrapped := Wrap(ErrNotFound, nil)
	if wrapped == nil {
		t.Fatal("Wrap(err, nil) = nil, want copy preserving code")
	}
	if wrapped.Code != "NOT_FOUND" || wrapped.Message != "Resource not found" {
		t.Errorf("got %+v, want code/message preserved", wrapped)
	}
	if wrapped.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", wrapped.Unwrap())
	}
	if !stderrors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is(wrap(nil cause), ErrNotFound) = false, want true")
	}
}

func TestIsCompatibility(t *testing.T) {
	cause := stderrors.New("boom")
	wrapped := Wrap(ErrNotFound, cause)

	if !stderrors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is(wrapped, ErrNotFound) = false, want true")
	}
	if stderrors.Is(wrapped, ErrInternal) {
		t.Error("errors.Is(wrapped, ErrInternal) = true, want false")
	}
	if !Is(wrapped, ErrNotFound) {
		t.Error("Is(wrapped, ErrNotFound) = false, want true")
	}

	// Works through an additional fmt %w layer.
	double := fmt.Errorf("handler context: %w", wrapped)
	if !stderrors.Is(double, ErrNotFound) {
		t.Error("errors.Is(fmt-wrapped, ErrNotFound) = false, want true")
	}

	// Reverse direction also matches by code.
	if !stderrors.Is(ErrNotFound, wrapped) {
		t.Error("errors.Is(sentinel, wrapped) = false, want true")
	}
}

func TestAsCompatibility(t *testing.T) {
	cause := stderrors.New("db down")
	err := fmt.Errorf("service call: %w", Wrap(ErrForbidden, cause))

	var appErr *AppError
	if !stderrors.As(err, &appErr) {
		t.Fatal("errors.As = false, want true")
	}
	if appErr.Code != "FORBIDDEN" || appErr.Message != "Access denied" {
		t.Errorf("got %+v, want FORBIDDEN/Access denied", appErr)
	}
	if !stderrors.Is(appErr.Unwrap(), cause) {
		t.Errorf("cause = %v, want %v", appErr.Unwrap(), cause)
	}

	var appErr2 *AppError
	if !As(err, &appErr2) {
		t.Fatal("As helper = false, want true")
	}
	if appErr2.Code != "FORBIDDEN" {
		t.Errorf("Code = %q, want FORBIDDEN", appErr2.Code)
	}
}

func TestNilHandling(t *testing.T) {
	if Wrap(nil, stderrors.New("x")) != nil {
		t.Error("Wrap(nil, err) != nil, want nil")
	}
	if Wrap(nil, nil) != nil {
		t.Error("Wrap(nil, nil) != nil, want nil")
	}

	var nilErr *AppError
	if nilErr.Error() != "" {
		t.Errorf("nil Error() = %q, want empty", nilErr.Error())
	}
	if nilErr.Unwrap() != nil {
		t.Errorf("nil Unwrap() = %v, want nil", nilErr.Unwrap())
	}

	if Is(nil, ErrNotFound) {
		t.Error("Is(nil, ErrNotFound) = true, want false")
	}
	if As(nil, new(*AppError)) {
		t.Error("As(nil, …) = true, want false")
	}
	if _, ok := Get(nil); ok {
		t.Error("Get(nil) = true, want false")
	}
}

func TestGet(t *testing.T) {
	t.Run("direct sentinel", func(t *testing.T) {
		got, ok := Get(ErrInvalidInput)
		if !ok || got == nil {
			t.Fatal("Get(sentinel) = (nil, false), want match")
		}
		if got.Code != "INVALID_INPUT" {
			t.Errorf("Code = %q, want INVALID_INPUT", got.Code)
		}
	})

	t.Run("wrapped chain", func(t *testing.T) {
		cause := stderrors.New("constraint violation")
		err := fmt.Errorf("store insert: %w", Wrap(ErrAlreadyExists, cause))
		got, ok := Get(err)
		if !ok {
			t.Fatal("Get = false, want true")
		}
		if got.Code != "ALREADY_EXISTS" || got.Message != "Resource already exists" {
			t.Errorf("got %+v, want ALREADY_EXISTS preserved", got)
		}
		if !stderrors.Is(got.Cause, cause) {
			t.Errorf("cause = %v, want %v", got.Cause, cause)
		}
	})

	t.Run("no app error", func(t *testing.T) {
		err := fmt.Errorf("plain: %w", stderrors.New("nope"))
		if _, ok := Get(err); ok {
			t.Error("Get(plain) = true, want false")
		}
	})
}
