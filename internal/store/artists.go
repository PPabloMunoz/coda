package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// ListArtists returns a filtered page of active artists plus the total count.
// Unless opts.All, only in_library artists are returned.
func (st *Store) ListArtists(ctx context.Context, opts ArtistListOptions) ([]Artist, int, error) {
	var total int

	conditions := []string{"active = 1"}
	if !opts.All {
		conditions = append(conditions, "in_library = 1")
	}
	args := []any{}
	if query := strings.TrimSpace(opts.Query); query != "" {
		pattern := likePattern(query)
		conditions = append(conditions, `(unaccent(name) LIKE unaccent(?) ESCAPE '\' OR unaccent(sort_name) LIKE unaccent(?) ESCAPE '\' OR unaccent(disambiguation) LIKE unaccent(?) ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	where := "WHERE " + strings.Join(conditions, " AND ")

	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM artists "+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count artists: %w", err),
		)
	}

	order := sortExpression(opts.Sort, map[string]string{
		"name":      "unaccent(name)",
		"sort_name": "unaccent(sort_name)",
	}, "name") + " " + orderDirection(opts.Order) + " NULLS LAST"

	queryArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := st.db.QueryContext(ctx, `
		SELECT id, name, sort_name, disambiguation, active, in_library
		FROM artists `+where+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query artists: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	artists := []Artist{}

	for rows.Next() {
		a := Artist{}

		if err := rows.Scan(
			&a.ID,
			&a.Name,
			&a.SortName,
			&a.Disambiguation,
			&a.Active,
			&a.InLibrary,
		); err != nil {
			return nil, 0, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan artist: %w", err),
			)
		}

		artists = append(artists, a)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading rows: %w", err),
		)
	}

	if opts.IncludeTrackStats {
		if err := st.attachArtistTrackStats(ctx, artists); err != nil {
			return nil, 0, err
		}
	}

	return artists, total, nil
}

// GetArtistByID returns an active artist by MBID, or ErrNotFound.
func (st *Store) GetArtistByID(ctx context.Context, id string) (*Artist, error) {
	stmt, err := st.db.PrepareContext(ctx, `
		SELECT id, name, sort_name, disambiguation, active, in_library
		FROM artists
		WHERE id = ? AND active = 1`)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("prepare query: %w", err),
		)
	}
	defer func() { _ = stmt.Close() }()

	var art Artist
	if err := stmt.QueryRowContext(ctx, id).Scan(
		&art.ID,
		&art.Name,
		&art.SortName,
		&art.Disambiguation,
		&art.Active,
		&art.InLibrary,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperrors.ErrNotFound
		}
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query artist: %w", err),
		)
	}

	return &art, nil
}

// CreateArtist inserts an artist or reactivates it as active/in_library on conflict.
// A permanent-deletion tombstone blocks the insert with ErrForbidden.
func (st *Store) CreateArtist(ctx context.Context, art Artist) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	var ignored bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM ignored WHERE id = ? AND type = 'artist')`, art.ID,
	).Scan(&ignored); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("check ignored: %w", err),
		)
	}
	if ignored {
		return apperrors.ErrForbidden
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO artists (id, name, sort_name, disambiguation, active, in_library)
		VALUES (?, ?, ?, ?, 1, 1)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			sort_name=excluded.sort_name,
			disambiguation=excluded.disambiguation,
			active=1,
			in_library=1`,
		art.ID, art.Name, art.SortName, art.Disambiguation,
	); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("insert artist: %w", err),
		)
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit create artist: %w", err),
		)
	}

	return nil
}

// DeleteArtist removes groups where id appears and no other active library
// artist remains credited, soft-deletes the artist, and prunes orphan
// collaborators. Groups shared with a surviving library artist are kept so
// they still show in the other artist's "appears in".
// When permanent is true the artist MBID is also tombstoned in ignored so
// later imports and re-adds cannot resurrect it until the row is removed.
func (st *Store) DeleteArtist(ctx context.Context, id string, permanent bool) (int64, error) {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM artists WHERE id = ? AND active = 1)`, id,
	).Scan(&exists); err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("check artist: %w", err),
		)
	}
	if !exists {
		return 0, apperrors.ErrNotFound
	}

	// Delete groups where the artist appears but no other active library
	// artist remains credited. Groups shared with a surviving library
	// artist are kept with their credits intact so they keep showing in
	// the other artist's "appears in". Releases and tracks cascade-delete;
	// linked files auto-unlink via ON DELETE SET NULL. The deleted id is
	// excluded explicitly so this works regardless of statement order
	// within the tx.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM release_groups WHERE id IN (
			SELECT rg.id
			FROM release_groups rg
			JOIN artist_credit_items aci_del
			  ON aci_del.artist_credit_id = rg.artist_credit_id
			WHERE aci_del.artist_id = ?
			AND NOT EXISTS (
				SELECT 1 FROM artist_credit_items aci_lib
				JOIN artists a_lib ON a_lib.id = aci_lib.artist_id
				WHERE aci_lib.artist_credit_id = rg.artist_credit_id
				  AND aci_lib.artist_id <> ?
				  AND a_lib.active = 1 AND a_lib.in_library = 1
			)
		)`, id, id,
	); err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete orphan release groups: %w", err),
		)
	}

	// Soft-delete: keep the artist row so surviving collaboration credits
	// still resolve (artist_credit_items.artist_id is ON DELETE RESTRICT).
	// in_library is cleared too so the row stays out of both the default
	// library view and ?all=true.
	res, err := tx.ExecContext(ctx,
		`UPDATE artists SET active = 0, in_library = 0 WHERE id = ? AND active = 1`, id)
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist: %w", err),
		)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete artist: %w", err),
		)
	}
	if n == 0 {
		return 0, apperrors.ErrNotFound
	}

	if permanent {
		var name string
		if err := tx.QueryRowContext(ctx,
			`SELECT name FROM artists WHERE id = ?`, id,
		).Scan(&name); err != nil {
			return 0, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("read artist name: %w", err),
			)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ignored (id, name, type)
			VALUES (?, ?, 'artist')
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, type='artist'`,
			id, name,
		); err != nil {
			return 0, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("ignore artist: %w", err),
			)
		}
	}

	// Collect credits orphaned by the deletes above (also reclaims rows
	// leaked by re-imports, which insert a fresh credit row per upsert).
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM artist_credits
		WHERE NOT EXISTS (
			SELECT 1 FROM release_groups rg WHERE rg.artist_credit_id = artist_credits.id
		) AND NOT EXISTS (
			SELECT 1 FROM releases r WHERE r.artist_credit_id = artist_credits.id
		) AND NOT EXISTS (
			SELECT 1 FROM tracks t WHERE t.artist_credit_id = artist_credits.id
		)`,
	); err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete orphan credits: %w", err),
		)
	}

	// Drop collaborators left trackless by the deletes above (they only
	// ever appeared on this artist's releases). Same tx so delete + prune
	// commit atomically.
	pruned, err := pruneOrphanCollaboratorsTx(ctx, tx)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit delete artist: %w", err),
		)
	}

	return pruned, nil
}

// PruneOrphanCollaborators soft-deletes collaboration-only artists that no
// longer back any track in any credit position (e.g. collaborators left
// behind when the library artist whose releases carried them was removed).
// Collaborators still heard on another artist's tracks survive. Rows are
// kept so surviving group/release credits still resolve
// (artist_credit_items.artist_id is ON DELETE RESTRICT). Returns rows cleared.
func (st *Store) PruneOrphanCollaborators(ctx context.Context) (int64, error) {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	n, err := pruneOrphanCollaboratorsTx(ctx, tx)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit prune collaborators: %w", err),
		)
	}

	return n, nil
}

// pruneOrphanCollaboratorsTx soft-deletes collaboration-only artists with no track
// credit left in tx, returning rows cleared.
func pruneOrphanCollaboratorsTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE artists SET active = 0, in_library = 0
		WHERE active = 1 AND in_library = 0
		AND NOT EXISTS (
			SELECT 1 FROM artist_credit_items aci
			JOIN tracks t ON t.artist_credit_id = aci.artist_credit_id
			WHERE aci.artist_id = artists.id
		)`,
	)
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("prune collaborators: %w", err),
		)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("prune collaborators: %w", err),
		)
	}
	return n, nil
}

// DeleteInactiveArtists hard-deletes soft-deleted artists with no credit references left.
func (st *Store) DeleteInactiveArtists(ctx context.Context) (int64, error) {
	res, err := st.db.ExecContext(ctx, `
		DELETE FROM artists 
		WHERE active = 0 AND in_library = 0
			AND NOT EXISTS (
			SELECT 1 FROM artist_credit_items WHERE artist_id = artists.id
		)`,
	)
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete inactive artists: %w", err),
		)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete inactive artists: %w", err),
		)
	}
	return n, nil
}
