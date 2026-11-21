package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
	log   *utils.Logger
	st    *store.Store
	mb    *musicbrainz.Client
	queue *tasks.Queue
}

func NewHandler(l *utils.Logger, st *store.Store, mb *musicbrainz.Client, q *tasks.Queue,
) *gin.Engine {
	handler := Handler{
		log:   l,
		st:    st,
		mb:    mb,
		queue: q,
	}

	router := gin.Default()
	v1 := router.Group("/api/v1")

	v1.GET("/health", handler.health)
	v1.GET("/stats", handler.libraryStats)
	v1.POST("/scan", handler.postScan)

	// Local Files
	// mux.HandleFunc("GET /api/files", handler.getLocalFiles())
	// mux.HandleFunc("GET /api/files/stats", handler.stats())
	// mux.HandleFunc("GET /api/files/{id}", handler.getLocalFile())
	// mux.HandleFunc("POST /api/files/{id}/link", handler.link())
	// mux.HandleFunc("DELETE /api/files/{id}/link", handler.unlink())
	// mux.HandleFunc("POST /api/files/{id}/refresh", handler.refreshLocalFile())
	//
	// Artists
	v1.GET("/artists", handler.getArtists)
	v1.GET("/artists/:id", handler.getArtist)
	v1.DELETE("/artists/:id", handler.deleteArtist)
	v1.GET("/artists/:id/import", handler.getArtistImportStatus)
	v1.POST("/artists", handler.postArtist)
	v1.POST("/artists/:id/refresh", handler.refreshArtist)
	//
	// MusicBrainz
	// mux.HandleFunc("GET /api/musicbrainz/artists", handler.searchMusicBrainzArtists())
	//
	// Release Groups
	// mux.HandleFunc("GET /api/release-groups", handler.getReleaseGroups())
	// mux.HandleFunc("GET /api/release-groups/{id}", handler.getReleaseGroup())
	// mux.HandleFunc("DELETE /api/release-groups/{id}", handler.deleteReleaseGroup())
	// mux.HandleFunc("PUT /api/release-groups/{id}/update", handler.replaceRelease())
	//
	// Releases
	// mux.HandleFunc("GET /api/releases", handler.getReleases())
	// mux.HandleFunc("GET /api/releases/{id}", handler.getRelease())
	//
	// Embedded Astro UI (web/dist). Catch-all, registered last.
	// mountWeb(mux)

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

func requireOptionalMBID(w http.ResponseWriter, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return requireMBID(raw)
}

func (h *Handler) handleError(w http.ResponseWriter, err error) bool {
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.log.Debugf("not found: %v", err)
			writeJSON(w, http.StatusNotFound, nil)
			return true
		}
		if errors.Is(err, musicbrainz.ErrNotFound) {
			h.log.Debugf("musicbrainz not found: %v", err)
			writeJSON(w, http.StatusNotFound, nil)
			return true
		}
		h.log.Error(err.Error())
		writeError(w, http.StatusInternalServerError, "Internal Server Error")
		return true
	}
	return false
}

// handleErrorCtx is handleError with operation context (IDs, no paths).
// Single log line: Debug on 404, Error on 500. Used by handlers that
// mutate files so failures carry the file/track IDs.
func (h *Handler) handleErrorCtx(w http.ResponseWriter, err error, format string, args ...any) bool {
	if err == nil {
		return false
	}
	ctxMsg := fmt.Sprintf(format, args...)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, musicbrainz.ErrNotFound) {
		h.log.Debugf("%s: not found: %v", ctxMsg, err)
		writeJSON(w, http.StatusNotFound, nil)
		return true
	}
	h.log.Errorf("%s: %v", ctxMsg, err)
	writeError(w, http.StatusInternalServerError, "Internal Server Error")
	return true
}

// withLogging records one line per API request. Health checks are excluded.
// 5xx -> Error, 4xx -> Warn, 2xx/3xx -> Debug. Only the path is logged,
// never query values.
func (h *Handler) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		msg := fmt.Sprintf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start))
		switch {
		case rw.status >= 500:
			h.log.Errorf("request %s", msg)
		case rw.status >= 400:
			h.log.Warnf("request %s", msg)
		default:
			h.log.Debugf("request %s", msg)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// ErrorResponse is the JSON shape for API errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

// WriteJSON encodes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError encodes a JSON error message with the given status code.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}
