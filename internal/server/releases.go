package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// getReleases resolves the release kept for a release-group:
// GET /api/releases?group=<release-group-mbid>. One release per group is
// enforced by the UNIQUE on releases.release_group_id. The group param is
// required; a group with no saved release yet answers 404 null.
func (h *Handler) getReleases(c *gin.Context) {
	ctx := c.Request.Context()

	releaseGroupID, err := requireMBID(c.Query("group"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	release, err := h.st.GetReleaseOfGroup(
		ctx,
		releaseGroupID,
		hasInclude(c, "files"),
	)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, release)
}

func (h *Handler) getRelease(c *gin.Context) {
	ctx := c.Request.Context()

	releaseID, err := requireMBID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	release, err := h.st.GetRelease(
		ctx,
		releaseID,
		hasInclude(c, "files"),
	)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, release)
}
