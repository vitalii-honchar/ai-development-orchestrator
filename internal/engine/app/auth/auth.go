package auth

import (
	"context"
	"errors"
	"go-software-factory/internal/core/domain"
)

var ErrDuplicate = errors.New("duplicate user")
var ErrUserNotFound = errors.New("user not found")
var ErrInvalidCredentials = errors.New("invalid username or password")

type UserRepository interface {
	GetByUsername(context.Context, string) (*domain.User, error)
	Create(context.Context, *domain.User) error
}

type PasswordVerifier func(password, encodedHash string) (bool, error)

type LoginUseCase struct {
	repository     UserRepository
	verifyPassword PasswordVerifier
}

func NewLoginUseCase(repository UserRepository, verifyPassword PasswordVerifier) *LoginUseCase {
	return &LoginUseCase{repository: repository, verifyPassword: verifyPassword}
}

// Login returns only the authenticated identity, never the stored password hash.
func (l *LoginUseCase) Login(ctx context.Context, username, password string) (string, error) {
	if username == "" || password == "" {
		return "", ErrInvalidCredentials
	}
	user, err := l.repository.GetByUsername(ctx, username)
	if errors.Is(err, ErrUserNotFound) {
		return "", ErrInvalidCredentials
	}
	if err != nil {
		return "", err
	}
	if user.State != domain.UserStateActive {
		return "", ErrInvalidCredentials
	}
	valid, err := l.verifyPassword(password, user.Password)
	if err != nil {
		return "", err
	}
	if !valid {
		return "", ErrInvalidCredentials
	}

	return user.Username, nil
}
