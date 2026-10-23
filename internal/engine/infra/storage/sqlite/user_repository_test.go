package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go-software-factory/internal/core/domain"
	appauth "go-software-factory/internal/engine/app/auth"
	infraauth "go-software-factory/internal/engine/infra/auth"

	"github.com/mattn/go-sqlite3"
)

func TestUserPersistenceAndAdminInitialization(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := &SqliteConfig{Path: filepath.Join(t.TempDir(), "users.db"), MaxOpenConns: 2, Pragmas: PragmasSettings}
	db, err := NewDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := db.Start(ctx); err != nil {
		t.Fatal(err)
	}
	repository := NewUserRepository(db.DB)
	if err := infraauth.NewAdminInitializer(repository, "", "").Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByUsername(ctx, adminUsername); !errors.Is(err, appauth.ErrUserNotFound) {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{adminUsername, ""}, {"", "password"}} {
		if err := infraauth.NewAdminInitializer(repository, pair[0], pair[1]).Start(ctx); err == nil {
			t.Fatal("partial credentials accepted")
		}
	}
	initializer := infraauth.NewAdminInitializer(repository, adminUsername, "initial password")
	if err := initializer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := repository.GetByUsername(ctx, adminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != domain.UserStateActive || first.Role != domain.UserRoleAdmin || first.CreatedAt.IsZero() ||
		!first.CreatedAt.Equal(first.UpdatedAt) ||
		first.Password == "initial password" {
		t.Fatalf("invalid seeded user: state %s role %s", first.State, first.Role)
	}
	login := appauth.NewLoginUseCase(repository, infraauth.Verify)
	if identity, err := login.Login(ctx, adminUsername, "initial password"); err != nil || identity != adminUsername {
		t.Fatalf("seed login = %q, %v", identity, err)
	}
	if _, err := login.Login(ctx, "Admin", "initial password"); !errors.Is(err, appauth.ErrInvalidCredentials) {
		t.Fatal("username must be exact")
	}
	if err := infraauth.NewAdminInitializer(repository, adminUsername, "replacement password").Start(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := repository.GetByUsername(ctx, adminUsername)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("existing admin changed")
	}
	// Neither initializer nor Create may reactivate or overwrite existing users.
	if _, err := db.ExecContext(
		ctx,
		`UPDATE users SET state = 'TERMINATED', role = 'OTHER' WHERE username = 'admin'`,
	); err != nil {
		t.Fatal(err)
	}
	existing, err := repository.GetByUsername(ctx, adminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if err := initializer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	err = repository.Create(
		ctx,
		&domain.User{
			Username:  adminUsername,
			Password:  "replacement",
			State:     domain.UserStateActive,
			Role:      domain.UserRoleAdmin,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	)
	if !errors.Is(err, appauth.ErrDuplicate) {
		t.Fatalf("duplicate create = %v; want ErrDuplicate", err)
	}
	var sqlErr sqlite3.Error
	if !errors.As(err, &sqlErr) || sqlErr.ExtendedCode != sqlite3.ErrConstraintPrimaryKey {
		t.Fatalf("duplicate error must preserve the SQL primary-key error: %v", err)
	}

	again, err = repository.GetByUsername(ctx, adminUsername)
	if err != nil || !reflect.DeepEqual(existing, again) {
		t.Fatal("existing user overwritten")
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := initializer.Start(ctx); err == nil {
		t.Fatal("database failure hidden")
	}
	reopened, err := NewDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := reopened.Start(ctx); err != nil {
		t.Fatal(err)
	}
	again, err = NewUserRepository(reopened.DB).GetByUsername(ctx, adminUsername)
	if err != nil || !reflect.DeepEqual(existing, again) {
		t.Fatal("user did not survive restart")
	}
	// Non-duplicate SQL constraint failures must still stop initialization.
	if _, err := reopened.ExecContext(
		ctx,
		`CREATE TRIGGER reject_user
		BEFORE INSERT ON users
		BEGIN SELECT RAISE(ABORT, 'insert failed'); END;`,
	); err != nil {
		t.Fatal(err)
	}
	err = infraauth.NewAdminInitializer(NewUserRepository(reopened.DB), "another", "password").Start(ctx)
	if err == nil || errors.Is(err, appauth.ErrDuplicate) {
		t.Fatalf("non-duplicate constraint failure misclassified: %v", err)
	}
	if !errors.As(err, &sqlErr) || sqlErr.ExtendedCode != sqlite3.ErrConstraintTrigger {
		t.Fatalf("initializer must propagate the SQL trigger error: %v", err)
	}
}
