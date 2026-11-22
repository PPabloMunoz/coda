package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
	"github.com/ppablomunoz/coda/internal/store"
	"github.com/ppablomunoz/coda/internal/tags"
	"github.com/ppablomunoz/coda/internal/tasks"
)

// getLocalFiles lists scanned files with linked/unlinked, query, format, and track filters.
func (h *Handler) getLocalFiles(c *gin.Context) {
	ctx := c.Request.Context()

	linked, err := parseOptionalBool(c.Query("linked"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	if linked == nil && c.Query("unlink") != "" {
		value := false
		linked = &value
	}

	limit, offset, err := parseLimitAndOffset(c)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	files, total, err := h.st.ListFiles(
		ctx, store.FileListOptions{
			SearchOptions: store.SearchOptions{Query: c.Query("q")},
			SortOptions:   store.SortOptions{Sort: c.Query("sort"), Order: c.Query("order")},
			PageOptions:   store.PageOptions{Limit: limit, Offset: offset},
			Linked:        linked,
			IncludeTrack:  hasInclude(c, "track"),
			Format:        c.Query("format"),
		})
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	response := Response[[]store.LocalFile]{
		Total: total,
		Items: files,
	}

	c.JSON(http.StatusOK, response)
}

// getLocalFile returns one file by id, optionally with its linked track context.
func (h *Handler) getLocalFile(c *gin.Context) {
	ctx := c.Request.Context()

	localFileID, err := requireLocalFileID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	file, err := h.st.GetFile(
		ctx,
		localFileID,
		hasInclude(c, "track"),
	)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, file)
}

// stats returns total/linked/unlinked and per-format file counts.
func (h *Handler) stats(c *gin.Context) {
	ctx := c.Request.Context()
	stats, err := h.st.GetLocalFilesStats(ctx)
	if err != nil {
		h.writeAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}

type PostLinkBody struct {
	TrackID string `json:"track_id"`
}

// libraryTargetPath builds the on-disk destination for a linked track
// under root. Filesystem work stays in the caller; this is pure.
// Single owner is tasks.LibraryTargetPath (reused by the scan auto-link).
func libraryTargetPath(root string, info *tags.LinkInfo, ext string) (string, error) {
	return tasks.LibraryTargetPath(root, info, ext)
}

// ensureMoveDestination creates the destination parent dir and reports
// whether renaming is safe. Single owner is tasks.EnsureMoveDestination
// (reused by the scan auto-link).
func ensureMoveDestination(op string, fileID int64, trackID, src, dst string) error {
	return tasks.EnsureMoveDestination(op, fileID, trackID, src, dst)
}

// link writes release tags and cover to a file, moves it to its library path,
// and records the track link (204). Occupied destinations fail with 409 untouched.
func (h *Handler) link(c *gin.Context) {
	ctx := c.Request.Context()

	localFileID, err := requireLocalFileID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	var body PostLinkBody

	if err := c.ShouldBindBodyWith(&body, binding.JSON); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("body invalid: %w", err)),
		)
		return
	}

	trackID, err := requireMBID(body.TrackID)
	if err != nil {
		return
	}

	start := time.Now()

	// Write tags to file
	// Get file
	localFile, err := h.st.GetFile(ctx, localFileID, false)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file %d load -> %w", localFileID, err)),
		)
		return
	}

	// Get tacks release data
	fullRelease, track, err := h.st.GetTrackFullRelease(
		ctx,
		trackID)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q load release -> %w", localFileID, trackID, err)),
		)
		return
	}

	// Write tags to the source file first (in place). The rename below
	// moves the already-tagged file, then the DB is updated to the
	// final on-disk state.
	linkInfo := tasks.LinkInfoFromStore(fullRelease, track)

	root, err := h.st.GetRoot(ctx, localFile.RootID)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d load root -> %w", localFileID, err)),
		)
		return
	}

	dst, err := libraryTargetPath(root.Path, linkInfo, localFile.FormatHint)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q build path -> %w", localFileID, trackID, err)),
		)
		return
	}

	// Fail before touching tags when the destination is occupied, so a
	// 409 leaves the source file untouched.
	if err := ensureMoveDestination("link", localFileID, trackID, localFile.Path, dst); err != nil {
		h.writeAppError(c, err)
		return
	}

	t := tags.ToMap(tags.SnapshotFromLink(linkInfo))
	if err := tags.WriteTags(localFile.Path, t); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q write tags -> %w", localFileID, trackID, err)),
		)
		return
	}
	if err := h.embedCover(ctx, localFile.Path, fullRelease.ID); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q write cover -> %w", localFileID, trackID, err)),
		)
		return
	}

	src := localFile.Path
	moved := dst != src
	if moved {
		if err := os.Rename(src, dst); err != nil {
			h.writeAppError(c, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("link file=%d track=%q move -> %w", localFileID, trackID, err)),
			)
			return
		}
	}

	fi, err := os.Stat(dst)
	if err != nil {
		// Tags were written and the file may have moved; DB still
		// points at src. Next scan heals this to unlinked, never
		// to a wrong link.
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q stat -> %w", localFileID, trackID, err)),
		)
		return
	}

	if err := h.st.Link(ctx, localFileID,
		trackID, dst, fi.Size(), fi.ModTime().UnixNano(),
	); err != nil {
		if moved {
			if rbErr := os.Rename(dst, src); rbErr != nil {
				h.log.Errorf("link file=%d track=%q rollback failed: %v", localFileID, trackID, rbErr)
			}
		}
		if apperrors.Is(err, apperrors.ErrAlreadyExists) {
			h.writeAppError(c, err)
			return
		}
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("link file=%d track=%q save -> %w", localFileID, trackID, err)),
		)
		return
	}

	h.log.Infof("link file=%d track=%q done in %s", localFileID, trackID, time.Since(start))
	c.JSON(http.StatusNoContent, nil)
}

