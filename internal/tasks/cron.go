package tasks

import (
	"context"
	"time"

	"github.com/ppablomunoz/coda/internal/utils"
)

var scanRootsInterval = 6 * time.Hour
var pruneArtistsInterval = 24 * time.Hour

type Scheduler struct {
	queue *Queue
	log   *utils.Logger
}

func NewScheduler(q *Queue, log *utils.Logger) *Scheduler {
	return &Scheduler{queue: q, log: log}
}

func (s *Scheduler) Start(ctx context.Context) {
	if s.log != nil {
		s.log.Debugf("scheduler started (scan interval=%s prune interval=%s)", scanRootsInterval, pruneArtistsInterval)
	}
	scanTicker := time.NewTicker(scanRootsInterval)
	defer scanTicker.Stop()
	pruneTicker := time.NewTicker(pruneArtistsInterval)
	defer pruneTicker.Stop()

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
		}
	}
}
