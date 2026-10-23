package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/engine/app/auth"

	"github.com/mattn/go-sqlite3"
)

type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	var user domain.User
	err := r.db.QueryRowContext(
		ctx,
		`SELECT username, password, state, role, created_at, updated_at FROM users WHERE username = ?`,
		username,
	).
		Scan(&user.Username, &user.Password, &user.State, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	return &user, nil
}

func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO users (username, password, state, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		user.Username, user.Password, user.State, user.Role, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		var sqlErr sqlite3.Error
		if errors.As(err, &sqlErr) &&
			(sqlErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey ||
				sqlErr.ExtendedCode == sqlite3.ErrConstraintUnique) {
			return fmt.Errorf("create user: %w: %w", auth.ErrDuplicate, err)
		}

		return fmt.Errorf("create user: %w", err)
	}

	return nil
}
