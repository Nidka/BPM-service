package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Nidka/BPM-service/internal/features/operations/domain"
	"github.com/Nidka/BPM-service/internal/shared/application/pagination"
	"strings"
	"time"
)

type Service struct {
	work UnitOfWork
	now  func() time.Time
}

func New(work UnitOfWork) *Service { return &Service{work: work, now: time.Now} }
func validKey(key string) bool {
	if len(key) < 8 || len(key) > 100 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

type authorizer func(context.Context, Store, domain.Actor) error

func roleSite(org, site string, allowed func(string) bool) authorizer {
	return func(ctx context.Context, db Store, a domain.Actor) error {
		if !allowed(a.Role) {
			return domain.ErrDenied
		}
		if site != "" {
			_, e := db.Location(ctx, a, org, site)
			return e
		}
		return nil
	}
}

// write conserva cuerpo, clave y respuesta en la misma transacción; cada reintento
// vuelve a autorizar antes de consultar el recibo idempotente.
func requestHash(user, kind string, value any) (string, error) {
	body, e := json.Marshal(struct {
		Actor, Action string
		Input         any
	}{user, kind, value})
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
func write[T any](ctx context.Context, s *Service, user, org, kind, key string, input any, authorize authorizer, perform func(Store, domain.Actor) (T, error)) (out T, err error) {
	if !validKey(key) {
		return out, &domain.ValidationError{Fields: map[string]string{"idempotencyKey": "Reintenta la misma operación con su identificador original."}}
	}
	hash, e := requestHash(user, kind, input)
	if e != nil {
		return out, e
	}
	key = "operations:" + kind + ":" + key
	err = s.work.Within(ctx, func(db Store) error {
		actor, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if e = authorize(ctx, db, actor); e != nil {
			return e
		}
		if e = db.Lock(ctx, org); e != nil {
			return e
		}
		replay, found, e := db.Replay(ctx, org, key, hash)
		if e != nil {
			return e
		}
		if found {
			return json.Unmarshal(replay, &out)
		}
		out, e = perform(db, actor)
		if e != nil {
			return e
		}
		body, e := json.Marshal(out)
		if e != nil {
			return e
		}
		return db.Remember(ctx, actor, org, key, hash, body)
	})
	return
}
func (s *Service) CreateSupplier(ctx context.Context, user, org, key string, input domain.Supplier) (domain.Supplier, error) {
	v, e := domain.NormalizeSupplier(input)
	if e != nil {
		return v, e
	}
	return write(ctx, s, user, org, "supplier", key, v, roleSite(org, "", domain.CanConfigure), func(db Store, a domain.Actor) (domain.Supplier, error) { return db.CreateSupplier(ctx, a, org, v) })
}
func (s *Service) CreateProduct(ctx context.Context, user, org, key string, input domain.Product) (domain.Product, error) {
	v, e := domain.NormalizeProduct(input)
	if e != nil {
		return v, e
	}
	return write(ctx, s, user, org, "product", key, v, roleSite(org, "", domain.CanConfigure), func(db Store, a domain.Actor) (domain.Product, error) { return db.CreateProduct(ctx, a, org, v) })
}
func (s *Service) UpdatePolicy(ctx context.Context, user, org, id, key string, v domain.TemperaturePolicy) (domain.Product, error) {
	if !domain.ValidID(id) {
		return domain.Product{}, domain.ErrNotFound
	}
	if e := domain.ValidatePolicy(v); e != nil {
		return domain.Product{}, e
	}
	return write(ctx, s, user, org, "policy:"+id, key, v, roleSite(org, "", domain.CanConfigure), func(db Store, a domain.Actor) (domain.Product, error) { return db.UpdatePolicy(ctx, a, org, id, v) })
}
func (s *Service) Receive(ctx context.Context, user, org, key string, input domain.Receipt) (domain.Receipt, error) {
	v, e := domain.NormalizeReceipt(input, s.now())
	if e != nil {
		return v, e
	}
	return write(ctx, s, user, org, "receipt", key, v, roleSite(org, v.LocationID, domain.CanReceive), func(db Store, a domain.Actor) (domain.Receipt, error) {
		v.RequestHash, e = requestHash(user, "receipt", v)
		if e != nil {
			return v, e
		}
		previous, readErr := db.Receipt(ctx, org, v.ID)
		if readErr == nil {
			if previous.RequestHash != v.RequestHash || previous.ReceivedBy != a.ID || previous.LocationID != v.LocationID || previous.ClientOperationID != v.ClientOperationID {
				return v, domain.ErrConflict
			}
			return previous, nil
		}
		if !errors.Is(readErr, domain.ErrNotFound) {
			return v, readErr
		}
		supplier, e := db.Supplier(ctx, org, v.SupplierID)
		if e != nil {
			return v, e
		}
		zone, e := db.Location(ctx, a, org, v.LocationID)
		if e != nil {
			return v, e
		}
		location, e := time.LoadLocation(zone)
		if e != nil {
			return v, e
		}
		for i := range v.Items {
			item := &v.Items[i]
			product, e := db.Product(ctx, org, item.ProductID)
			if e != nil {
				return v, e
			}
			if e = domain.ValidateReceiptItem(*item, product, v.ReceivedAt.In(location).Format("2006-01-02")); e != nil {
				if validation, ok := e.(*domain.ValidationError); ok {
					fields := map[string]string{}
					for key, value := range validation.Fields {
						fields[fmt.Sprintf("items.%d.%s", i, key)] = value
					}
					return v, &domain.ValidationError{Fields: fields}
				}
				return v, e
			}
			item.ProductName = product.Name
			item.Unit = product.Unit
			item.MinimumTemperature = product.MinimumTemperature
			item.MaximumTemperature = product.MaximumTemperature
		}
		v.ReceivedBy = a.ID
		v.SupplierName = supplier.Name
		return db.CreateReceipt(ctx, a, org, v)
	})
}
func (s *Service) CreatePlan(ctx context.Context, user, org, key string, input domain.CleaningPlan) (domain.CleaningPlan, error) {
	v, e := domain.NormalizePlan(input)
	if e != nil {
		return v, e
	}
	return write(ctx, s, user, org, "plan", key, v, roleSite(org, v.LocationID, domain.CanConfigure), func(db Store, a domain.Actor) (domain.CleaningPlan, error) { return db.CreatePlan(ctx, a, org, v) })
}
func (s *Service) Execute(ctx context.Context, user, org, key string, input domain.CleaningExecution) (domain.CleaningExecution, error) {
	v, e := domain.NormalizeExecution(input, s.now())
	if e != nil {
		return v, e
	}
	return write(ctx, s, user, org, "execution", key, v, roleSite(org, v.LocationID, domain.CanExecuteCleaning), func(db Store, a domain.Actor) (domain.CleaningExecution, error) {
		v.RequestHash, e = requestHash(user, "execution", v)
		if e != nil {
			return v, e
		}
		previous, readErr := db.Execution(ctx, org, v.ID)
		if readErr == nil {
			if previous.RequestHash != v.RequestHash || previous.ResponsibleUserID != a.ID || previous.LocationID != v.LocationID || previous.ClientOperationID != v.ClientOperationID {
				return v, domain.ErrConflict
			}
			return previous, nil
		}
		if !errors.Is(readErr, domain.ErrNotFound) {
			return v, readErr
		}
		plan, e := db.Plan(ctx, org, v.CleaningPlanID)
		if e != nil {
			return v, e
		}
		if !plan.Active || plan.LocationID != v.LocationID {
			return v, domain.ErrNotFound
		}
		v.ResponsibleUserID = a.ID
		v.Area = plan.Area
		v.Task = plan.Task
		return db.CreateExecution(ctx, a, org, v)
	})
}
func (s *Service) Receipt(ctx context.Context, user, org, id string) (out domain.Receipt, err error) {
	if !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if !domain.CanReadStock(a.Role) {
			return domain.ErrDenied
		}
		v, e := db.Receipt(ctx, org, id)
		if e != nil {
			return e
		}
		if _, e = db.Location(ctx, a, org, v.LocationID); e != nil {
			return e
		}
		out = v
		return nil
	})
	return
}
func (s *Service) Execution(ctx context.Context, user, org, id string) (out domain.CleaningExecution, err error) {
	if !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if !domain.CanReadCleaning(a.Role) {
			return domain.ErrDenied
		}
		v, e := db.Execution(ctx, org, id)
		if e != nil {
			return e
		}
		if _, e = db.Location(ctx, a, org, v.LocationID); e != nil {
			return e
		}
		if a.Role == "WORKER" && v.ResponsibleUserID != a.ID {
			return domain.ErrDenied
		}
		out = v
		return nil
	})
	return
}

