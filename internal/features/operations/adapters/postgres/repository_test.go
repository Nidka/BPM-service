package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nidka/BPM-service/internal/features/operations/application"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const org = "10000000-0000-0000-0000-000000000001"
const foreign = "10000000-0000-0000-0000-000000000002"
const site = "20000000-0000-0000-0000-000000000001"
const otherSite = "20000000-0000-0000-0000-000000000002"
const supplierID = "30000000-0000-0000-0000-000000000001"
const productID = "40000000-0000-0000-0000-000000000001"
const planID = "70000000-0000-0000-0000-000000000001"

func uid(kind, n int) string    { return fmt.Sprintf("%08d-0000-0000-0000-%012d", kind, n) }
func number(n float64) *float64 { return &n }

func database(t *testing.T, beforeV5 ...func(context.Context, *pgxpool.Pool)) (context.Context, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil || cfg.ConnConfig.Database != "bpm_test" {
		t.Fatal("requires isolated bpm_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	admin, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := pgx.Identifier{fmt.Sprintf("operations_%d", time.Now().UnixNano())}.Sanitize()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 8
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"V1__baseline.sql", "V2__authentication_sessions.sql", "V4__checklist_evidence_items.sql", "V5__daily_operations.sql"} {
		if name == "V5__daily_operations.sql" && New(pool).RequireSchema(ctx) == nil {
			t.Fatal("operaciones arrancan sin V5")
		}
		if name == "V5__daily_operations.sql" {
			for _, hook := range beforeV5 {
				hook(ctx, pool)
			}
		}
		sql, e := os.ReadFile("../../../../../migraciones/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(sql)); e != nil {
			t.Fatal(name, e)
		}
	}
	if e = New(pool).RequireSchema(ctx); e != nil {
		t.Fatal("no reconoce V5", e)
	}
	return ctx, pool
}
func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO organizations(id,legal_name,business_type) VALUES($1,'Organización','RESTAURANT'),($2,'Ajena','RESTAURANT')`, org, foreign)
	exec(`INSERT INTO application_users(id,email,display_name,platform_role) VALUES('admin','admin@example.invalid','Admin','USER'),('chef','chef@example.invalid','Chef','USER'),('chef2','chef2@example.invalid','Otro chef','USER'),('worker','worker@example.invalid','Worker','USER'),('worker2','worker2@example.invalid','Otro worker','USER'),('outside','outside@example.invalid','Ajeno','USER'),('super','super@example.invalid','Super','SUPER_ADMIN')`)
	exec(`INSERT INTO organization_memberships(organization_id,user_id,role) VALUES($1,'admin','ADMIN'),($1,'chef','CHEF'),($1,'chef2','CHEF'),($1,'worker','WORKER'),($1,'worker2','WORKER'),($2,'outside','CHEF')`, org, foreign)
	exec(`INSERT INTO locations(id,organization_id,name,address,timezone) VALUES($1,$3,'Principal','Dirección','America/Lima'),($2,$3,'Otra','Dirección','America/Lima')`, site, otherSite, org)
	exec(`INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES($1,'chef',$2),($1,'worker',$2),($1,'worker2',$2),($1,'chef2',$3)`, org, site, otherSite)
}
func receipt(n int, at time.Time) domain.Receipt {
	return domain.Receipt{ID: uid(5, n), LocationID: site, SupplierID: supplierID, ReceivedAt: at, ClientOperationID: uid(8, n), ClientCreatedAt: at, Items: []domain.ReceiptItem{{ID: uid(6, n), ProductID: productID, BatchCode: "Lote", Quantity: "1.125", TemperatureCelsius: number(3), Decision: "ACCEPTED", PackagingCondition: "INTACT", StorageLocation: "Frío"}}}
}
func execution(n int, at time.Time) domain.CleaningExecution {
	return domain.CleaningExecution{ID: uid(9, n), LocationID: site, CleaningPlanID: planID, ExecutedAt: at, Result: "COMPLETED", ClientOperationID: uid(10, n), ClientCreatedAt: at}
}
func catalog(t *testing.T, ctx context.Context, s *application.Service) {
	t.Helper()
	if _, e := s.CreateSupplier(ctx, "admin", org, "supplier-key", domain.Supplier{ID: supplierID, Name: "Proveedor"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateProduct(ctx, "admin", org, "product-key", domain.Product{ID: productID, Name: "Producto", Unit: "kg", RequiresTemperature: true, MinimumTemperature: number(1), MaximumTemperature: number(5)}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreatePlan(ctx, "admin", org, "plan-key", domain.CleaningPlan{ID: planID, LocationID: site, Area: "Cocina", Task: "Limpiar mesa", Frequency: "DAILY"}); e != nil {
		t.Fatal(e)
	}
}
func TestDailyOperationsPostgres(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	repo := New(pool)
	s := application.New(repo)
	catalog(t, ctx, s)
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	t.Run("permisos y contextos vigentes", func(t *testing.T) {
		for _, user := range []string{"admin", "super", "worker", "outside", "chef2"} {
			if _, e := s.Receive(ctx, user, org, "denied-receipt", receipt(1, now)); e == nil {
				t.Fatal("recibió sin permiso", user)
			}
		}
		for _, user := range []string{"worker", "outside"} {
			if _, e := s.Products(ctx, user, org, pagination.Request{}); e == nil {
				t.Fatal("leyó catálogo", user)
			}
		}
		for _, user := range []string{"chef", "worker", "outside"} {
			if _, e := s.CreateProduct(ctx, user, org, "denied-product", domain.Product{ID: uid(4, 9), Name: "Otro", Unit: "kg"}); e == nil {
				t.Fatal("creó catálogo", user)
			}
		}
		if e := repo.Within(ctx, func(db application.Store) error {
			a, e := db.Actor(ctx, "chef", org)
			if e != nil {
				return e
			}
			zone, e := db.Location(ctx, a, org, site)
			if zone != "America/Lima" {
				t.Fatal(zone)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("reglas sanitarias impiden stock y observación no crea lote", func(t *testing.T) {
		for i, change := range []func(*domain.ReceiptItem){func(i *domain.ReceiptItem) { i.PackagingCondition = "DAMAGED" }, func(i *domain.ReceiptItem) { i.ExpirationDate = "2000-01-01" }, func(i *domain.ReceiptItem) { i.TemperatureCelsius = number(7) }} {
			v := receipt(11+i, now)
			change(&v.Items[0])
			if _, e := s.Receive(ctx, "chef", org, fmt.Sprintf("invalid-%d", i), v); e == nil {
				t.Fatal("aceptó insumo inválido", i)
			}
		}
		v := receipt(15, now)
		v.Items[0].Decision = "OBSERVED"
		v.Items[0].RejectionReason = "Pendiente"
		v.Items[0].StorageLocation = ""
		v.Items[0].TemperatureCelsius = number(9)
		if _, e := s.Receive(ctx, "chef", org, "observed-key", v); e != nil {
			t.Fatal(e)
		}
		var count int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM storage_lots WHERE organization_id=$1`, org).Scan(&count); e != nil || count != 0 {
			t.Fatal(count, e)
		}
	})
	t.Run("reintentos concurrentes y otra clave conservan una recepción lote y auditoría", func(t *testing.T) {
		v := receipt(1, now)
		var wg sync.WaitGroup
		out := make(chan error, 3)
		for range 3 {
			wg.Add(1)
			go func() { defer wg.Done(); _, e := s.Receive(ctx, "chef", org, "concurrent-key", v); out <- e }()
		}
		wg.Wait()
		close(out)
		for e := range out {
			if e != nil {
				t.Fatal(e)
			}
		}
		if _, e := s.Receive(ctx, "chef", org, "new-replay-key", v); e != nil {
			t.Fatal(e)
		}
		for _, table := range []string{"supply_receipts", "storage_lots"} {
			var count int
			if e := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE organization_id=$1 AND id=$2", org, map[string]string{"supply_receipts": v.ID, "storage_lots": v.Items[0].ID}[table]).Scan(&count); e != nil || count != 1 {
				t.Fatal(table, count, e)
			}
		}
		var count int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND entity_id=$2`, org, v.ID).Scan(&count); e != nil || count != 1 {
			t.Fatal("auditoría duplicada", count, e)
		}
		changed := receipt(1, now)
		changed.Notes = "Otro cuerpo"
		if _, e := s.Receive(ctx, "chef", org, "changed-key", changed); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("mismo ID redefinido", e)
		}
		changed = receipt(2, now)
		changed.ClientOperationID = v.ClientOperationID
		if _, e := s.Receive(ctx, "chef", org, "changed-client-key", changed); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("misma operación redefinida", e)
		}
		changed = receipt(1, now)
		if _, e := s.Receive(ctx, "worker", org, "concurrent-key", changed); e == nil {
			t.Fatal("replay omite autorización")
		}
	})
	t.Run("snapshot y política versionada", func(t *testing.T) {
		if _, e := s.UpdatePolicy(ctx, "admin", org, productID, "policy-key", domain.TemperaturePolicy{RowVersion: 1, RequiresTemperature: true, MinimumTemperature: number(2), MaximumTemperature: number(4)}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.UpdatePolicy(ctx, "admin", org, productID, "stale-policy-key", domain.TemperaturePolicy{RowVersion: 1, RequiresTemperature: false}); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("versión vieja", e)
		}
		got, e := s.Receipt(ctx, "admin", org, uid(5, 1))
		if e == nil && got.ReceivedByName != "Chef" {
			t.Fatal("nombre del receptor omitido", got.ReceivedByName)
		}
		if e != nil || got.Items[0].MinimumTemperature == nil || *got.Items[0].MinimumTemperature != 1 || *got.Items[0].MaximumTemperature != 5 {
			t.Fatal("snapshot alterado", got, e)
		}
		if _, e = s.Receive(ctx, "chef", org, "replay-after-policy", receipt(1, now)); e != nil {
			t.Fatal("replay con nueva política", e)
		}
	})
	t.Run("limpieza separa ejecutores y sedes", func(t *testing.T) {
		for i, user := range []string{"worker", "worker2", "chef"} {
			if _, e := s.Execute(ctx, user, org, fmt.Sprintf("execution-%d", i), execution(i+1, now)); e != nil {
				t.Fatal(user, e)
			}
		}
		got, e := s.Executions(ctx, "worker", org, site, pagination.Request{})
		if e != nil || len(got.Items) != 1 || got.Items[0].ResponsibleUserID != "worker" {
			t.Fatal(got, e)
		}
		team, e := s.Executions(ctx, "admin", org, site, pagination.Request{})
		if e != nil || len(team.Items) != 3 {
			t.Fatal(team, e)
		}
		if _, e = s.Execution(ctx, "worker", org, uid(9, 2)); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("leyó ejecución ajena", e)
		}
		for _, user := range []string{"admin", "super", "chef2"} {
			if _, e = s.Execute(ctx, user, org, "wrong-executor", execution(9, now)); e == nil {
				t.Fatal("ejecutó sin permiso", user)
			}
		}
		v := execution(9, now)
		v.LocationID = otherSite
		if _, e = s.Execute(ctx, "chef2", org, "wrong-plan-site", v); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("plan de otra sede", e)
		}
	})
	t.Run("páginas PEPS y fechas descendentes completas", func(t *testing.T) {
		for i := 20; i < 25; i++ {
			if _, e := s.Receive(ctx, "chef", org, fmt.Sprintf("page-receipt-%d", i), receipt(i, now.Add(time.Duration(i)*time.Second))); e != nil {
				t.Fatal(e)
			}
		}
		seen := map[string]bool{}
		token := ""
		last := time.Now().Add(time.Hour)
		for {
			p, e := s.Receipts(ctx, "chef", org, site, pagination.Request{Size: 2, Token: token})
			if e != nil {
				t.Fatal(e)
			}
			for _, v := range p.Items {
				if seen[v.ID] || v.ReceivedAt.After(last) || len(v.Items) != 1 {
					t.Fatal("página rota", v)
				}
				seen[v.ID] = true
				last = v.ReceivedAt
			}
			token = p.NextToken
			if token == "" {
				break
			}
		}
		if len(seen) != 7 {
			t.Fatal("recepciones perdidas", len(seen))
		}
		lots, e := s.Lots(ctx, "chef", org, site, pagination.Request{Size: 2})
		if e != nil || len(lots.Items) != 2 || lots.Items[0].ID != uid(6, 1) || lots.NextToken == "" {
			t.Fatal(lots, e)
		}
		next, e := s.Lots(ctx, "chef", org, site, pagination.Request{Size: 2, Token: lots.NextToken})
		if e != nil || len(next.Items) != 2 || !next.Items[0].ReceivedAt.After(lots.Items[1].ReceivedAt) {
			t.Fatal(next, e)
		}
		if _, e = s.Lots(ctx, "chef", foreign, site, pagination.Request{Size: 2, Token: lots.NextToken}); e == nil {
			t.Fatal("cursor cruza empresa")
		}
		if _, e = s.Lots(ctx, "chef", org, otherSite, pagination.Request{Size: 2, Token: lots.NextToken}); e == nil {
			t.Fatal("cursor cruza sede")
		}
	})
	t.Run("búsqueda literal y catálogo ID estable", func(t *testing.T) {
		v := domain.Supplier{ID: uid(3, 2), Name: "Literal %_"}
		if _, e := s.CreateSupplier(ctx, "admin", org, "literal-supplier", v); e != nil {
			t.Fatal(e)
		}
		if _, e := s.CreateSupplier(ctx, "admin", org, "literal-supplier-other-key", v); e != nil {
			t.Fatal("ID idéntico", e)
		}
		v.ID = uid(3, 3)
		if _, e := s.CreateSupplier(ctx, "admin", org, "duplicate-name", v); !errors.Is(e, domain.ErrConflict) {
			t.Fatal("nombre duplicado", e)
		}
		found, e := s.Suppliers(ctx, "chef", org, pagination.Request{Query: "%_"})
		if e != nil || len(found.Items) != 1 || found.Items[0].ID != uid(3, 2) {
			t.Fatal(found, e)
		}
		for _, user := range []string{"admin", "super"} {
			if _, e := s.Products(ctx, user, org, pagination.Request{}); e != nil {
				t.Fatal(user, e)
			}
		}
	})
	t.Run("revocación y metadatos mínimos", func(t *testing.T) {
		if _, e := pool.Exec(ctx, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='chef'`, org); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Receipt(ctx, "chef", org, uid(5, 1)); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("membresía revocada", e)
		}
		var bad int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id=$1 AND metadata<>'{}'::jsonb`, org).Scan(&bad); e != nil || bad != 0 {
			t.Fatal("auditoría incluye datos sensibles", bad, e)
		}
		if e := repo.Within(ctx, func(db application.Store) error { _, e := db.Supplier(ctx, foreign, supplierID); return e }); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("proveedor cruza empresa", e)
		}
		if e := repo.Within(ctx, func(db application.Store) error { _, e := db.Product(ctx, foreign, productID); return e }); !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("producto cruza empresa", e)
		}
		if _, e := pool.Exec(ctx, `UPDATE organizations SET status='INACTIVE' WHERE id=$1`, org); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Products(ctx, "super", org, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
			t.Fatal("super leyó empresa inactiva", e)
		}
	})
}

func TestDailyOperationsLegacyAndRequiredSchema(t *testing.T) {
	ctx, pool := database(t, func(ctx context.Context, pool *pgxpool.Pool) {
		seed(t, ctx, pool)
		statements := []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO suppliers(id,organization_id,name) VALUES($1,$2,'Legado')`, []any{supplierID, org}},
			{`INSERT INTO products(id,organization_id,name,unit,requires_temperature) VALUES($1,$2,'Legado','kg',true)`, []any{productID, org}},
			{`INSERT INTO supply_receipts(id,organization_id,location_id,supplier_id,received_by,received_at,client_operation_id,client_created_at) VALUES($1,$2,$3,$4,'chef','2000-01-01T12:00:00Z',$5,'2000-01-01T12:00:00Z')`, []any{uid(5, 90), org, site, supplierID, uid(8, 90)}},
			{`INSERT INTO supply_receipt_items(id,organization_id,receipt_id,product_id,quantity,decision,expiration_date) VALUES($1,$2,$3,$4,1,'ACCEPTED','2000-01-02')`, []any{uid(6, 90), org, uid(5, 90), productID}},
			{`INSERT INTO storage_lots(id,organization_id,location_id,receipt_item_id,storage_location,label,quantity_available) VALUES($1,$2,$3,$4,'Depósito','Etiqueta',1)`, []any{uid(11, 90), org, site, uid(6, 90)}},
		}
		for _, statement := range statements {
			if _, e := pool.Exec(ctx, statement.sql, statement.args...); e != nil {
				t.Fatal(e)
			}
		}
	})
	repo := New(pool)
	s := application.New(repo)
	products, e := s.Products(ctx, "admin", org, pagination.Request{})
	if e != nil || len(products.Items) != 1 || !products.Items[0].RequiresTemperature || products.Items[0].MinimumTemperature != nil || products.Items[0].MaximumTemperature != nil || products.Items[0].RowVersion != 1 {
		t.Fatal("inventó criterio legado", products, e)
	}
	got, e := s.Receipt(ctx, "admin", org, uid(5, 90))
	if e != nil || len(got.Items) != 1 || got.Items[0].PackagingCondition != "" || got.Items[0].MinimumTemperature != nil || got.Items[0].StorageLocation != "Depósito" || got.Items[0].Label != "Etiqueta" {
		t.Fatal("inventó envase o perdió ubicación legado", got, e)
	}
	if got.RequestHash != "" {
		t.Fatal("inventó hash de legado")
	}
	lots, e := s.Lots(ctx, "chef", org, site, pagination.Request{})
	if e != nil || len(lots.Items) != 1 || !lots.Items[0].Expired || lots.Items[0].ID != uid(11, 90) || lots.Items[0].ReceiptItemID != uid(6, 90) {
		t.Fatal("vencimiento por sede", lots, e)
	}
	v := receipt(91, time.Now().UTC().Add(-time.Hour))
	if _, e = s.Receive(ctx, "chef", org, "legacy-no-policy", v); e == nil {
		t.Fatal("aceptó producto sin criterio definido")
	}
	if _, e = pool.Exec(ctx, `DROP INDEX storage_lots_receipt_item_uq`); e != nil {
		t.Fatal(e)
	}
	if e = repo.RequireSchema(ctx); e == nil {
		t.Fatal("arranca sin unicidad lote/item")
	}
}

