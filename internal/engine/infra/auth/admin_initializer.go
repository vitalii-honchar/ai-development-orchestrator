package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-software-factory/internal/core/domain"
	appauth "go-software-factory/internal/engine/app/auth"
)

type AdminInitializer struct {
	repository appauth.UserRepository
	username   string
	password   string
}

var errAdminCredentials = errors.New("ADMIN_USERNAME and ADMIN_PASSWORD must be set together")

func NewAdminInitializer(repository appauth.UserRepository, username, password string) *AdminInitializer {
	return &AdminInitializer{repository: repository, username: username, password: password}
}

func (a *AdminInitializer) Start(ctx context.Context) error {
	if a.username == "" && a.password == "" {
		return nil
	}
	if a.username == "" || a.password == "" {
		return errAdminCredentials
	}
	hash, err := Hash(a.password)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := a.repository.Create(ctx, &domain.User{
		Username: a.username, Password: hash, State: domain.UserStateActive,
		Role: domain.UserRoleAdmin, CreatedAt: now, UpdatedAt: now,
	}); err != nil && !errors.Is(err, appauth.ErrDuplicate) {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}

	return nil
}

func (a *AdminInitializer) Stop(context.Context) error {
	return nil
}

func (a *AdminInitializer) Name() string {
	return "auth-admin-initializer"
}
