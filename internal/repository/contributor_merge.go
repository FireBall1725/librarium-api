// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Duplicate contributors, and folding them together.
//
// An import spells a name three ways and the catalogue believes in three
// people: "R. A. Montgomery" with eight books, "R.A. Montgomery" with four and
// "R A Montgomery" with two, where there is one author of fourteen. The Authors
// page draws three cards, each looking like a minor contributor.

// contributorNameKey is what makes two spellings the same name.
//
// Punctuation goes, and spacing goes only between single letters. That second
// rule is the whole design: "R.A." and "R. A." have to meet, and collapsing all
// spacing to get there would also merge "Jo Anne Smith" into "Joanne Smith" and
// "De Vries" into "DeVries", which are plausibly two people each. Initials are
// unambiguous in a way that syllables are not.
//
// Hyphens and both apostrophes are stripped, so "Ten'ya Yabuno" meets "Tenya
// Yabuno", which is a romanisation choice rather than a different person.
const contributorNameKey = `regexp_replace(
        regexp_replace(lower(translate(c.name, '.''’-', '')), '[[:space:]]+', ' ', 'g'),
        '(^| )([a-z]) ', '\1\2', 'g')`

// DuplicateCandidate is one person the catalogue believes is several.
type DuplicateCandidate struct {
	// Key is the normalised name the members share. Sent so a client has
	// something stable to key a row on.
	Key     string            `json:"key"`
	Members []DuplicateMember `json:"members"`
}

// DuplicateMember is one of the spellings, with what would move if it lost.
type DuplicateMember struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Books int       `json:"books"`
}

// DuplicateCandidates finds the groups a reviewer should look at.
//
// Tombstoned rows are excluded, because a merged contributor is not a duplicate
// of anything any more; and pairs somebody has already rejected are excluded,
// or the queue offers the same wrong answer every morning forever.
//
// A group survives the dismissal filter only if every pair in it is still open.
// Rejecting one pair out of three does not silently merge the other two: the
// group is dropped and the remaining pair comes back as its own group on the
// next sweep, which is the conservative direction.
func (r *ContributorRepo) DuplicateCandidates(ctx context.Context) ([]DuplicateCandidate, error) {
	rows, err := r.db.Query(ctx, `
		WITH keyed AS (
		    SELECT c.id, c.name, `+contributorNameKey+` AS key
		      FROM contributors c
		     WHERE c.merged_into IS NULL
		),
		grouped AS (
		    SELECT key FROM keyed GROUP BY key HAVING count(*) > 1
		),
		open AS (
		    -- A group is open when no pair inside it has been dismissed.
		    SELECT g.key FROM grouped g
		     WHERE NOT EXISTS (
		         SELECT 1 FROM keyed a JOIN keyed b ON b.key = a.key AND a.id < b.id
		           JOIN contributor_not_duplicates nd
		             ON nd.lower_id = a.id AND nd.higher_id = b.id
		          WHERE a.key = g.key)
		)
		SELECT k.key, k.id, k.name,
		       (SELECT count(*)::int FROM book_contributors bc WHERE bc.contributor_id = k.id)
		  FROM keyed k JOIN open o ON o.key = k.key
		 ORDER BY k.key, 4 DESC, k.name`)
	if err != nil {
		return nil, fmt.Errorf("finding duplicate contributors: %w", err)
	}
	defer rows.Close()

	out := []DuplicateCandidate{}
	for rows.Next() {
		var (
			key    string
			member DuplicateMember
		)
		if err := rows.Scan(&key, &member.ID, &member.Name, &member.Books); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Key == key {
			out[n-1].Members = append(out[n-1].Members, member)
			continue
		}
		out = append(out, DuplicateCandidate{Key: key, Members: []DuplicateMember{member}})
	}
	return out, rows.Err()
}

// MergeResult reports what a merge actually moved.
type MergeResult struct {
	// Credits repointed at the survivor.
	Credits int `json:"credits"`
	// Collapsed counts credits that vanished because the survivor already held
	// the same role on the same book. Reported rather than swallowed: it is the
	// difference between "moved four books" and "moved three and one was
	// already there", and a reviewer checking the arithmetic deserves both.
	Collapsed int `json:"collapsed"`
	Merged    int `json:"merged"`
}