// list vincula la continuación a identidad, recurso y sede, sin sustituir permisos.
func list[T any](ctx context.Context, s *Service, user, org, resource, site string, request pagination.Request, allowed func(string) bool, fetch func(Store, domain.Actor, pagination.Window) ([]T, error), position func(T) pagination.Position) (out pagination.Page[T], err error) {
	if site != "" && !domain.ValidID(site) {
		return out, pagination.ErrInvalid
	}
	request, e := pagination.Normalize(request)
	if e != nil {
		return out, e
	}
	scope := []string{user, org, "operations", resource, site}
	window, e := pagination.Open(request, scope...)
	if e != nil {
		return out, e
	}
	err = s.work.Within(ctx, func(db Store) error {
		a, e := db.Actor(ctx, user, org)
		if e != nil {
			return e
		}
		if e = roleSite(org, site, allowed)(ctx, db, a); e != nil {
			return e
		}
		rows, e := fetch(db, a, window)
		if e != nil {
			return e
		}
		out = pagination.Close(rows, window, position, scope...)
		return nil
	})
	return
}
func (s *Service) Suppliers(ctx context.Context, user, org string, r pagination.Request) (pagination.Page[domain.Supplier], error) {
	return list(ctx, s, user, org, "suppliers", "", r, domain.CanReadStock, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.Supplier, error) {
		return db.Suppliers(ctx, org, w)
	}, func(v domain.Supplier) pagination.Position {
		return pagination.Position{Key: strings.ToLower(v.Name), ID: v.ID}
	})
}
func (s *Service) Products(ctx context.Context, user, org string, r pagination.Request) (pagination.Page[domain.Product], error) {
	return list(ctx, s, user, org, "products", "", r, domain.CanReadStock, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.Product, error) {
		return db.Products(ctx, org, w)
	}, func(v domain.Product) pagination.Position {
		return pagination.Position{Key: strings.ToLower(v.Name), ID: v.ID}
	})
}
func (s *Service) Receipts(ctx context.Context, user, org, site string, r pagination.Request) (pagination.Page[domain.Receipt], error) {
	if site == "" {
		return pagination.Page[domain.Receipt]{}, pagination.ErrInvalid
	}
	return list(ctx, s, user, org, "receipts", site, r, domain.CanReadStock, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.Receipt, error) {
		return db.Receipts(ctx, org, site, w)
	}, func(v domain.Receipt) pagination.Position {
		return pagination.Position{Key: v.ReceivedAt.UTC().Format(time.RFC3339Nano), ID: v.ID}
	})
}
func (s *Service) Lots(ctx context.Context, user, org, site string, r pagination.Request) (pagination.Page[domain.Lot], error) {
	if site == "" {
		return pagination.Page[domain.Lot]{}, pagination.ErrInvalid
	}
	return list(ctx, s, user, org, "lots", site, r, domain.CanReadStock, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.Lot, error) {
		return db.Lots(ctx, org, site, w)
	}, func(v domain.Lot) pagination.Position {
		return pagination.Position{Key: v.ReceivedAt.UTC().Format(time.RFC3339Nano) + "|" + v.ReceiptItemID, ID: v.ID}
	})
}
func (s *Service) Plans(ctx context.Context, user, org, site string, r pagination.Request) (pagination.Page[domain.CleaningPlan], error) {
	if site == "" {
		return pagination.Page[domain.CleaningPlan]{}, pagination.ErrInvalid
	}
	return list(ctx, s, user, org, "plans", site, r, domain.CanReadCleaning, func(db Store, _ domain.Actor, w pagination.Window) ([]domain.CleaningPlan, error) {
		return db.Plans(ctx, org, site, w)
	}, func(v domain.CleaningPlan) pagination.Position {
		return pagination.Position{Key: strings.ToLower(v.Area + " " + v.Task), ID: v.ID}
	})
}
func (s *Service) Executions(ctx context.Context, user, org, site string, r pagination.Request) (pagination.Page[domain.CleaningExecution], error) {
	if site == "" {
		return pagination.Page[domain.CleaningExecution]{}, pagination.ErrInvalid
	}
	return list(ctx, s, user, org, "executions", site, r, domain.CanReadCleaning, func(db Store, a domain.Actor, w pagination.Window) ([]domain.CleaningExecution, error) {
		return db.Executions(ctx, a, org, site, w)
	}, func(v domain.CleaningExecution) pagination.Position {
		return pagination.Position{Key: v.ExecutedAt.UTC().Format(time.RFC3339Nano), ID: v.ID}
	})
}
