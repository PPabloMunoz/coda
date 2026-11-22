package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ppablomunoz/coda/internal/artwork"
	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/musicbrainz"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tasks"
	"github.com/ppablomunoz/coda/internal/utils"
)

type Response[T any] struct {
	Total int `json:"total"`
	Items T   `json:"items"`
}

type Handler struct {
	log      *utils.Logger
	st       *store.Store
	mb       *musicbrainz.Client
	queue    *tasks.Queue
	art      *artwork.Fetcher // nil disables cover lazy-fetch (cache still serves)
	coverDir string
}

func NewHandler(l *utils.Logger, st *store.Store, mb *musicbrainz.Client, q *tasks.Queue,
	art *artwork.Fetcher, coverDir string,
) *gin.Engine {
	handler := Handler{
		log:      l,
		st:       st,
		mb:       mb,
		queue:    q,
		art:      art,
		coverDir: coverDir,
	}

	router := gin.New()
	router.Use(gin.Recovery())

	v1 := router.Group("/api/v1")
	v1.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: []string{"/api/v1/health"},
	}))

	v1.GET("/health", handler.health)
	v1.GET("/stats", handler.libraryStats)
	v1.POST("/scan", handler.postScan)

	// Local Files
	v1.GET("/files", handler.getLocalFiles)
	v1.GET("/files/stats", handler.stats)
	v1.GET("/files/:id", handler.getLocalFile)
	v1.POST("/files/:id/link", handler.link)
	v1.DELETE("/files/:id/link", handler.unlink)
	v1.POST("/files/:id/refresh", handler.refreshLocalFile)

	// Artists
	v1.GET("/artists", handler.getArtists)
	v1.GET("/artists/:id", handler.getArtist)
	v1.DELETE("/artists/:id", handler.deleteArtist)
	v1.GET("/artists/:id/import", handler.getArtistImportStatus)
	v1.POST("/artists", handler.postArtist)
	v1.POST("/artists/:id/refresh", handler.refreshArtist)

	// MusicBrainz
	v1.GET("/musicbrainz/artists", handler.searchMusicBrainzArtists)

	// Release Groups
	v1.GET("/release-groups", handler.getReleaseGroups)
	v1.GET("/release-groups/:id", handler.getReleaseGroup)
	v1.GET("/release-groups/:id/cover", handler.getReleaseGroupCover)
	v1.DELETE("/release-groups/:id", handler.deleteReleaseGroup)
	v1.PUT("/release-groups/:id/update", handler.replaceRelease)

	// Releases
	v1.GET("/releases", handler.getReleases)
	v1.GET("/releases/:id", handler.getRelease)
	v1.GET("/releases/:id/cover", handler.getReleaseCover)

	// Embedded Astro UI (web/dist). Catch-all, registered last.
	mountWeb(router)

	return router
}

func (h *Handler) health(c *gin.Context) {
	c.String(http.StatusOK, "ok")
}

func parseLimitAndOffset(c *gin.Context) (limit int, offset int, err error) {
	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, err = strconv.Atoi(limitStr)
	if err != nil {
		return 0, 0, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("invalid 'limit' parameter: must be an integer"),
		)
	}
	if limit < 1 || limit > 100 {
		return 0, 0, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("'limit' must be between 1 and 100"),
		)
	}

	offset, err = strconv.Atoi(offsetStr)
	if err != nil {
		return 0, 0, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("invalid 'offset' parameter: must be an integer"),
		)
	}
	if offset < 0 {
		return 0, 0, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("'offset' must be non-negative"),
		)
	}

	return limit, offset, nil
}

// hasInclude reports whether an ?include=... comma-separated flag contains name.
func hasInclude(c *gin.Context, name string) bool {
	for value := range strings.SplitSeq(c.Query("include"), ",") {
		if strings.TrimSpace(value) == name {
			return true
		}
	}
	return false
}

func parseOptionalBool(raw string) (*bool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, apperrors.ErrInvalidInput
	}
	return &value, nil
}

func requireMBID(raw string) (string, error) {
	if err := utils.ValidateMBID(raw); err != nil {
		return "", apperrors.ErrInvalidID
	}
	return raw, nil
}

func requireOptionalMBID(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return requireMBID(raw)
}
