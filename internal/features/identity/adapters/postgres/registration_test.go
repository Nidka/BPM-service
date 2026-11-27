package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/features/identity/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

// Todos los datos de prueba viven en un esquema propio con la migración real.
// Nunca se insertan cuentas en public.application_users.
func TestRegistrationPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for PostgreSQL integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires database bpm_test")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("cannot connect to test database")
	}
	defer admin.Close(context.Background())
	schema := pgx.Identifier{fmt.Sprintf("identity_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("cannot initialize test pool")
	}
	defer pool.Close()
	baseline, err := os.ReadFile("../../../../../migraciones/V1__baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(baseline)); err != nil {
		t.Fatal(err)
	}
	v22, err := os.ReadFile("../../../../../migraciones/V22__onboarding_support_contact.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(v22)); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(pool)
	user := domain.NewUser{ID: "test-uid-1", Email: "person@example.invalid", DisplayName: "Persona"}
	if err := repo.CreateUser(ctx, user); err != nil {
		t.Fatal("cannot register test user")
	}
	var role, status, actor, name string
	var created, updated time.Time
	err = pool.QueryRow(ctx, `SELECT platform_role,status,created_by,display_name,created_at,updated_at FROM application_users WHERE id=$1`, user.ID).Scan(&role, &status, &actor, &name, &created, &updated)
	if err != nil || role != "USER" || status != "ACTIVE" || actor != user.ID || created.IsZero() || updated.IsZero() {
		t.Fatal("registration did not enforce role/status/audit")
	}
	profile, err := repo.FindByUserID(ctx, user.ID)
	if err != nil || profile.ID != user.ID || profile.PlatformRole != domain.PlatformRoleUser || len(profile.Organizations) != 0 {
		t.Fatal("registered user must have a readable profile without organization access")
	}
	if profile.OnboardingSeenAt != nil {
		t.Fatal("a new user has not seen the onboarding")
	}
	seenAt := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	if err := repo.SetOnboardingSeen(ctx, user.ID, &seenAt); err != nil {
		t.Fatal(err)
	}
	if profile, err = repo.FindByUserID(ctx, user.ID); err != nil || profile.OnboardingSeenAt == nil || !profile.OnboardingSeenAt.Equal(seenAt) {
		t.Fatal("onboarding seen must be persisted", err)
	}
	if err := repo.SetOnboardingSeen(ctx, user.ID, nil); err != nil {
		t.Fatal(err)
	}
	if profile, err = repo.FindByUserID(ctx, user.ID); err != nil || profile.OnboardingSeenAt != nil {
		t.Fatal("onboarding seen must be cleared", err)
	}
	if !errors.Is(repo.SetOnboardingSeen(ctx, "missing", &seenAt), domain.ErrUserNotFound) {
		t.Fatal("unknown user must not be updated")
	}
	if _, err := pool.Exec(ctx, `UPDATE application_users SET status='INACTIVE' WHERE id=$1`, user.ID); err != nil {
		t.Fatal("cannot prepare replay fixture")
	}
	user.DisplayName = "Changed"
	if err := repo.CreateUser(ctx, user); err != nil {
		t.Fatal("idempotent replay failed")
	}
	if err := pool.QueryRow(ctx, `SELECT status,display_name FROM application_users WHERE id=$1`, user.ID).Scan(&status, &name); err != nil || status != "INACTIVE" || name != "Persona" {
		t.Fatal("replay modified an existing user")
	}
	user.ID = "other-uid"
	user.Email = "PERSON@example.invalid"
	if !errors.Is(repo.CreateUser(ctx, user), domain.ErrRegistrationConflict) {
		t.Fatal("email collision must not merge identities")
	}
	var count int
	if pool.QueryRow(ctx, `SELECT COUNT(*) FROM application_users`).Scan(&count) != nil || count != 1 {
		t.Fatal("unexpected registration rows")
	}
}