// unlink clears a file's tags and cover and removes its DB track link (204).
func (h *Handler) unlink(c *gin.Context) {
	ctx := c.Request.Context()

	localFileID, err := requireLocalFileID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	start := time.Now()

	localFile, err := h.st.GetFile(ctx, localFileID, false)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("unlink file=%d load -> %w", localFileID, err)),
		)
		return
	}

	if err := tags.ClearTags(localFile.Path); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("unlink file=%d clear tags -> %w", localFileID, err)),
		)
		return
	}

	if err := tags.ClearCover(localFile.Path); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("unlink file=%d clear cover -> %w", localFileID, err)),
		)
		return
	}

	err = h.st.Unlink(ctx, localFileID)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("unlink file=%d save -> %w", localFileID, err)),
		)
		return
	}

	h.log.Infof("unlink file=%d done in %s", localFileID, time.Since(start))
	c.JSON(http.StatusNoContent, nil)
}

// refreshLocalFile rewrites tags/cover for an already-linked file and moves it
// to its current library path (204).
func (h *Handler) refreshLocalFile(c *gin.Context) {
	ctx := c.Request.Context()

	localFileID, err := requireLocalFileID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	// Write tags to file
	// Get file
	localFile, err := h.st.GetFile(ctx, localFileID, false)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d load -> %w", localFileID, err)),
		)
		return
	}

	if localFile.TrackID == nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInvalidInput,
			fmt.Errorf("file is not linked")),
		)
		return
	}

	start := time.Now()
	trackID := *localFile.TrackID

	// Get tacks release data
	fullRelease, track, err := h.st.GetTrackFullRelease(
		ctx,
		trackID)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d track=%q load release -> %w", localFileID, trackID, err)),
		)
		return
	}

	// Write tags to the source file first (in place). The rename below
	// moves the already-tagged file, then the DB is updated to the
	// final on-disk state.
	linkInfo := tasks.LinkInfoFromStore(fullRelease, track)

	root, err := h.st.GetRoot(ctx, localFile.RootID)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d load root -> %w", localFileID, err)),
		)
		return
	}

	dst, err := libraryTargetPath(root.Path, linkInfo, localFile.FormatHint)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d track=%q build path -> %w", localFileID, trackID, err)),
		)
		return
	}

	// Fail before touching tags when the destination is occupied, so a
	// 409 leaves the source file untouched.
	if err := ensureMoveDestination("refresh", localFileID, trackID, localFile.Path, dst); err != nil {
		h.writeAppError(c, err)
		return
	}

	t := tags.ToMap(tags.SnapshotFromLink(linkInfo))
	if err := tags.WriteTags(localFile.Path, t); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d track=%q write tags -> %w", localFileID, trackID, err)),
		)
		return
	}
	if err := h.embedCover(ctx, localFile.Path, fullRelease.ID); err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d track=%q write cover -> %w", localFileID, trackID, err)),
		)
		return
	}

	src := localFile.Path
	moved := dst != src
	if moved {
		if err := os.Rename(src, dst); err != nil {
			h.writeAppError(c, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("refresh file=%d track=%q move -> %w", localFileID, trackID, err)),
			)
			return
		}
	}

	fi, err := os.Stat(dst)
	if err != nil {
		// Tags were written and the file may have moved; DB still
		// points at src. Next scan heals this to unlinked, never
		// to a wrong link.
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d track=%q stat -> %w", localFileID, trackID, err)),
		)
		return
	}

	if err := h.st.Link(ctx, localFileID,
		trackID, dst, fi.Size(), fi.ModTime().UnixNano(),
	); err != nil {
		if moved {
			if rbErr := os.Rename(dst, src); rbErr != nil {
				h.log.Errorf("refresh file=%d track=%q rollback failed: %v", localFileID, trackID, rbErr)
			}
		}
		if apperrors.Is(err, apperrors.ErrAlreadyExists) {
			h.writeAppError(c, err)
			return
		}
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("refresh file=%d track=%q save -> %w", localFileID, trackID, err)),
		)
		return
	}

	h.log.Infof("refresh file=%d track=%q done in %s", localFileID, trackID, time.Since(start))
	c.JSON(http.StatusNoContent, nil)
}

