package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

// ListIgnored returns a filtered page of permanently deleted tombstones
// plus the total count.
func (st *Store) ListIgnored(ctx context.Context, opts IgnoredListOptions) ([]IgnoredEntry, int, error) {
	conditions := []string{}
	args := []any{}

	if opts.Type != "" {
		conditions = append(conditions, "type = ?")
		args = append(args, opts.Type)
	}
	if query := strings.TrimSpace(opts.Query); query != "" {
		conditions = append(conditions, `unaccent(name) LIKE unaccent(?) ESCAPE '\'`)
		args = append(args, likePattern(query))
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int
	if err := st.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM ignored"+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("count ignored: %w", err),
		)
	}

	order := sortExpression(opts.Sort, map[string]string{
		"name":       "unaccent(name)",
		"deleted_at": "deleted_at",
	}, "name") + " " + orderDirection(opts.Order)

	queryArgs := append(append([]any{}, args...), opts.Limit, opts.Offset)
	rows, err := st.db.QueryContext(ctx, `
		SELECT id, name, type, deleted_at
		FROM ignored`+where+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query ignored: %w", err),
		)
	}
	defer func() { _ = rows.Close() }()

	items := []IgnoredEntry{}
	for rows.Next() {
		var e IgnoredEntry
		if err := rows.Scan(&e.ID, &e.Name, &e.Type, &e.DeletedAt); err != nil {
			return nil, 0, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan ignored: %w", err),
			)
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading ignored: %w", err),
		)
	}

	return items, total, nil
}

// RemoveIgnored deletes a tombstone row so the entity can be added again.
// For artists it also reactivates a soft-deleted row (active = 0) as a
// collaboration-only artist (in_library stays 0): the row only survives
// because other releases still credit it, so without this its page would
// 404 and offer no way back in. Library membership still requires an
// explicit POST /artists. Release groups keep the old behavior — nothing
// refreshes eagerly, a group returns on the next artist refresh or import.
func (st *Store) RemoveIgnored(ctx context.Context, id string) error {
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("remove ignored start tx: %w", err),
		)
	}
	defer func() { _ = tx.Rollback() }()

	var typ string
	if err := tx.QueryRowContext(ctx,
		`SELECT type FROM ignored WHERE id = ?`, id,
	).Scan(&typ); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperrors.ErrNotFound
		}
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("check ignored: %w", err),
		)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM ignored WHERE id = ?`, id); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete ignored: %w", err),
		)
	}

	if typ == "artist" {
		// Reactivate only soft-deleted collaborators, never touch a live
		// library row. A hard-deleted row (no credits left) matches
		// nothing here and is re-added via POST /artists instead.
		if _, err := tx.ExecContext(ctx,
			`UPDATE artists SET active = 1 WHERE id = ? AND active = 0 AND in_library = 0`, id,
		); err != nil {
			return apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("reactivate artist: %w", err),
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("commit remove ignored: %w", err),
		)
	}
	return nil
}
