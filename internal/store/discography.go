package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// SaveDiscography upserts each release's group/release/tracks in one tx,
// skipping rows with missing ids/titles and deduping by release-group id.
// Permanently deleted groups (ignored) are skipped so a delete stays gone
// across refreshes.
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

	ignored, err := ignoredGroupIDsTx(ctx, tx)
	if err != nil {
		return err
	}

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

		if _, ok := ignored[r.Group.ID]; ok {
			continue
		}

		filterReleases = append(filterReleases, r)
	}

	for _, r := range filterReleases {
		if err := st.SaveFullRelease(ctx, tx, r); err != nil {
			return err
		}
	}

	if err := suppressIgnoredCollaboratorsTx(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit discography: %w", err),
		)
	}

	return nil
}

// SaveArtistDiscography saves an artist's discography only when the artist
// is still an active library member and not permanently deleted. It returns
// ErrNotFound when the artist was removed mid-import (or is ignored) so the
// runner can forget the import instead of marking it failed. Ignored groups
// are skipped like SaveDiscography.
func (st *Store) SaveArtistDiscography(ctx context.Context, artistID string,
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

	var active, inLibrary int
	if err := tx.QueryRowContext(ctx,
		`SELECT active, in_library FROM artists WHERE id = ?`, artistID,
	).Scan(&active, &inLibrary); err != nil {
		if err == sql.ErrNoRows {
			return apperrors.ErrNotFound
		}
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("check artist: %w", err),
		)
	}
	if active != 1 || inLibrary != 1 {
		return apperrors.ErrNotFound
	}

	var artistIgnored bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM ignored WHERE id = ? AND type = 'artist')`, artistID,
	).Scan(&artistIgnored); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("check ignored artist: %w", err),
		)
	}
	if artistIgnored {
		return apperrors.ErrNotFound
	}

	ignored, err := ignoredGroupIDsTx(ctx, tx)
	if err != nil {
		return err
	}

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

		if _, ok := ignored[r.Group.ID]; ok {
			continue
		}

		filterReleases = append(filterReleases, r)
	}

	for _, r := range filterReleases {
		if err := st.SaveFullRelease(ctx, tx, r); err != nil {
			return err
		}
	}

	if err := suppressIgnoredCollaboratorsTx(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit discography: %w", err),
		)
	}

	return nil
}

// ignoredGroupIDsTx loads permanently deleted release-group MBIDs in tx.
func ignoredGroupIDsTx(ctx context.Context, tx *sql.Tx) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM ignored WHERE type = 'release_group'`)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query ignored groups: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	ignored := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan ignored group: %w", err),
			)
		}
		ignored[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading ignored groups: %w", err),
		)
	}
	return ignored, nil
}

// suppressIgnoredCollaboratorsTx keeps permanently deleted artists inactive
// when live credits reference them again. The artist row must keep existing
// so credit joins still resolve; it just never reactivates to active = 1.
func suppressIgnoredCollaboratorsTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE artists SET active = 0
		WHERE EXISTS (
			SELECT 1 FROM ignored
			WHERE ignored.id = artists.id AND ignored.type = 'artist'
		) AND active = 1`,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("suppress ignored artists: %w", err),
		)
	}
	return nil
}
