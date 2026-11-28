package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// httpStatusFor maps an AppError code to its HTTP status (unknown codes are 500).
func httpStatusFor(code string) int {
	switch code {
	case "NOT_FOUND":
		return http.StatusNotFound
	case "UNAUTHORIZED":
		return http.StatusUnauthorized
	case "FORBIDDEN":
		return http.StatusForbidden
	case "ALREADY_EXISTS":
		return http.StatusConflict
	case "INVALID_INPUT", "INVALID_ID":
		return http.StatusBadRequest
	case "PAYLOAD_TOO_LARGE":
		return http.StatusRequestEntityTooLarge
	case "INVALID_HOST":
		return http.StatusMisdirectedRequest
	case "MUSICBRAINZ_FAILED":
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// writeAppError logs err with method/path and aborts with its safe message and code.
// The actual request path is logged (not the /:id route template) so a failure
// names the release/artist/file it belongs to; causes go through LogDetail.
// A *http.MaxBytesError anywhere in the chain maps to 413 PAYLOAD_TOO_LARGE
// (cover uploads handle their own limit and never leak MaxBytesError here).
func (h *Handler) writeAppError(c *gin.Context, err error) {
	path := c.Request.URL.EscapedPath()
	if path == "" {
		path = c.FullPath()
	}

	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		h.log.Errorf("%s %s -> PAYLOAD_TOO_LARGE: %s | cause: %v",
			c.Request.Method, path, apperrors.LogDetail(err), maxErr)
		c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge,
			gin.H{"error": apperrors.ErrPayloadTooLarge.Message, "code": apperrors.ErrPayloadTooLarge.Code})
		return
	}
	appErr, ok := apperrors.Get(err)
	if !ok || appErr == nil {
		h.log.Errorf("%s %s: unexpected: %s",
			c.Request.Method, path, apperrors.LogDetail(err))
		c.AbortWithStatusJSON(http.StatusInternalServerError,
			gin.H{"error": "An unexpected error occurred"})
		return
	}

	switch {
	case appErr.Cause != nil:
		h.log.Errorf("%s %s -> %s: %s",
			c.Request.Method, path, appErr.Code, apperrors.LogDetail(err))
	case appErr.Code == apperrors.ErrNotFound.Code:
		// A missing entity/cover is a normal answer for the web client; keep
		// it out of the warning stream and visible only with CODA_DEBUG=1.
		h.log.Debugf("%s %s -> %s: %s",
			c.Request.Method, path, appErr.Code, appErr.Message)
	case httpStatusFor(appErr.Code) >= http.StatusInternalServerError:
		h.log.Errorf("%s %s -> %s: %s",
			c.Request.Method, path, appErr.Code, appErr.Message)
	default:
		h.log.Warnf("%s %s -> %s: %s",
			c.Request.Method, path, appErr.Code, appErr.Message)
	}

	c.AbortWithStatusJSON(httpStatusFor(appErr.Code),
		gin.H{"error": appErr.Message, "code": appErr.Code})
}
