package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

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
		return nil, 0, fmt.Errorf("store: count artists: %w", err)
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
		return nil, 0, fmt.Errorf("store: query artists: %w", err)
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
			return nil, 0, fmt.Errorf("store: scan artist: %w", err)
		}

		artists = append(artists, a)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: reading rows: %w", err)
	}

	if opts.IncludeTrackStats {
		if err := st.attachArtistTrackStats(ctx, artists); err != nil {
			return nil, 0, err
		}
	}

	return artists, total, nil
}

func (st *Store) GetArtistByID(ctx context.Context, id string) (*Artist, error) {
	stmt, err := st.db.PrepareContext(ctx, `
		SELECT id, name, sort_name, disambiguation, active, in_library
		FROM artists
		WHERE id = ? AND active = 1`)
	if err != nil {
		return nil, st.prepareQueryError(err)
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
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: query artist: %w", err)
	}

	return &art, nil
}

func (st *Store) CreateArtist(ctx context.Context, art Artist) error {
	stmt, err := st.db.PrepareContext(ctx, `
		INSERT INTO artists (id, name, sort_name, disambiguation, active, in_library)
		VALUES (?, ?, ?, ?, 1, 1)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			sort_name=excluded.sort_name,
			disambiguation=excluded.disambiguation,
			active=1,
			in_library=1`)
	if err != nil {
		return st.prepareQueryError(err)
	}
	defer func() { _ = stmt.Close() }()

	_, err = stmt.ExecContext(ctx, art.ID, art.Name, art.SortName, art.Disambiguation)
	if err != nil {
		return fmt.Errorf("store: insert artist: %w", err)
	}

	return nil
}

func (st *Store) DeleteArtist(ctx context.Context, id string) (int64, error) {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: start tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM artists WHERE id = ? AND active = 1)`, id,
	).Scan(&exists); err != nil {
		return 0, fmt.Errorf("store: check artist: %w", err)
	}
	if !exists {
		return 0, ErrNotFound
	}

	// Release groups where the artist is the primary credit (position 0).
	// Groups where the artist is only a guest (position > 0) are handled
	// below. Releases and tracks cascade-delete; linked files auto-unlink
	// via ON DELETE SET NULL.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM release_groups WHERE id IN (
			SELECT rg.id
			FROM release_groups rg
			JOIN artist_credit_items aci
			  ON aci.artist_credit_id = rg.artist_credit_id
			WHERE aci.artist_id = ? AND aci.position = 0
		)`, id,
	); err != nil {
		return 0, fmt.Errorf("store: delete release groups: %w", err)
	}

	// Guest groups left with no active library artist behind (the deleted
	// artist appears, but nobody else in the credits is still in the
	// library). Groups shared with a surviving library artist are kept with
	// their credits intact. The deleted id is excluded explicitly so this
	// works regardless of statement order within the tx.
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
		return 0, fmt.Errorf("store: delete orphan release groups: %w", err)
	}

	// Soft-delete: keep the artist row so surviving collaboration credits
	// still resolve (artist_credit_items.artist_id is ON DELETE RESTRICT).
	// in_library is cleared too so the row stays out of both the default
	// library view and ?all=true.
	res, err := tx.ExecContext(ctx,
		`UPDATE artists SET active = 0, in_library = 0 WHERE id = ? AND active = 1`, id)
	if err != nil {
		return 0, fmt.Errorf("store: delete artist: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: delete artist: %w", err)
	}
	if n == 0 {
		return 0, ErrNotFound
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
		return 0, fmt.Errorf("store: delete orphan credits: %w", err)
	}

	// Drop collaborators left trackless by the deletes above (they only
	// ever appeared on this artist's releases). Same tx so delete + prune
	// commit atomically.
	pruned, err := pruneOrphanCollaboratorsTx(ctx, tx)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit delete artist: %w", err)
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
		return 0, fmt.Errorf("store: start tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	n, err := pruneOrphanCollaboratorsTx(ctx, tx)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit prune collaborators: %w", err)
	}

	return n, nil
}

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
		return 0, fmt.Errorf("store: prune collaborators: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune collaborators: %w", err)
	}
	return n, nil
}
