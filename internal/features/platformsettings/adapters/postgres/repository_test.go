package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/platformsettings/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSupportContactPostgres usa un esquema aislado en bpm_test con V1 y V22. Se omite sin TEST_DATABASE_URL.
func TestSupportContactPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("platform_settings_%d", time.Now().UnixNano())}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := New(pool)
	if err = repo.RequireSchema(ctx); err == nil {
		t.Skip("bpm_test public ya contiene V22; se requiere base limpia en el esquema")
	}
	for _, name := range []string{"V1__baseline.sql", "V22__onboarding_support_contact.sql", "V22__onboarding_support_contact.sql"} {
		sql, err := os.ReadFile("../../../../../migraciones/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(name, err)
		}
	}
	if err = repo.RequireSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO application_users(id,email,display_name,platform_role,status) VALUES('root','r@bpm.pe','R','SUPER_ADMIN','ACTIVE'),('off','o@bpm.pe','O','USER','INACTIVE')`); err != nil {
		t.Fatal(err)
	}
	if role, err := repo.PlatformRole(ctx, "root"); err != nil || role != "SUPER_ADMIN" {
		t.Fatal(role, err)
	}
	if _, err := repo.PlatformRole(ctx, "off"); !errors.Is(err, domain.ErrDenied) {
		t.Fatal("inactive user must be denied", err)
	}
	c, err := repo.SupportContact(ctx)
	if err != nil || c.Email != domain.DefaultSupportEmail || c.WhatsApp != "" {
		t.Fatal(c, err)
	}
	if c, err = repo.SaveSupportContact(ctx, "root", domain.SupportContact{Email: "s@bpm.pe", WhatsApp: "51987654321"}); err != nil || c.WhatsApp != "51987654321" {
		t.Fatal(c, err)
	}
	if c, err = repo.SaveSupportContact(ctx, "root", domain.SupportContact{Email: "s@bpm.pe"}); err != nil || c.WhatsApp != "" {
		t.Fatal(c, err)
	}
	var seen *time.Time
	if err = pool.QueryRow(ctx, `SELECT onboarding_seen_at FROM application_users WHERE id='root'`).Scan(&seen); err != nil || seen != nil {
		t.Fatal("onboarding_seen_at must default to NULL", seen, err)
	}
}
