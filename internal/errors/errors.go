package errors

import stderrors "errors"

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
