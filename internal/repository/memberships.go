// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MembershipRepo struct {
	db *pgxpool.Pool
}

func NewMembershipRepo(db *pgxpool.Pool) *MembershipRepo {
	return &MembershipRepo{db: db}
}

func (r *MembershipRepo) Add(ctx context.Context, tx pgx.Tx, id, libraryID, userID, roleID uuid.UUID, invitedBy *uuid.UUID) error {
	const q = `
		INSERT INTO library_memberships (id, library_id, user_id, role_id, invited_by)
		VALUES ($1, $2, $3, $4, $5)`

	_, err := tx.Exec(ctx, q, id, libraryID, userID, roleID, invitedBy)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicate
		}
		return fmt.Errorf("adding member: %w", err)
	}
	return nil
}

func (r *MembershipRepo) ListByLibrary(ctx context.Context, libraryID uuid.UUID, search, tagFilter string) ([]*models.LibraryMember, error) {
	args := []any{libraryID}
	where := `WHERE lm.library_id = $1`
	if search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(` AND (lower(u.username) LIKE lower($%d) OR lower(u.display_name) LIKE lower($%d))`, len(args), len(args))
	}
	if tagFilter != "" {
		args = append(args, tagFilter)
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM member_tags mt2 JOIN tags t2 ON t2.id = mt2.tag_id WHERE mt2.library_id = lm.library_id AND mt2.user_id = lm.user_id AND lower(t2.name) = lower($%d))`, len(args))
	}

	q := `
		SELECT u.id, u.username, u.display_name, u.email,
		       ro.id, ro.name,
		       lm.invited_by, lm.joined_at,
		       COALESCE(
		           (SELECT json_agg(json_build_object('id', t.id, 'name', t.name, 'color', t.color) ORDER BY t.name)
		            FROM member_tags mt JOIN tags t ON t.id = mt.tag_id WHERE mt.library_id = lm.library_id AND mt.user_id = lm.user_id),
		           '[]'::json
		       ) AS tags
		FROM library_memberships lm
		JOIN users u  ON u.id  = lm.user_id
		JOIN roles ro ON ro.id = lm.role_id
		` + where + `
		ORDER BY lm.joined_at`

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}
	defer rows.Close()

	var out []*models.LibraryMember
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *MembershipRepo) UpdateRole(ctx context.Context, libraryID, userID, roleID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`UPDATE library_memberships SET role_id = $3 WHERE library_id = $1 AND user_id = $2`,
		libraryID, userID, roleID,
	)
	if err != nil {
		return fmt.Errorf("updating member role: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *MembershipRepo) Remove(ctx context.Context, libraryID, userID uuid.UUID) error {
	result, err := r.db.Exec(ctx,
		`DELETE FROM library_memberships WHERE library_id = $1 AND user_id = $2`,
		libraryID, userID,
	)
	if err != nil {
		return fmt.Errorf("removing member: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMember(s scanner) (*models.LibraryMember, error) {
	var (
		pgUserID    pgtype.UUID
		pgRoleID    pgtype.UUID
		pgInvitedBy pgtype.UUID
		tagsJSON    []byte
		m           models.LibraryMember
	)
	err := s.Scan(
		&pgUserID, &m.Username, &m.DisplayName, &m.Email,
		&pgRoleID, &m.RoleName,
		&pgInvitedBy, &m.JoinedAt,
		&tagsJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning member: %w", err)
	}
	m.UserID = uuid.UUID(pgUserID.Bytes)
	m.RoleID = uuid.UUID(pgRoleID.Bytes)
	if pgInvitedBy.Valid {
		id := uuid.UUID(pgInvitedBy.Bytes)
		m.InvitedBy = &id
	}
	if err := json.Unmarshal(tagsJSON, &m.Tags); err != nil || m.Tags == nil {
		m.Tags = []*models.Tag{}
	}
	return &m, nil
}