func TestDailyOperationsAllKeysetPagesAndLiteralQueries(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool))
	catalog(t, ctx, s)
	names := []string{"Zulu", "Alpha", "Éclair", "Área", "Literal %_", "Beta"}
	for i, name := range names {
		if _, e := s.CreateSupplier(ctx, "admin", org, fmt.Sprintf("supplier-page-%d", i), domain.Supplier{ID: uid(3, 100+i), Name: name}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.CreateProduct(ctx, "admin", org, fmt.Sprintf("product-page-%d", i), domain.Product{ID: uid(4, 100+i), Name: name, Unit: "kg"}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.CreatePlan(ctx, "admin", org, fmt.Sprintf("plan-page-%d", i), domain.CleaningPlan{ID: uid(7, 100+i), LocationID: site, Area: name, Task: "Tarea", Frequency: "WEEKLY", DueTime: "23:59"}); e != nil {
			t.Fatal(e)
		}
	}
	for _, kind := range []string{"supplier", "product", "plan"} {
		token := ""
		last := ""
		seen := map[string]bool{}
		for {
			var entries []struct{ id, key string }
			var next string
			var err error
			switch kind {
			case "supplier":
				p, e := s.Suppliers(ctx, "chef", org, pagination.Request{Size: 2, Token: token})
				err = e
				next = p.NextToken
				for _, v := range p.Items {
					entries = append(entries, struct{ id, key string }{v.ID, strings.ToLower(v.Name)})
				}
			case "product":
				p, e := s.Products(ctx, "chef", org, pagination.Request{Size: 2, Token: token})
				err = e
				next = p.NextToken
				for _, v := range p.Items {
					entries = append(entries, struct{ id, key string }{v.ID, strings.ToLower(v.Name)})
				}
			case "plan":
				p, e := s.Plans(ctx, "worker", org, site, pagination.Request{Size: 2, Token: token})
				err = e
				next = p.NextToken
				for _, v := range p.Items {
					entries = append(entries, struct{ id, key string }{v.ID, strings.ToLower(v.Area + " " + v.Task)})
				}
			}
			if err != nil {
				t.Fatal(kind, err)
			}
			for _, v := range entries {
				key := v.key + "|" + v.id
				if seen[v.id] || last > key {
					t.Fatal("página no ordenada", kind, key, last)
				}
				seen[v.id] = true
				last = key
			}
			token = next
			if token == "" {
				break
			}
		}
		if len(seen) != 7 {
			t.Fatal("catálogo truncado", kind, len(seen))
		}
	}
	literal, e := s.Products(ctx, "chef", org, pagination.Request{Query: "%_"})
	if e != nil || len(literal.Items) != 1 || literal.Items[0].Name != "Literal %_" {
		t.Fatal(literal, e)
	}
	literalPlans, e := s.Plans(ctx, "worker", org, site, pagination.Request{Query: "%_"})
	if e != nil || len(literalPlans.Items) != 1 {
		t.Fatal(literalPlans, e)
	}
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	for i := 1; i <= 8; i++ {
		v := execution(100+i, at.Add(time.Duration(i/2)*time.Second))
		if _, e := s.Execute(ctx, "worker", org, fmt.Sprintf("execution-page-%d", i), v); e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	lastTime := time.Now().Add(time.Hour)
	lastID := ""
	token := ""
	for {
		p, e := s.Executions(ctx, "worker", org, site, pagination.Request{Size: 2, Token: token})
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range p.Items {
			if seen[v.ID] || v.ExecutedAt.After(lastTime) || v.ExecutedAt.Equal(lastTime) && v.ID >= lastID {
				t.Fatal("ejecución repetida o desordenada", v)
			}
			seen[v.ID] = true
			lastTime = v.ExecutedAt
			lastID = v.ID
		}
		token = p.NextToken
		if token == "" {
			break
		}
	}
	if len(seen) != 8 {
		t.Fatal("ejecuciones perdidas", len(seen))
	}
}

func TestDailyOperationsReceiptBatchOrderAndAtomicRollback(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	s := application.New(New(pool))
	catalog(t, ctx, s)
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	v := receipt(101, now)
	for i := 1; i < 50; i++ {
		item := v.Items[0]
		item.ID = uid(6, 200-i)
		v.Items = append(v.Items, item)
	}
	got, e := s.Receive(ctx, "chef", org, "fifty-item-receipt", v)
	if e != nil || len(got.Items) != 50 {
		t.Fatal("recepción truncada", len(got.Items), e)
	}
	for i := range v.Items {
		if got.Items[i].ID != v.Items[i].ID {
			t.Fatal("orden de ítems perdido", i)
		}
	}
	page, e := s.Receipts(ctx, "admin", org, site, pagination.Request{Size: 1})
	if e != nil || len(page.Items) != 1 || len(page.Items[0].Items) != 50 {
		t.Fatal("batch recortó ítems", page, e)
	}
	bad := receipt(102, now)
	duplicate := bad.Items[0]
	duplicate.ID = v.Items[0].ID
	bad.Items = append(bad.Items, duplicate)
	if _, e = s.Receive(ctx, "chef", org, "duplicate-item-fails", bad); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("ítem de recepción anterior reasignado", e)
	}
	var headers, lots, audit int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM supply_receipts WHERE organization_id=$1 AND id=$2),(SELECT count(*) FROM storage_lots WHERE organization_id=$1 AND id=$3),(SELECT count(*) FROM audit_events WHERE organization_id=$1 AND entity_id=$2::text)`, org, bad.ID, bad.Items[0].ID).Scan(&headers, &lots, &audit); e != nil || headers != 0 || lots != 0 || audit != 0 {
		t.Fatal("escritura parcial", headers, lots, audit, e)
	}
}

func TestDailyOperationsActiveScopeAndShareLocks(t *testing.T) {
	ctx, pool := database(t)
	seed(t, ctx, pool)
	repo := New(pool)
	s := application.New(repo)
	catalog(t, ctx, s)
	for _, change := range []struct {
		inactive, restore string
		want              error
	}{
		{`UPDATE application_users SET status='INACTIVE' WHERE id='worker'`, `UPDATE application_users SET status='ACTIVE' WHERE id='worker'`, domain.ErrDenied},
		{`UPDATE locations SET status='INACTIVE' WHERE id='` + site + `'`, `UPDATE locations SET status='ACTIVE' WHERE id='` + site + `'`, domain.ErrNotFound},
		{`DELETE FROM membership_locations WHERE user_id='worker' AND organization_id='` + org + `'`, `INSERT INTO membership_locations(organization_id,user_id,location_id) VALUES('` + org + `','worker','` + site + `')`, domain.ErrDenied},
	} {
		if _, e := pool.Exec(ctx, change.inactive); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Plans(ctx, "worker", org, site, pagination.Request{}); !errors.Is(e, change.want) {
			t.Fatal("contexto revocado sigue accesible", e)
		}
		if _, e := pool.Exec(ctx, change.restore); e != nil {
			t.Fatal(e)
		}
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- repo.Within(ctx, func(db application.Store) error {
			a, e := db.Actor(ctx, "worker", org)
			if e != nil {
				return e
			}
			if _, e = db.Location(ctx, a, org, site); e != nil {
				return e
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	updateCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	_, e := pool.Exec(updateCtx, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='worker'`, org)
	cancel()
	close(release)
	if held := <-done; held != nil {
		t.Fatal(held)
	}
	if e == nil {
		t.Fatal("autorización sin bloqueo FOR SHARE")
	}
	if _, e = pool.Exec(ctx, `UPDATE organization_memberships SET status='INACTIVE' WHERE organization_id=$1 AND user_id='worker'`, org); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Plans(ctx, "worker", org, site, pagination.Request{}); !errors.Is(e, domain.ErrDenied) {
		t.Fatal(e)
	}
}
