package store

import (
	"context"
	"fmt"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// attachArtistTrackStats counts tracks in releases of release groups where the
// artist holds the primary (position 0) credit. Linked tracks are counted
// once even when multiple local files point at the same track.
func (st *Store) attachArtistTrackStats(ctx context.Context, artists []Artist) error {
	if len(artists) == 0 {
		return nil
	}

	ph := placeholders(len(artists))
	args := make([]any, len(artists))
	for i := range artists {
		args[i] = artists[i].ID
	}

	rows, err := st.db.QueryContext(ctx, `
		SELECT aci.artist_id,
			COUNT(DISTINCT t.id),
			COUNT(DISTINCT CASE WHEN lf.track_id IS NOT NULL THEN t.id END)
		FROM artist_credit_items aci
		JOIN release_groups rg ON rg.artist_credit_id = aci.artist_credit_id
		JOIN releases r ON r.release_group_id = rg.id
		JOIN tracks t ON t.release_id = r.id
		LEFT JOIN local_files lf ON lf.track_id = t.id
		WHERE aci.artist_id IN (`+ph+`)
		  AND aci.position = 0
		GROUP BY aci.artist_id`,
		args...,
	)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query artist track stats: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	byArtist := make(map[string]ArtistTrackStats, len(artists))
	for rows.Next() {
		var artistID string
		var stats ArtistTrackStats
		if err := rows.Scan(
			&artistID,
			&stats.TrackCount,
			&stats.LinkedTrackCount,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan artist track stats: %w", err),
			)
		}
		byArtist[artistID] = stats
	}
	if err := rows.Err(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading artist track stats: %w", err),
		)
	}

	for i := range artists {
		stats := byArtist[artists[i].ID]
		artists[i].Stats = &stats
	}

	return nil
}

func (st *Store) GetArtistTrackStats(
	ctx context.Context,
	id string,
) (*ArtistTrackStats, error) {
	artists := []Artist{{ID: id}}
	if err := st.attachArtistTrackStats(ctx, artists); err != nil {
		return nil, err
	}
	if artists[0].Stats == nil {
		return &ArtistTrackStats{}, nil
	}
	return artists[0].Stats, nil
}
