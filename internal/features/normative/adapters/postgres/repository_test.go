package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/normative/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// database crea un esquema aislado en bpm_test con V1 y V17. Se omite sin TEST_DATABASE_URL.
func database(t *testing.T) (context.Context, *Repository) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{fmt.Sprintf("normative_%d", time.Now().UnixNano())}.Sanitize()
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
	t.Cleanup(pool.Close)
	repo := New(pool)
	if err = repo.RequireSchema(ctx); err == nil {
		t.Skip("bpm_test public ya contiene V17; se requiere base limpia en el esquema")
	}
	for _, name := range []string{"V1__baseline.sql", "V17__normative_assistant.sql"} {
		sql, err := os.ReadFile("../../../../../migraciones/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(name, err)
		}
	}
	if err = repo.RequireSchema(ctx); err != nil {
		t.Fatal("no reconoce V17", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin','admin@bpm.pe','Admin','SUPER_ADMIN'),('chef','chef@bpm.pe','Chef','USER')`); err != nil {
		t.Fatal(err)
	}
	return ctx, repo
}

const norm = `Artículo 12. Refrigeración
Los alimentos refrigerados se conservan a temperaturas de 0 °C a 4 °C.

Artículo 13. Congelación
Los productos congelados se mantienen a -18 °C o menos.

Artículo 20. Higiene del personal
El manipulador se lava las manos con agua y jabón antes de iniciar sus labores.`

func TestSearchFindsParagraphsInSpanishIgnoringAccentsAndArchived(t *testing.T) {
	ctx, repo := database(t)
	if role, err := repo.PlatformRole(ctx, "admin"); err != nil || role != "SUPER_ADMIN" {
		t.Fatalf("rol: %q %v", role, err)
	}
	if role, err := repo.PlatformRole(ctx, "chef"); err != nil || role != "USER" {
		t.Fatalf("rol sin plataforma: %q %v", role, err)
	}
	doc, err := repo.CreateDocument(ctx, "admin", NewID(), domain.DocumentInput{Title: "NTS 142", Source: "RM 822-2018/MINSA", Content: norm}, domain.Fragments(norm))
	if err != nil || doc.Characters == 0 {
		t.Fatal(err)
	}
	cases := []struct{ query, expect string }{
		{"refrigeracion", "Refrigeración"},         // sin tilde
		{"temperatura refrigerado", "0 °C a 4 °C"}, // singular frente a plural (raíz en español)
		{"CONGELADO", "-18 °C"},                    // mayúsculas
		{"lavado de manos", "agua y jabón"},        // palabras vacías y variante verbal
		{"jabon dinosaurio", "agua y jabón"},       // sin coincidencia total: cualquiera de las palabras
	}
	for _, c := range cases {
		got, err := repo.Search(ctx, c.query, 20)
		if err != nil || len(got) == 0 || !strings.Contains(got[0].Text, c.expect) || got[0].DocumentTitle != "NTS 142" || got[0].DocumentSource != "RM 822-2018/MINSA" {
			t.Fatalf("%q: %+v %v", c.query, got, err)
		}
	}
	if got, err := repo.Search(ctx, "dinosaurio", 20); err != nil || len(got) != 0 {
		t.Fatalf("sin coincidencias: %+v %v", got, err)
	}
	// Editar reemplaza los fragmentos; archivar saca la norma de la búsqueda.
	if _, err = repo.UpdateDocument(ctx, "admin", doc.ID, domain.DocumentInput{Title: "NTS 142 (2024)", Content: "Artículo 1. Cadena de frío."}, []string{"Artículo 1. Cadena de frío."}); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Search(ctx, "refrigeracion", 20); len(got) != 0 {
		t.Fatalf("debían desaparecer los fragmentos anteriores: %+v", got)
	}
	if got, _ := repo.Search(ctx, "cadena frio", 20); len(got) != 1 || got[0].DocumentTitle != "NTS 142 (2024)" {
		t.Fatalf("fragmentos nuevos: %+v", got)
	}
	if _, err = repo.SetStatus(ctx, "admin", doc.ID, domain.StatusArchived); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Search(ctx, "cadena frio", 20); len(got) != 0 {
		t.Fatalf("una norma archivada no aparece: %+v", got)
	}
	if n, _ := repo.ActiveCount(ctx); n != 0 {
		t.Fatalf("activos: %d", n)
	}
	if _, err = repo.Document(ctx, NewID()); err != domain.ErrNotFound {
		t.Fatalf("inexistente: %v", err)
	}
}
