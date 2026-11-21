package store

import (
	"context"
	"fmt"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

type LibraryStats struct {
	Artists       int64           `json:"artists"`
	ReleaseGroups int64           `json:"release_groups"`
	Releases      int64           `json:"releases"`
	Tracks        int64           `json:"tracks"`
	Files         LocalFilesStats `json:"files"`
}

func (st *Store) GetLibraryStats(ctx context.Context) (*LibraryStats, error) {
	stats := &LibraryStats{}

	if err := st.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM artists WHERE active = 1 AND in_library = 1),
			(SELECT COUNT(*) FROM release_groups),
			(SELECT COUNT(*) FROM releases),
			(SELECT COUNT(*) FROM tracks)`,
	).Scan(
		&stats.Artists,
		&stats.ReleaseGroups,
		&stats.Releases,
		&stats.Tracks,
	); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count stats: %w", err),
		)
	}

	files, err := st.GetLocalFilesStats(ctx)
	if err != nil {
		return nil, err
	}
	stats.Files = *files

	return stats, nil
}
