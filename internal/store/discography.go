package store

import (
	"context"
	"fmt"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// SaveDiscography upserts each release's group/release/tracks in one tx,
// skipping rows with missing ids/titles and deduping by release-group id.
func (st *Store) SaveDiscography(ctx context.Context,
	releases []FullReleaseParams,
) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("save discography start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	seen := map[string]struct{}{}
	filterReleases := []FullReleaseParams{}
	for _, r := range releases {
		if strings.TrimSpace(r.ID) == "" {
			continue
		}
		if strings.TrimSpace(r.Title) == "" {
			continue
		}
		if strings.TrimSpace(r.Group.ID) == "" {
			continue
		}
		if strings.TrimSpace(r.Group.Title) == "" {
			continue
		}

		if _, ok := seen[r.Group.ID]; ok {
			continue
		}
		seen[r.Group.ID] = struct{}{}

		filterReleases = append(filterReleases, r)
	}

	for _, r := range filterReleases {
		if err := st.SaveFullRelease(ctx, tx, r); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit discography: %w", err),
		)
	}

	return nil
}
