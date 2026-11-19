package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/tasks"
)

func (h *Handler) getLocalFiles() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queryParams := r.URL.Query()

		linked, err := parseOptionalBool(queryParams.Get("linked"))
		if err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if linked == nil && queryParams.Get("unlink") != "" {
			value := false
			linked = &value
		}

		limit, offset, err := parseLimitAndOffset(r)
		if err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		files, total, err := h.st.ListFiles(
			r.Context(), store.FileListOptions{
				SearchOptions: store.SearchOptions{Query: queryParams.Get("q")},
				SortOptions:   store.SortOptions{Sort: queryParams.Get("sort"), Order: queryParams.Get("order")},
				PageOptions:   store.PageOptions{Limit: limit, Offset: offset},
				Linked:        linked,
				IncludeTrack:  hasInclude(r, "track"),
				Format:        queryParams.Get("format"),
			})
		if h.handleError(w, err) {
			return
		}

		response := Response[[]store.LocalFile]{
			Total: total,
			Items: files,
		}

		writeJSON(w, http.StatusOK, response)
	}
}

func (h *Handler) getLocalFile() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		localFileID, ok := requireFileID(w, r.PathValue("id"))
		if !ok {
			return
		}

		file, err := h.st.GetFile(
			r.Context(),
			localFileID,
			hasInclude(r, "track"),
		)
		if h.handleError(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, file)
	}
}

func (h *Handler) stats() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := h.st.GetLocalFilesStats(r.Context())
		if h.handleError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, stats)
	}
}

type PostLinkBody struct {
	TrackID string `json:"track_id"`
}

func (h *Handler) link() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		localFileID, ok := requireFileID(w, r.PathValue("id"))
		if !ok {
			return
		}

		var body PostLinkBody

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			h.log.Debugf("bad request %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, http.StatusBadRequest, "body invalid")
			return
		}

		trackID, ok := requireMBID(w, body.TrackID)
		if !ok {
			return
		}

		start := time.Now()

		// Write tags to file
		// Get file
		localFile, err := h.st.GetFile(ctx, localFileID, false)
		if h.handleErrorCtx(w, err, "link file=%d load", localFileID) {
			return
		}

		// Get tacks release data
		fullRelease, track, err := h.st.GetTrackFullRelease(
			ctx,
			trackID)
		if h.handleErrorCtx(w, err, "link file=%d track=%q load release", localFileID, trackID) {
			return
		}

		// Write tags
		linkInfo := tasks.LinkInfoFromStore(fullRelease, track)
		t := tags.ToMap(tags.SnapshotFromLink(linkInfo))
		if err := tags.WriteTags(localFile.Path, t); h.handleErrorCtx(w, err, "link file=%d track=%q write tags", localFileID, trackID) {
			return
		}

		// Insert into DB

		if err := h.st.Link(ctx, localFileID, trackID); h.handleErrorCtx(w, err, "link file=%d track=%q save", localFileID, trackID) {
			return
		}

		h.log.Infof("link file=%d track=%q done in %s", localFileID, trackID, time.Since(start))
		writeJSON(w, http.StatusNoContent, nil)
	}
}

func (h *Handler) unlink() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		localFileID, ok := requireFileID(w, r.PathValue("id"))
		if !ok {
			return
		}

		start := time.Now()

		localFile, err := h.st.GetFile(ctx, localFileID, false)
		if h.handleErrorCtx(w, err, "unlink file=%d load", localFileID) {
			return
		}

		if err := tags.ClearTags(localFile.Path); h.handleErrorCtx(w, err, "unlink file=%d clear tags", localFileID) {
			return
		}

		err = h.st.Unlink(r.Context(), localFileID)
		if h.handleErrorCtx(w, err, "unlink file=%d save", localFileID) {
			return
		}

		h.log.Infof("unlink file=%d done in %s", localFileID, time.Since(start))
		writeJSON(w, http.StatusNoContent, nil)
	}
}

func (h *Handler) refreshLocalFile() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		localFileID, ok := requireFileID(w, r.PathValue("id"))
		if !ok {
			return
		}

		// Write tags to file
		// Get file
		localFile, err := h.st.GetFile(ctx, localFileID, false)
		if h.handleErrorCtx(w, err, "refresh file=%d load", localFileID) {
			return
		}

		if localFile.TrackID == nil {
			writeError(w, http.StatusBadRequest, "file is not linked")
			return
		}

		start := time.Now()
		trackID := *localFile.TrackID

		// Get tacks release data
		fullRelease, track, err := h.st.GetTrackFullRelease(
			ctx,
			trackID)
		if h.handleErrorCtx(w, err, "refresh file=%d track=%q load release", localFileID, trackID) {
			return
		}

		// Write tags
		linkInfo := tasks.LinkInfoFromStore(fullRelease, track)
		t := tags.ToMap(tags.SnapshotFromLink(linkInfo))
		if err := tags.WriteTags(localFile.Path, t); h.handleErrorCtx(w, err, "refresh file=%d track=%q write tags", localFileID, trackID) {
			return
		}

		h.log.Infof("refresh file=%d track=%q done in %s", localFileID, trackID, time.Since(start))
		writeJSON(w, http.StatusNoContent, nil)
	}
}

func requireFileID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}
