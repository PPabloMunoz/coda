package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (st *Store) ListReleaseGroups(
	ctx context.Context,
	opts ReleaseGroupListOptions,
) ([]ReleaseGroupParams, int, error) {
	conditions := []string{}
	args := []any{}

	if opts.ArtistID != "" {
		filter := `EXISTS (
			SELECT 1 FROM artist_credit_items aci_filter
			WHERE aci_filter.artist_credit_id = rg.artist_credit_id
			  AND aci_filter.artist_id = ?`
		if opts.Primary != nil {
			if *opts.Primary {
				filter += `
			  AND aci_filter.position = 0`
			} else {
				filter += `
			  AND aci_filter.position > 0`
			}
		}
		filter += `)`
		conditions = append(conditions, filter)
		args = append(args, opts.ArtistID)
	}
	if query := strings.TrimSpace(opts.Query); query != "" {
		pattern := likePattern(query)
		conditions = append(conditions, `(unaccent(rg.title) LIKE unaccent(?) ESCAPE '\' OR EXISTS (
			SELECT 1 FROM artist_credit_items aci_query
			JOIN artists a_query ON a_query.id = aci_query.artist_id
			WHERE aci_query.artist_credit_id = rg.artist_credit_id
			  AND (unaccent(a_query.name) LIKE unaccent(?) ESCAPE '\' OR unaccent(aci_query.credited_name) LIKE unaccent(?) ESCAPE '\')))`)
		args = append(args, pattern, pattern, pattern)
	}
	if opts.Type != "" {
		conditions = append(conditions, "rg.type = ?")
		args = append(args, opts.Type)
	}
	if opts.HasRelease != nil {
		if *opts.HasRelease {
			conditions = append(conditions, `EXISTS (
				SELECT 1 FROM releases r_filter
				WHERE r_filter.release_group_id = rg.id)`)
		} else {
			conditions = append(conditions, `NOT EXISTS (
				SELECT 1 FROM releases r_filter
				WHERE r_filter.release_group_id = rg.id)`)
		}
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM release_groups rg"+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("[store] count release groups: %w", err)
	}

	dir := orderDirection(opts.Order)
	artistOrder := `unaccent(COALESCE(a0.sort_name, a0.name, aci0.credited_name))`
	typeOrder := `CASE rg.type WHEN 'album' THEN 0 WHEN 'ep' THEN 1 WHEN 'single' THEN 2 ELSE 3 END`
	titleOrder := `unaccent(rg.title)`

	order := ""
	switch opts.Sort {
	case "title":
		order = titleOrder + " " + dir
	case "type":
		order = typeOrder + " " + dir + ", " + titleOrder + " ASC"
	default:
		order = artistOrder + " " + dir + " NULLS LAST, " + typeOrder + " ASC, " + titleOrder + " ASC"
	}

	queryArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := st.db.QueryContext(ctx, `
		SELECT rg.id, rg.title, rg.type, rg.artist_credit_id
		FROM release_groups rg
		LEFT JOIN artist_credit_items aci0
		  ON aci0.artist_credit_id = rg.artist_credit_id AND aci0.position = 0
		LEFT JOIN artists a0 ON a0.id = aci0.artist_id`+where+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("[store] query release groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	groups := []ReleaseGroupParams{}
	creditIDs := []int64{}
	seen := map[int64]struct{}{}
	for rows.Next() {
		var g ReleaseGroupParams
		var creditID sql.NullInt64
		if err := rows.Scan(&g.ID, &g.Title, &g.Type, &creditID); err != nil {
			return nil, 0, fmt.Errorf("[store] scan release_group: %w", err)
		}
		if creditID.Valid {
			creditIDs = append(creditIDs, creditID.Int64)
			seen[creditID.Int64] = struct{}{}
		} else {
			creditIDs = append(creditIDs, -1)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("[store] reading rows: %w", err)
	}

	if len(seen) > 0 {
		byCredit, err := st.getCreditEntries(ctx, seen)
		if err != nil {
			return nil, 0, err
		}

		for i, id := range creditIDs {
			if id >= 0 {
				groups[i].Credit = byCredit[id]
			}
		}
	}

	if opts.IncludeRelease {
		if err := st.attachReleaseSummaries(ctx, groups); err != nil {
			return nil, 0, err
		}
	}

	return groups, total, nil
}

// attachReleaseSummaries adds the single saved release and its counts to a
// page of release groups in one query, so callers do not need one release
// request per group.
func (st *Store) attachReleaseSummaries(
	ctx context.Context,
	groups []ReleaseGroupParams,
) error {
	if len(groups) == 0 {
		return nil
	}

	ph := placeholders(len(groups))
	args := make([]any, len(groups))
	for i := range groups {
		args[i] = groups[i].ID
	}

	rows, err := st.db.QueryContext(ctx, `
		SELECT r.release_group_id, r.id, r.title, r.date, r.status,
			(SELECT COUNT(*) FROM tracks t WHERE t.release_id = r.id),
			(SELECT COUNT(DISTINCT lf.track_id) FROM local_files lf
			 JOIN tracks t ON t.id = lf.track_id
			 WHERE t.release_id = r.id)
		FROM releases r
		WHERE r.release_group_id IN (`+ph+`)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("[store] query release summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byGroup := make(map[string]*ReleaseSummary, len(groups))
	for rows.Next() {
		var groupID string
		var summary ReleaseSummary
		var date, status sql.NullString

		if err := rows.Scan(
			&groupID,
			&summary.ID,
			&summary.Title,
			&date,
			&status,
			&summary.TrackCount,
			&summary.LinkedFileCount,
		); err != nil {
			return fmt.Errorf("[store] scan release summary: %w", err)
		}

		summary.Date = nullString(date)
		summary.Status = nullString(status)

		byGroup[groupID] = &summary
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("[store] reading release summaries: %w", err)
	}

	for i := range groups {
		if summary, ok := byGroup[groups[i].ID]; ok {
			groups[i].Release = summary
		}
	}

	return nil
}

func (st *Store) GetReleaseGroup(ctx context.Context, id string) (*ReleaseGroupParams, error) {
	stmt, err := st.db.PrepareContext(ctx, `
		SELECT DISTINCT rg.id, rg.title, rg.type, rg.artist_credit_id
		FROM release_groups rg
		LEFT JOIN artist_credit_items aci ON aci.artist_credit_id = rg.artist_credit_id
		WHERE rg.id = ?`,
	)
	if err != nil {
		return nil, st.prepareQueryError(err)
	}
	defer func() { _ = stmt.Close() }()

	var g ReleaseGroupParams
	var creditID sql.NullInt64
	if err := stmt.QueryRowContext(ctx, id).Scan(
		&g.ID,
		&g.Title,
		&g.Type,
		&creditID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("[store] query release groups: %w", err)
	}

	if creditID.Valid {
		credits, err := st.getCreditEntry(ctx, creditID.Int64)
		if err != nil {
			return nil, err
		}
		g.Credit = credits
	}

	return &g, nil
}

func (st *Store) DeleteReleaseGroup(ctx context.Context, id string) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("[store]: start tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM release_groups WHERE id = ?)`, id,
	).Scan(&exists); err != nil {
		return fmt.Errorf("[store]: check release group: %w", err)
	}
	if !exists {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx,
		"DELETE FROM release_groups WHERE id = ?", id,
	); err != nil {
		return fmt.Errorf("[store]: delete release groups: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		DELETE FROM artist_credits
		WHERE NOT EXISTS (
			SELECT 1 FROM release_groups
			WHERE release_groups.artist_credit_id = artist_credits.id
		)
		AND NOT EXISTS (
			SELECT 1 FROM releases
			WHERE releases.artist_credit_id = artist_credits.id
		)
		AND NOT EXISTS (
			SELECT 1 FROM tracks
			WHERE tracks.artist_credit_id = artist_credits.id
		)`,
	)
	if err != nil {
		return fmt.Errorf("[store] cleanup orphan credits: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("[store]: commit delete release group: %w", err)
	}

	return nil
}
