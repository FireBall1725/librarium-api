// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// BookReader is what one person has recorded about a book, as it is shown to
// the others who share a library with them.
//
// Notes are absent by construction rather than blanked by a caller: the field
// is labelled private where it is written, so the way to keep that promise is
// for the query never to select it. A struct with no Notes field cannot leak
// notes through a handler that forgets.
type BookReader struct {
	UserID      uuid.UUID  `json:"user_id"`
	DisplayName string     `json:"display_name"`
	Username    string     `json:"username"`
	ReadStatus  string     `json:"read_status"`
	Rating      *int       `json:"rating,omitempty"`
	IsFavorite  bool       `json:"is_favorite"`
	Review      string     `json:"review"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ReadersOf returns everyone who has recorded something about a book and who
// shares a library with the caller.
//
// Sharing a library is the whole permission rule. A rating is written next to a
// review the product labels "visible to members", so members are exactly who
// should see it; someone with no library in common is not a member of anything
// the caller is in.
//
// Rows with nothing on them are left out. A person who opened a book once has a
// row saying unread with no rating and no review, and listing them would fill
// the page with people who have not read it.
func (r *UserBookRepo) ReadersOf(ctx context.Context, bookID, callerID uuid.UUID) ([]*BookReader, error) {
	const q = `
		SELECT ub.user_id, u.display_name, u.username,
		       ub.read_status, ub.rating, ub.is_favorite, ub.review,
		       -- Sessions are keyed on the person and the work, not on the
		       -- user_books row, so they join on the same pair.
		       (SELECT min(rs.started_at) FROM reading_sessions rs
		         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id),
		       (SELECT max(rs.finished_at) FROM reading_sessions rs
		         WHERE rs.user_id = ub.user_id AND rs.book_id = ub.book_id),
		       ub.updated_at
		  FROM user_books ub
		  JOIN users u ON u.id = ub.user_id
		 WHERE ub.book_id = $1
		   AND ub.deleted_at IS NULL
		   -- Something to show. Unread with no rating and no review is a row
		   -- the database made, not an opinion anybody expressed.
		   AND (ub.rating IS NOT NULL OR ub.review <> '' OR ub.is_favorite
		        OR ub.read_status <> 'unread')
		   -- Shares a library with the caller. An instance can host households
		   -- that have nothing to do with each other.
		   AND EXISTS (
		       SELECT 1
		         FROM user_roles mine
		         JOIN user_roles theirs
		           ON theirs.library_id IS NOT DISTINCT FROM mine.library_id
		           OR theirs.library_id IS NULL OR mine.library_id IS NULL
		        WHERE mine.user_id = $2 AND theirs.user_id = ub.user_id)
		 ORDER BY (ub.rating IS NULL), ub.updated_at DESC`

	rows, err := r.db.Query(ctx, q, bookID, callerID)
	if err != nil {
		return nil, fmt.Errorf("listing who has read this: %w", err)
	}
	defer rows.Close()

	out := make([]*BookReader, 0)
	for rows.Next() {
		var b BookReader
		if err := rows.Scan(&b.UserID, &b.DisplayName, &b.Username,
			&b.ReadStatus, &b.Rating, &b.IsFavorite, &b.Review,
			&b.StartedAt, &b.FinishedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning a reader: %w", err)
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}