// requireLocalFileID validates a numeric file id path param, mapping failures to ErrInvalidID.
func requireLocalFileID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, apperrors.ErrInvalidID
	}
	return id, nil
}

// getFileSuggestions returns ranked link suggestions for an unlinked file
// (200). Tag reads are best-effort: a missing/unreadable file yields empty
// suggestions, never a 500 — the next scan heals or prunes the row.
// Already-linked files are 409; nothing here writes tags or moves files.
func (h *Handler) getFileSuggestions(c *gin.Context) {
	ctx := c.Request.Context()

	localFileID, err := requireLocalFileID(c.Param("id"))
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	limit := 5
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 10 {
			h.writeAppError(c, apperrors.Wrap(
				apperrors.ErrInvalidInput,
				fmt.Errorf("invalid 'limit' parameter: must be an integer between 1 and 10")),
			)
			return
		}
	}

	// Optional artist scope for second-chance suggestions after the wizard's
	// artist step. Malformed is 400; unknown is 404 from the store lookup.
	artistID := strings.TrimSpace(c.Query("artist"))
	if artistID != "" {
		artistID, err = requireMBID(artistID)
		if err != nil {
			h.writeAppError(c, err)
			return
		}
	}

	file, err := h.st.GetFile(ctx, localFileID, false)
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	if file.TrackID != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrAlreadyExists,
			fmt.Errorf("file %d is already linked", localFileID)),
		)
		return
	}

	snap, err := tags.Read(file.Path)
	if err != nil {
		h.log.Errorf("suggest file=%d read tags -> %v", localFileID, err)
		c.JSON(http.StatusOK, store.SuggestionResult{
			Suggestions: []store.SuggestedTrack{},
		})
		return
	}

	root, err := h.st.GetRoot(ctx, file.RootID)
	if err != nil {
		h.writeAppError(c, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("suggest file=%d load root -> %w", localFileID, err)),
		)
		return
	}

	res, err := h.st.SuggestTracks(ctx, store.SuggestOptions{
		RootPath: root.Path,
		FilePath: file.Path,
		Snapshot: snap,
		Limit:    limit,
		ArtistID: artistID,
	})
	if err != nil {
		h.writeAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, res)
}
