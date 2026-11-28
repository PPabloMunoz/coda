package errors

import (
	stderrors "errors"
	"strings"
)

// AppError is a stable, machine-readable application error with a safe
// public message and an optional internal cause. The cause is inspectable
// via Unwrap but never rendered by Error.
type AppError struct {
	Code    string
	Message string
	Cause   error
}

// Predefined application errors for common situations.
var (
	ErrNotFound          = &AppError{Code: "NOT_FOUND", Message: "Resource not found"}
	ErrUnauthorized      = &AppError{Code: "UNAUTHORIZED", Message: "Authentication required"}
	ErrForbidden         = &AppError{Code: "FORBIDDEN", Message: "Access denied"}
	ErrAlreadyExists     = &AppError{Code: "ALREADY_EXISTS", Message: "Resource already exists"}
	ErrInvalidID         = &AppError{Code: "INVALID_ID", Message: "The id must be a valid uuid"}
	ErrInvalidInput      = &AppError{Code: "INVALID_INPUT", Message: "Invalid input"}
	ErrPayloadTooLarge   = &AppError{Code: "PAYLOAD_TOO_LARGE", Message: "Request body too large"}
	ErrInvalidHost       = &AppError{Code: "INVALID_HOST", Message: "Host not allowed"}
	ErrMusicBrainzFailed = &AppError{Code: "MUSICBRAINZ_FAILED", Message: "MusicBrainz failed"}
	ErrInternal          = &AppError{Code: "INTERNAL_ERROR", Message: "An unexpected error occurred"}
)

// Error returns the safe, human-readable message. It never includes Cause.
func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap returns the underlying cause, if any.
func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is reports whether target is an *AppError with the same Code. Code-based
// matching lets Wrap create a new instance while preserving identity for
// errors.Is.
func (e *AppError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	t, ok := target.(*AppError)
	if !ok || t == nil {
		return false
	}
	return e.Code != "" && e.Code == t.Code
}

// Wrap attaches cause to appErr, preserving its Code and Message in a new
// instance. The sentinels are never mutated. Wrap(nil, cause) returns nil.
func Wrap(appErr *AppError, cause error) *AppError {
	if appErr == nil {
		return nil
	}
	return &AppError{Code: appErr.Code, Message: appErr.Message, Cause: cause}
}

// Is is equivalent to the standard errors.Is.
func Is(err, target error) bool {
	return stderrors.Is(err, target)
}

// As is equivalent to the standard errors.As.
func As(err error, target any) bool {
	return stderrors.As(err, target)
}

// Get retrieves the first AppError in err's chain. It returns false when
// err is nil or contains no AppError.
func Get(err error) (*AppError, bool) {
	if err == nil {
		return nil, false
	}
	appErr, ok := stderrors.AsType[*AppError](err)
	return appErr, ok
}

// LogDetail renders err for server logs: the cause chain behind any AppError,
// keeping plain fmt wrapping context but dropping the client-safe AppError
// message. AppError.Error() deliberately hides the cause, so logging err
// directly says "An unexpected error occurred" and nothing else. Never send
// this string to a client.
func LogDetail(err error) string {
	return logDetail(err, 0)
}

// logDetail walks AppError causes at most maxLogDetailDepth levels deep so a
// malformed cycle cannot hang a log line.
func logDetail(err error, depth int) string {
	if err == nil {
		return ""
	}
	const maxLogDetailDepth = 16
	if depth >= maxLogDetailDepth {
		return err.Error()
	}
	appErr, ok := Get(err)
	if !ok || appErr == nil || appErr.Cause == nil {
		return err.Error()
	}

	cause := logDetail(appErr.Cause, depth+1)
	if err.Error() == appErr.Message {
		// Direct AppError (or a wrapper with no extra context): the safe
		// message is generic, so log only the cause.
		return cause
	}
	// A plain fmt wrapper (for example "cover refresh release=..."): keep its
	// context and replace the embedded safe message with the cause.
	msg := err.Error()
	if !strings.HasSuffix(msg, appErr.Message) {
		return msg + ": " + cause
	}
	prefix := strings.TrimSuffix(msg, appErr.Message)
	prefix = strings.TrimSuffix(prefix, ": ")
	if prefix == "" {
		return cause
	}
	return prefix + ": " + cause
}
