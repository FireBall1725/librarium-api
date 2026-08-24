// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fireball1725/librarium-api/internal/api/responses"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Apply status values returned by ApplyUserBookInteractionOp.
const (
	SyncApplyStatusApplied        = "applied"
	SyncApplyStatusDiscardedStale = "discarded_stale"
	SyncApplyStatusNotFound       = "not_found"
	SyncApplyStatusInvalid        = "invalid"
)

type SyncRepo struct {
	db *pgxpool.Pool
}

func NewSyncRepo(db *pgxpool.Pool) *SyncRepo {
	return &SyncRepo{db: db}
}

// UserBookInteractionChanges returns up to `limit` ops for the given
// user's interactions that have a per-field updated_at, row updated_at,
// or deleted_at strictly greater than `since`. Ordered by row updated_at
// ascending so the client can advance its sync cursor by taking the max
// updated_at of the returned ops.
//
// Per-field LWW emits one op per changed field. A tombstone (deleted_at)
// emits a single Deleted=true op and suppresses the per-field ops for
// that row (the row is gone; per-field state is moot).
func (r *SyncRepo) UserBookInteractionChanges(ctx context.Context, userID uuid.UUID, since time.Time, limit int) ([]responses.SyncOp, error) {
	const q = `
		SELECT ub.id,
		       ub.rating, ub.rating_updated_at,
		       ub.read_status, ub.read_status_updated_at,
		       -- Progress lives on the pass, not the verdict, so it is read
		       -- from the caller's newest open session for this book. Sent
		       -- back in the shape the client writes, so a device that did
		       -- not set it still learns it.
		       (SELECT jsonb_build_object('pages_read', rs.progress_value)
		          FROM reading_sessions rs
		         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id
		           AND rs.finished_at IS NULL AND rs.progress_unit = 'page'
		         ORDER BY rs.created_at DESC LIMIT 1) AS progress,
		       (SELECT rs.progress_updated_at
		          FROM reading_sessions rs
		         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id
		           AND rs.finished_at IS NULL
		         ORDER BY rs.created_at DESC LIMIT 1) AS progress_updated_at,
		       ub.is_favorite, ub.is_favorite_updated_at,
		       ub.updated_at, ub.deleted_at
		  FROM user_books ub
		 WHERE ub.user_id = $1
		   AND (
		           ub.updated_at             > $2
		        OR ub.rating_updated_at      > $2
		        OR ub.read_status_updated_at > $2
		        OR ub.is_favorite_updated_at > $2
		        OR ub.deleted_at             > $2
		        OR EXISTS (SELECT 1 FROM reading_sessions rs
		                    WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id
		                      AND rs.progress_updated_at > $2)
		       )
		 ORDER BY ub.updated_at ASC
		 LIMIT $3`
	rows, err := r.db.Query(ctx, q, userID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("sync ubi changes: %w", err)
	}
	defer rows.Close()

	var ops []responses.SyncOp
	for rows.Next() {
		var (
			id           uuid.UUID
			rating       *int16
			ratingTS     *time.Time
			readStatus   string
			readStatusTS *time.Time
			progressRaw  []byte
			progressTS   *time.Time
			isFavorite   bool
			isFavoriteTS *time.Time
			updatedAt    time.Time
			deletedAt    *time.Time
		)
		if err := rows.Scan(
			&id,
			&rating, &ratingTS,
			&readStatus, &readStatusTS,
			&progressRaw, &progressTS,
			&isFavorite, &isFavoriteTS,
			&updatedAt, &deletedAt,
		); err != nil {
			return nil, fmt.Errorf("sync ubi scan: %w", err)
		}

		// Tombstone short-circuit: per-field state is irrelevant once
		// the row is gone. Emit one op and move on.
		if deletedAt != nil && deletedAt.After(since) {
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Deleted:    true,
				UpdatedAt:  *deletedAt,
			})
			continue
		}

		if ratingTS != nil && ratingTS.After(since) {
			var v any
			if rating != nil {
				v = *rating
			}
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "rating",
				Value:      v,
				UpdatedAt:  *ratingTS,
			})
		}
		if readStatusTS != nil && readStatusTS.After(since) {
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "read_status",
				Value:      readStatus,
				UpdatedAt:  *readStatusTS,
			})
		}
		if progressTS != nil && progressTS.After(since) {
			var v any
			if len(progressRaw) > 0 {
				v = json.RawMessage(progressRaw)
			}
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "progress",
				Value:      v,
				UpdatedAt:  *progressTS,
			})
		}
		if isFavoriteTS != nil && isFavoriteTS.After(since) {
			ops = append(ops, responses.SyncOp{
				EntityType: "user_book_interaction",
				EntityID:   id,
				Field:      "is_favorite",
				Value:      isFavorite,
				UpdatedAt:  *isFavoriteTS,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync ubi rows: %w", err)
	}
	return ops, nil
}

// ApplyUserBookInteractionOp applies a single op against user_books. The
// caller's user_id is enforced as the row owner; ops targeting other users'
// rows return not_found.
//
// The entity type stays user_book_interaction because it is what iOS sends and
// the id it addresses is still one the server handed it. Reading state moved
// underneath; the protocol did not.
//
// Returns one of SyncApplyStatus* constants describing what happened.
func (r *SyncRepo) ApplyUserBookInteractionOp(ctx context.Context, userID uuid.UUID, op responses.SyncApplyOp) (string, error) {
	if op.EntityType != "user_book_interaction" {
		return SyncApplyStatusInvalid, nil
	}
	// An id from before the surrogate key existed still has to resolve. The
	// client deletes an op it is told not_found, so failing to forward it is
	// not a retry, it is the edit being thrown away.
	entityID, err := r.resolveEntityID(ctx, userID, op.EntityID)
	if err != nil {
		return "", err
	}
	op.EntityID = entityID

	if op.Deleted {
		return r.applyUBITombstone(ctx, userID, op.EntityID, op.UpdatedAt)
	}
	switch op.Field {
	case "rating":
		var rating *int16
		if op.Value != nil {
			f, ok := op.Value.(float64)
			if !ok {
				return SyncApplyStatusInvalid, nil
			}
			n := int16(f)
			if n < 1 || n > 10 {
				return SyncApplyStatusInvalid, nil
			}
			rating = &n
		}
		return r.applyUBIField(ctx, userID, op.EntityID, op.UpdatedAt, "rating", "rating_updated_at", rating)
	case "read_status":
		s, ok := op.Value.(string)
		if !ok {
			return SyncApplyStatusInvalid, nil
		}
		return r.applyUBIField(ctx, userID, op.EntityID, op.UpdatedAt, "read_status", "read_status_updated_at", s)
	case "is_favorite":
		b, ok := op.Value.(bool)
		if !ok {
			return SyncApplyStatusInvalid, nil
		}
		return r.applyUBIField(ctx, userID, op.EntityID, op.UpdatedAt, "is_favorite", "is_favorite_updated_at", b)
	case "progress":
		// Progress moved to reading_sessions, where it is a unit and a value
		// describing one pass through a book rather than an unvalidated blob on
		// the verdict. The op still addresses the opinion row, so this resolves
		// the pass it belongs to rather than asking the client to know about
		// sessions.
		//
		// This briefly returned invalid instead, on the reasoning that a client
		// told invalid would stop sending the field. The shipping iOS client
		// does the opposite: it treats invalid as acknowledged and deletes the
		// op, so a reader who set their page count saw it save locally and
		// never leave the device.
		return r.applyProgress(ctx, userID, op.EntityID, op.UpdatedAt, op.Value)
	default:
		return SyncApplyStatusInvalid, nil
	}
}

// applyUBIField does the per-field LWW UPDATE: only writes if the
// server's timestamp for the field is older than the op's, or if the
// field has never been touched. Skipped writes are reported as
// discarded_stale unless the row doesn't exist at all (not_found).
// applyProgress records how far through a book the caller is.
//
// The op addresses a user_books row, because that is what the sync protocol
// has always addressed and old clients cannot be asked to know otherwise. The
// pass it belongs to is resolved here: the caller's newest unfinished session
// for that book, or a new one if they have none open.
//
// The unit is pages, which is what the client sends, and pages need an edition
// because page 200 of a paperback is not page 200 of the omnibus. The client
// counted against whichever printing it was showing, so the book's first
// edition by position is the closest honest answer available server-side.
func (r *SyncRepo) applyProgress(ctx context.Context, userID, entityID uuid.UUID, opTS time.Time, value any) (string, error) {
	var bookID uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT book_id FROM user_books WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		entityID, userID).Scan(&bookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.classifyUBIMiss(ctx, userID, entityID)
	}
	if err != nil {
		return "", fmt.Errorf("apply progress: finding the book: %w", err)
	}

	pages, ok := pagesReadFrom(value)
	if !ok {
		// A shape nobody writes. Rejected rather than stored, since a value the
		// server cannot read is one no reader will ever see again.
		return SyncApplyStatusInvalid, nil
	}

	// Update the open session if this op is newer than what it already holds,
	// which is the same last-writer-wins rule every other field follows.
	const update = `
		UPDATE reading_sessions
		   SET progress_unit       = CASE WHEN $3::numeric IS NULL THEN NULL ELSE 'page' END,
		       progress_value      = $3,
		       progress_updated_at = $4
		 WHERE id = (SELECT id FROM reading_sessions
		              WHERE user_id = $1 AND book_id = $2 AND finished_at IS NULL
		              ORDER BY created_at DESC LIMIT 1)
		   AND (progress_updated_at IS NULL OR progress_updated_at < $4)
		   AND ($3::numeric IS NULL OR edition_id IS NOT NULL)
		 RETURNING id`
	var sessionID uuid.UUID
	err = r.db.QueryRow(ctx, update, userID, bookID, pages, opTS).Scan(&sessionID)
	if err == nil {
		return SyncApplyStatusApplied, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("apply progress: %w", err)
	}

	// Nothing updated: either the caller has no open session, or the one they
	// have already holds something newer, or it has no edition to count pages
	// against. Only the first is worth acting on.
	var openID uuid.UUID
	var openTS *time.Time
	err = r.db.QueryRow(ctx,
		`SELECT id, progress_updated_at FROM reading_sessions
		  WHERE user_id = $1 AND book_id = $2 AND finished_at IS NULL
		  ORDER BY created_at DESC LIMIT 1`, userID, bookID).Scan(&openID, &openTS)
	switch {
	case err == nil && openTS != nil && !openTS.Before(opTS):
		return SyncApplyStatusDiscardedStale, nil
	case err == nil:
		// An open session that the update skipped for want of an edition.
		if pages == nil {
			return SyncApplyStatusApplied, nil
		}
		if _, err := r.db.Exec(ctx,
			`UPDATE reading_sessions SET edition_id = (
			     SELECT id FROM book_editions WHERE book_id = $2
			      ORDER BY position, created_at, id LIMIT 1)
			  WHERE id = $1 AND edition_id IS NULL`, openID, bookID); err != nil {
			return "", fmt.Errorf("apply progress: attaching an edition: %w", err)
		}
		if err := r.db.QueryRow(ctx, update, userID, bookID, pages, opTS).Scan(&sessionID); err != nil {
			// No edition exists to count pages against, so there is nowhere
			// honest to put a page number.
			return SyncApplyStatusInvalid, nil
		}
		return SyncApplyStatusApplied, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("apply progress: reading the open session: %w", err)
	}

	// No open pass. Clearing progress on one that does not exist is already
	// true, so it needs no row.
	if pages == nil {
		return SyncApplyStatusApplied, nil
	}
	const insert = `
		INSERT INTO reading_sessions (user_id, book_id, edition_id, started_at, status,
		                              progress_unit, progress_value, progress_updated_at)
		SELECT $1, $2,
		       (SELECT id FROM book_editions WHERE book_id = $2
		         ORDER BY position, created_at, id LIMIT 1),
		       $4, 'reading', 'page', $3, $4
		 WHERE EXISTS (SELECT 1 FROM book_editions WHERE book_id = $2)`
	tag, err := r.db.Exec(ctx, insert, userID, bookID, pages, opTS)
	if err != nil {
		return "", fmt.Errorf("apply progress: opening a session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return SyncApplyStatusInvalid, nil
	}
	return SyncApplyStatusApplied, nil
}

// pagesReadFrom pulls the page count out of the progress blob the client sends.
//
// The wire shape is {pages_read?, percent?, position?} and the client only ever
// writes pages_read; null clears it. Anything else is a shape nobody writes.
func pagesReadFrom(value any) (*float64, bool) {
	if value == nil {
		return nil, true
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, present := obj["pages_read"]
	if !present || raw == nil {
		return nil, true
	}
	switch v := raw.(type) {
	case float64:
		if v < 0 {
			return nil, false
		}
		return &v, true
	case int:
		f := float64(v)
		return &f, true
	default:
		return nil, false
	}
}

func (r *SyncRepo) applyUBIField(ctx context.Context, userID, entityID uuid.UUID, opTS time.Time, valueCol, tsCol string, value interface{}) (string, error) {
	q := fmt.Sprintf(`
		UPDATE user_books
		   SET %s = $3, %s = $4, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2
		   AND (%s IS NULL OR %s < $4)
		   AND deleted_at IS NULL
		 RETURNING id`,
		valueCol, tsCol, tsCol, tsCol,
	)
	var returned uuid.UUID
	err := r.db.QueryRow(ctx, q, entityID, userID, value, opTS).Scan(&returned)
	if err == nil {
		return SyncApplyStatusApplied, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("apply %s: %w", valueCol, err)
	}
	return r.classifyUBIMiss(ctx, userID, entityID)
}

// applyUBITombstone sets deleted_at if it's NULL or older than opTS.
// A row already deleted with a newer or equal timestamp is treated as
// discarded_stale (the existing deletion wins).
func (r *SyncRepo) applyUBITombstone(ctx context.Context, userID, entityID uuid.UUID, opTS time.Time) (string, error) {
	const q = `
		UPDATE user_books
		   SET deleted_at = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2
		   AND (deleted_at IS NULL OR deleted_at < $3)
		 RETURNING id`
	var returned uuid.UUID
	err := r.db.QueryRow(ctx, q, entityID, userID, opTS).Scan(&returned)
	if err == nil {
		return SyncApplyStatusApplied, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("apply tombstone: %w", err)
	}
	return r.classifyUBIMiss(ctx, userID, entityID)
}

// classifyUBIMiss runs after an apply UPDATE returned 0 rows. It
// distinguishes "row doesn't exist (or wrong owner)" from "row exists
// but the LWW comparison rejected the write."
// resolveEntityID maps an id the client is holding onto the row it addresses.
//
// Current ids pass straight through. One minted before 000027 is looked up in
// the forwarding table, which is why that table exists: several per-edition
// rows could collapse into one per-work row, so the old ids could not simply be
// reused and every one of them still has to resolve.
//
// An id that matches nothing is returned unchanged, so the miss is classified
// downstream the way it always was.
func (r *SyncRepo) resolveEntityID(ctx context.Context, userID, entityID uuid.UUID) (uuid.UUID, error) {
	const q = `
		SELECT CASE
		         WHEN EXISTS (SELECT 1 FROM user_books WHERE id = $1 AND user_id = $2) THEN $1
		         ELSE COALESCE((SELECT l.user_book_id
		                          FROM legacy_interaction_ids l
		                          JOIN user_books ub ON ub.id = l.user_book_id AND ub.user_id = $2
		                         WHERE l.legacy_id = $1), $1)
		       END`
	var resolved uuid.UUID
	if err := r.db.QueryRow(ctx, q, entityID, userID).Scan(&resolved); err != nil {
		return uuid.Nil, fmt.Errorf("resolving entity id: %w", err)
	}
	return resolved, nil
}

func (r *SyncRepo) classifyUBIMiss(ctx context.Context, userID, entityID uuid.UUID) (string, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM user_books WHERE id = $1 AND user_id = $2)`
	var exists bool
	if err := r.db.QueryRow(ctx, q, entityID, userID).Scan(&exists); err != nil {
		return "", fmt.Errorf("classify miss: %w", err)
	}
	if !exists {
		return SyncApplyStatusNotFound, nil
	}
	return SyncApplyStatusDiscardedStale, nil
}
