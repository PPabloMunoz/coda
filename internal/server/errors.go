package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

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
	default:
		return http.StatusInternalServerError
	}
}

func (h *Handler) writeAppError(c *gin.Context, err error) {
	appErr, ok := apperrors.Get(err)
	if !ok {
		h.log.Errorf("%s %s: unexpected: %v", c.Request.Method, c.FullPath(), err)
		c.AbortWithStatusJSON(http.StatusInternalServerError,
			gin.H{"error": "An unexpected error occurred"})
		return
	}

	if appErr.Cause != nil {
		h.log.Errorf("%s %s -> %s: %v | cause: %v",
			c.Request.Method, c.FullPath(), appErr.Code, err, appErr.Cause)
	} else {
		h.log.Warnf("%s %s -> %s: %v",
			c.Request.Method, c.FullPath(), appErr.Code, appErr.Message)
	}

	c.AbortWithStatusJSON(httpStatusFor(appErr.Code),
		gin.H{"error": appErr.Message, "code": appErr.Code})
}
