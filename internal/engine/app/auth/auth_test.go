package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-software-factory/internal/core/domain"
)

const adminUsername = "admin"

type userRepositoryStub struct {
	user *domain.User
	err  error
}

func (r *userRepositoryStub) GetByUsername(context.Context, string) (*domain.User, error) {
	return r.user, r.err
}

func (r *userRepositoryStub) Create(context.Context, *domain.User) error {
	return nil
}

type verifierStub struct {
	valid bool
	err   error
}

func (v verifierStub) Verify(string, string) (bool, error) {
	return v.valid, v.err
}

func TestLogin(t *testing.T) {
	t.Parallel()
	failure := errors.New("storage unavailable")
	malformed := errors.New("malformed hash")
	for _, tc := range []struct {
		name    string
		state   domain.UserState
		repoErr error
		verify  verifierStub
		want    error
	}{
		{"active", domain.UserStateActive, nil, verifierStub{valid: true}, nil},
		{"wrong password", domain.UserStateActive, nil, verifierStub{}, ErrInvalidCredentials},
		{"missing", "", ErrUserNotFound, verifierStub{}, ErrInvalidCredentials},
		{"terminated", domain.UserStateTerminated, nil, verifierStub{valid: true}, ErrInvalidCredentials},
		{"storage error", "", failure, verifierStub{}, failure},
		{"malformed hash", domain.UserStateActive, nil, verifierStub{err: malformed}, malformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			useCase := NewLoginUseCase(
				&userRepositoryStub{
					user: &domain.User{Username: adminUsername, Password: "hash", State: tc.state},
					err:  tc.repoErr,
				},
				tc.verify.Verify,
			)
			identity, err := useCase.Login(ctx, adminUsername, "password")
			if !errors.Is(err, tc.want) || (err == nil && identity != adminUsername) || (err != nil && identity != "") {
				t.Fatalf("Login = %q, %v; want %v", identity, err, tc.want)
			}
		})
	}
}

func TestEmptyCredentials(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, credentials := range [][2]string{{"", "secret"}, {adminUsername, ""}} {
		if _, err := NewLoginUseCase(nil, nil).Login(
			ctx,
			credentials[0],
			credentials[1],
		); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
}
