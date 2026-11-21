package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	apperrors "github.com/ppablomunoz/coda/internal/errors"
)

func (st *Store) getCreditEntries(
	ctx context.Context,
	seen map[int64]struct{},
) (map[int64][]CreditEntry, error) {
	ph := placeholders(len(seen))

	args := make([]any, 0, len(seen))
	for id := range seen {
		args = append(args, id)
	}

	creditRows, err := st.db.QueryContext(ctx, `
		SELECT aci.artist_credit_id, aci.artist_id, a.name, a.sort_name,
		       a.disambiguation, aci.credited_name, aci.join_phrase
		FROM artist_credit_items aci
		JOIN artists a ON a.id = aci.artist_id
		WHERE aci.artist_credit_id IN (`+ph+`)
		ORDER BY aci.artist_credit_id, aci.position`,
		args...)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query credits: %w", err),
		)
	}
	defer func() { _ = creditRows.Close() }()

	byCredit := map[int64][]CreditEntry{}
	for creditRows.Next() {
		var cid int64
		var e CreditEntry
		var sortName, disamb sql.NullString
		if err := creditRows.Scan(&cid, &e.ArtistID, &e.Name, &sortName, &disamb, &e.CreditedName, &e.JoinPhrase); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan credit: %w", err),
			)
		}
		e.SortName = nullString(sortName)
		e.Disambiguation = nullString(disamb)
		byCredit[cid] = append(byCredit[cid], e)
	}
	if err := creditRows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading credits: %w", err),
		)
	}

	return byCredit, nil
}

func (st *Store) getCreditEntry(ctx context.Context, id int64) ([]CreditEntry, error) {
	creditRows, err := st.db.QueryContext(ctx, `
		SELECT aci.artist_id, a.name, a.sort_name,
		       a.disambiguation, aci.credited_name, aci.join_phrase
		FROM artist_credit_items aci
		JOIN artists a ON a.id = aci.artist_id
		WHERE aci.artist_credit_id = ?
		ORDER BY aci.artist_credit_id, aci.position`,
		id,
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("query credits: %w", err),
		)
	}
	defer func() { _ = creditRows.Close() }()

	credits := []CreditEntry{}

	for creditRows.Next() {
		var e CreditEntry
		if err := creditRows.Scan(
			&e.ArtistID, &e.Name, &e.SortName,
			&e.Disambiguation, &e.CreditedName, &e.JoinPhrase,
		); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("scan credit: %w", err),
			)
		}
		credits = append(credits, e)
	}
	if err := creditRows.Err(); err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("reading credits: %w", err),
		)
	}

	return credits, nil
}

// createCreditTx ensures every credited artist exists and inserts a new
// artist_credits row with ordered items. Empty credit returns nil (NULL FK).
// Callers run it inside their tx so credit + entity stay atomic.
func createCreditTx(ctx context.Context, tx *sql.Tx, entries []CreditEntry) (*int64, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	for _, e := range entries {
		if strings.TrimSpace(e.ArtistID) == "" {
			return nil, apperrors.Wrap(
				apperrors.ErrInvalidInput,
				fmt.Errorf("credit missing artist id"),
			)
		}
		if strings.TrimSpace(e.Name) == "" {
			return nil, apperrors.Wrap(
				apperrors.ErrInvalidInput,
				fmt.Errorf("credit missing artist name"),
			)
		}
		// Reactivate pruned collaborators (active = 0) when live credits
		// reference them again. in_library is deliberately untouched: a
		// user-deleted artist comes back as a collaborator, never as a
		// library member — re-adding is an explicit POST.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO artists (id, name, sort_name, disambiguation, in_library)
			VALUES (?, ?, ?, ?, 0)
			ON CONFLICT(id) DO UPDATE SET
				name=excluded.name,
				sort_name=excluded.sort_name,
				disambiguation=excluded.disambiguation,
				active=1`,
			e.ArtistID, e.Name, e.SortName, e.Disambiguation,
		); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("ensure credit artist: %w", err),
			)
		}
	}

	res, err := tx.ExecContext(ctx,
		"INSERT INTO artist_credits DEFAULT VALUES",
	)
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("create artist credit: %w", err),
		)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("credit id: %w", err),
		)
	}

	for i, e := range entries {
		credited := e.CreditedName
		if strings.TrimSpace(credited) == "" {
			credited = e.Name
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO artist_credit_items
				(artist_credit_id, position, artist_id, credited_name, join_phrase)
			VALUES (?, ?, ?, ?, ?)`,
			id, i, e.ArtistID, credited, e.JoinPhrase,
		); err != nil {
			return nil, apperrors.Wrap(
				apperrors.ErrInternal,
				fmt.Errorf("insert credit item: %w", err),
			)
		}
	}

	return &id, nil
}

// CleanupOrphanCredits deletes artist_credits rows no longer referenced by
// any release-group, release or track. Items cascade-delete via
// artist_credit_items. Returns rows removed. Safe to run periodically:
// SaveFullRelease commits the new credit + FK atomically, so a concurrent
// sweep only ever sees fully-linked or fully-orphaned rows.
func (st *Store) CleanupOrphanCredits(ctx context.Context) (int64, error) {
	res, err := st.db.ExecContext(ctx, `
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
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cleanup orphan credits: %w", err),
		)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, apperrors.Wrap(
			apperrors.ErrInternal,
			fmt.Errorf("cleanup orphan credits: %w", err),
		)
	}
	return n, nil
}
