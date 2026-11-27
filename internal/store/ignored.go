package store

import (
	"context"
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
// A later artist refresh or re-add recreates it; nothing refreshes eagerly.
func (st *Store) RemoveIgnored(ctx context.Context, id string) error {
	res, err := st.db.ExecContext(ctx, `DELETE FROM ignored WHERE id = ?`, id)
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete ignored: %w", err),
		)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("delete ignored: %w", err),
		)
	}
	if n == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}
