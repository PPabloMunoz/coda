package server

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// postScan enqueues a library rescan (same work as startup + 6h cron).
// Dedup is pending-only: if a scan is already queued it is not
// enqueued twice (a force request upgrades the pending scan), but a scan
// already running does not block a new one from queueing behind it.
// Always 202: the scan runs async.
//
// ?force_prune=true allows pruning roots whose complete walk yields zero
// files; without it those roots keep their rows. Incomplete walks never
// prune, even with force.
func (h *Handler) postScan(c *gin.Context) {
	force := false
	if raw := c.Query("force_prune"); raw != "" {
		v, err := parseOptionalBool(raw)
		if err != nil || v == nil {
			h.writeAppError(c, apperrors.Wrap(
				apperrors.ErrInvalidInput,
				fmt.Errorf("invalid 'force_prune' parameter: must be a boolean"),
			))
			return
		}
		force = *v
	}
	h.queue.EnqueueScan(force)
	c.JSON(http.StatusAccepted, nil)
}

// getScanStatus returns the queued/running/done/failed scan lifecycle plus
// the last completed run's counters. Terminal states persist, so the last
// run survives a restart; queued/running are memory-only.
func (h *Handler) getScanStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.queue.ScanStatus())
}
