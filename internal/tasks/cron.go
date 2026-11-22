package tasks

import (
	"context"
	"time"

	"github.com/ppablomunoz/coda/internal/utils"
)

var (
	scanRootsInterval             = 6 * time.Hour
	pruneArtistsInterval          = 24 * time.Hour
	deleteInactiveArtistsInterval = 24 * 7 * time.Hour // Once every week
)

type Scheduler struct {
	queue *Queue
	log   *utils.Logger
}

// NewScheduler returns a Scheduler enqueueing periodic scan/prune/delete work.
func NewScheduler(q *Queue, log *utils.Logger) *Scheduler {
	return &Scheduler{queue: q, log: log}
}

// Start ticks every 6h (scan), 24h (prune), and 7d (delete inactive) until ctx ends.
func (s *Scheduler) Start(ctx context.Context) {
	if s.log != nil {
		s.log.Debugf(
			"scheduler started (scan interval=%s prune interval=%s delete-inactive-artists interval=%s)",
			scanRootsInterval, pruneArtistsInterval, deleteInactiveArtistsInterval,
		)
	}
	scanTicker := time.NewTicker(scanRootsInterval)
	defer scanTicker.Stop()
	pruneTicker := time.NewTicker(pruneArtistsInterval)
	defer pruneTicker.Stop()
	deleteInactiveArtistsTicker := time.NewTicker(deleteInactiveArtistsInterval)
	defer deleteInactiveArtistsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-scanTicker.C:
			if s.log != nil {
				s.log.Debug("scheduler tick: enqueuing scan")
			}
			s.queue.EnqueueScan()
		case <-pruneTicker.C:
			if s.log != nil {
				s.log.Debug("scheduler tick: enqueuing prune")
			}
			s.queue.EnqueuePrune()
		case <-deleteInactiveArtistsTicker.C:
			if s.log != nil {
				s.log.Debug("scheduler tick: enqueuing deleting inactive artists")
			}
			s.queue.EnqueueDeleteInactiveArtists()
		}
	}
}
