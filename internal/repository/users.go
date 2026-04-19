// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725 (Adaléa)

package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/fireball1725/librarium-api/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepo struct {
	db *pgxpool.Pool
}

func NewUserRepo(db *pgxpool.Pool) *UserRepo {
	return &UserRepo{db: db}
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return n, nil
}

func (r *UserRepo) Create(ctx context.Context, tx pgx.Tx, id uuid.UUID, username, email, displayName string, isInstanceAdmin bool) (*models.User, error) {
	const q = `
		INSERT INTO users (id, username, email, display_name, is_instance_admin)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at`

	row := tx.QueryRow(ctx, q, id, username, email, displayName, isInstanceAdmin)
	u, err := scanUser(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("inserting user: %w", err)
	}
	return u, nil
}

func (r *UserRepo) FindByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	const q = `
		SELECT id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at
		FROM users WHERE id = $1`

	u, err := scanUser(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding user by id: %w", err)
	}
	return u, nil
}

func (r *UserRepo) FindByUsername(ctx context.Context, username string) (*models.User, error) {
	const q = `
		SELECT id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at
		FROM users WHERE username = $1`

	u, err := scanUser(r.db.QueryRow(ctx, q, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding user by username: %w", err)
	}
	return u, nil
}

func (r *UserRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	const q = `
		SELECT id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at
		FROM users WHERE email = $1`

	u, err := scanUser(r.db.QueryRow(ctx, q, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("finding user by email: %w", err)
	}
	return u, nil
}

func (r *UserRepo) ExistsByUsername(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, username).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking username existence: %w", err)
	}
	return exists, nil
}

func (r *UserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, email).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking email existence: %w", err)
	}
	return exists, nil
}

func (r *UserRepo) UpdateLastLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("updating last login: %w", err)
	}
	return nil
}

func (r *UserRepo) UpdateProfile(ctx context.Context, id uuid.UUID, displayName, email string) (*models.User, error) {
	const q = `
		UPDATE users SET display_name = $2, email = $3
		WHERE id = $1
		RETURNING id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at`

	u, err := scanUser(r.db.QueryRow(ctx, q, id, displayName, email))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("updating profile: %w", err)
	}
	return u, nil
}

// AdminUpdate performs a full update of all admin-editable fields.
func (r *UserRepo) AdminUpdate(ctx context.Context, id uuid.UUID, displayName, email string, isActive, isInstanceAdmin bool) (*models.User, error) {
	const q = `
		UPDATE users SET display_name = $2, email = $3, is_active = $4, is_instance_admin = $5
		WHERE id = $1
		RETURNING id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at`

	u, err := scanUser(r.db.QueryRow(ctx, q, id, displayName, email, isActive, isInstanceAdmin))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("admin updating user: %w", err)
	}
	return u, nil
}

func (r *UserRepo) List(ctx context.Context, limit, offset int) ([]*models.User, int, error) {
	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting users: %w", err)
	}

	const q = `
		SELECT id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at
		FROM users
		ORDER BY created_at ASC
		LIMIT $1 OFFSET $2`

	rows, err := r.db.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating users: %w", err)
	}
	return users, total, nil
}

// Search returns up to limit active users whose username, display_name, or email
// contains the query string (case-insensitive).
func (r *UserRepo) Search(ctx context.Context, query string, limit int) ([]*models.User, error) {
	const q = `
		SELECT id, username, email, display_name, is_active, is_instance_admin, created_at, updated_at, last_login_at
		FROM users
		WHERE is_active = TRUE
		  AND (
		        username     ILIKE '%' || $1 || '%'
		     OR display_name ILIKE '%' || $1 || '%'
		     OR email        ILIKE '%' || $1 || '%'
		  )
		ORDER BY display_name
		LIMIT $2`

	rows, err := r.db.Query(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("searching users: %w", err)
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	return nil
}

// scanner is satisfied by both pgx.Row and pgx.Rows, allowing scanUser to be
// reused for both single-row queries and iteration.
type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (*models.User, error) {
	var (
		pgID        pgtype.UUID
		username    string
		email       string
		displayName string
		isActive    bool
		isAdmin     bool
		createdAt   pgtype.Timestamptz
		updatedAt   pgtype.Timestamptz
		lastLogin   pgtype.Timestamptz
	)
	if err := s.Scan(&pgID, &username, &email, &displayName, &isActive, &isAdmin, &createdAt, &updatedAt, &lastLogin); err != nil {
		return nil, err
	}
	u := &models.User{
		ID:              uuid.UUID(pgID.Bytes),
		Username:        username,
		Email:           email,
		DisplayName:     displayName,
		IsActive:        isActive,
		IsInstanceAdmin: isAdmin,
		CreatedAt:       createdAt.Time,
		UpdatedAt:       updatedAt.Time,
	}
	if lastLogin.Valid {
		t := lastLogin.Time
		u.LastLoginAt = &t
	}
	return u, nil
}
