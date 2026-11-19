package server

import "net/http"

// postScan enqueues a library rescan (same work as startup + 6h cron).
// Dedup is pending-only: if a scan is already queued it is not
// enqueued twice, but a scan already running does not block a new
// one from queueing behind it. Always 202: the scan runs async.
func (h *Handler) postScan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.queue.EnqueueScan()
		writeJSON(w, http.StatusAccepted, nil)
	}
}