// Merge folds losers into a survivor.
//
// A tombstone, not a delete. contributors.merged_into has existed since the
// schema-tiers migration with nothing reading it, and it is what makes this
// reversible: undoing a wrong merge is nulling one column, where undoing a
// delete is restoring a backup. It also means an id a client cached still
// resolves to a real person rather than a 404, and the record that two
// spellings were one person survives.
func (r *ContributorRepo) Merge(
	ctx context.Context, survivorID uuid.UUID, loserIDs []uuid.UUID,
) (MergeResult, error) {
	var out MergeResult
	if len(loserIDs) == 0 {
		return out, nil
	}
	for _, id := range loserIDs {
		if id == survivorID {
			return out, fmt.Errorf("a contributor cannot be merged into itself")
		}
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("starting the merge: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var before int
	if err := tx.QueryRow(ctx,
		`SELECT count(*)::int FROM book_contributors WHERE contributor_id = ANY($1)`,
		loserIDs).Scan(&before); err != nil {
		return out, fmt.Errorf("counting credits: %w", err)
	}

	// ON CONFLICT, because the key is (book_id, contributor_id, role) and a
	// book that credits both spellings in the same role would collide. The
	// duplicate row is dropped rather than the merge failing, which is what the
	// reviewer meant by saying these are one person.
	tag, err := tx.Exec(ctx, `
		INSERT INTO book_contributors (book_id, contributor_id, role, display_order)
		SELECT bc.book_id, $1, bc.role, bc.display_order
		  FROM book_contributors bc
		 WHERE bc.contributor_id = ANY($2)
		ON CONFLICT (book_id, contributor_id, role) DO NOTHING`, survivorID, loserIDs)
	if err != nil {
		return out, fmt.Errorf("repointing credits: %w", err)
	}
	out.Credits = int(tag.RowsAffected())
	out.Collapsed = before - out.Credits

	if _, err := tx.Exec(ctx,
		`DELETE FROM book_contributors WHERE contributor_id = ANY($1)`, loserIDs); err != nil {
		return out, fmt.Errorf("clearing the old credits: %w", err)
	}

	// Blanks filled, nothing overwritten. A merge should not quietly replace a
	// bio somebody wrote with one from the row being folded away.
	if _, err := tx.Exec(ctx, `
		UPDATE contributors s SET
		    bio          = COALESCE(NULLIF(s.bio, ''), l.bio),
		    born_date    = COALESCE(s.born_date, l.born_date),
		    died_date    = COALESCE(s.died_date, l.died_date),
		    nationality  = COALESCE(NULLIF(s.nationality, ''), l.nationality),
		    external_ids = l.external_ids || s.external_ids,
		    updated_at   = NOW()
		  FROM (SELECT bio, born_date, died_date, nationality, external_ids
		          FROM contributors WHERE id = ANY($2)
		         ORDER BY updated_at DESC LIMIT 1) l
		 WHERE s.id = $1`, survivorID, loserIDs); err != nil {
		return out, fmt.Errorf("carrying details over: %w", err)
	}

	// Chains resolve to the same place. Merging B into C when A already points
	// at B must leave A pointing at C, or a client following A lands on a
	// tombstone.
	if _, err := tx.Exec(ctx,
		`UPDATE contributors SET merged_into = $1, updated_at = NOW()
		  WHERE merged_into = ANY($2)`, survivorID, loserIDs); err != nil {
		return out, fmt.Errorf("repointing earlier merges: %w", err)
	}

	tag, err = tx.Exec(ctx,
		`UPDATE contributors SET merged_into = $1, updated_at = NOW()
		  WHERE id = ANY($2) AND merged_into IS NULL`, survivorID, loserIDs)
	if err != nil {
		return out, fmt.Errorf("recording the merge: %w", err)
	}
	out.Merged = int(tag.RowsAffected())

	return out, tx.Commit(ctx)
}

// Dismiss records that two contributors are genuinely different people.
//
// Every pair in the group, so a group of three that is really three people is
// settled in one action rather than coming back as three pairs.
func (r *ContributorRepo) Dismiss(
	ctx context.Context, ids []uuid.UUID, by uuid.UUID,
) error {
	if len(ids) < 2 {
		return nil
	}
	var dismissedBy any
	if by != uuid.Nil {
		dismissedBy = by
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			lo, hi := ids[i], ids[j]
			// Ordered, so the pair has one row whichever way round it arrived.
			if hi.String() < lo.String() {
				lo, hi = hi, lo
			}
			if _, err := r.db.Exec(ctx, `
				INSERT INTO contributor_not_duplicates (lower_id, higher_id, dismissed_by)
				VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, lo, hi, dismissedBy); err != nil {
				return fmt.Errorf("dismissing a pair: %w", err)
			}
		}
	}
	return nil
}

// ResolveContributor follows a tombstone to whoever absorbed it.
//
// Bounded, because a cycle in the data would otherwise hang every read that
// touches it. A merge cannot create one — the constraint refuses self-reference
// and chains are repointed on the way through — but a bound is what stops one
// already in the table from taking the page down.
func (r *ContributorRepo) ResolveContributor(
	ctx context.Context, id uuid.UUID,
) (uuid.UUID, error) {
	at := id
	for i := 0; i < 8; i++ {
		var next *uuid.UUID
		err := r.db.QueryRow(ctx,
			`SELECT merged_into FROM contributors WHERE id = $1`, at).Scan(&next)
		if err == pgx.ErrNoRows {
			return uuid.Nil, ErrNotFound
		}
		if err != nil {
			return uuid.Nil, fmt.Errorf("resolving contributor: %w", err)
		}
		if next == nil {
			return at, nil
		}
		at = *next
	}
	return at, nil
}
