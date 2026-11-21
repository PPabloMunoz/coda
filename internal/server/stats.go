package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) libraryStats(c *gin.Context) {
	stats, err := h.st.GetLibraryStats(c.Request.Context())
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}
